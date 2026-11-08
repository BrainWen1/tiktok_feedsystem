package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"feedsystem/internal/utils/file"
	"feedsystem/internal/utils/logger"

	"github.com/google/uuid"
)

const (
	// chunkLockTTL 单分片写入的锁过期时间，覆盖一次 5MB 落盘 + Redis 回写
	chunkLockTTL = 30 * time.Second
	// mergeLockTTL 合并锁过期时间 = 10min；覆盖大文件顺序读取 + hash 计算
	mergeLockTTL = 10 * time.Minute
	// cancelLockTTL 取消上传需要递归删除整个分片目录，给锁留出更长时间
	cancelLockTTL = 5 * time.Minute
	// chunkLockWait 抢锁最长等待时间，超时返回可重试错误
	chunkLockWait = 3 * time.Second
	// chunkLockRetryInterval 抢锁失败后的轮询间隔
	chunkLockRetryInterval = 50 * time.Millisecond
	// chunkIncompleteTTL 未完成上传的分片目录保留时间，超过后定时任务回收
	chunkIncompleteTTL = 24 * time.Hour
	// uploadIDPattern upload_id 只允许安全字符，避免被拼进文件路径
	uploadIDPattern = `^[A-Za-z0-9_-]{8,64}$`
)

var (
	// ErrStorageUnavailable 分片会话依赖 Redis，Redis 不可用时上传功能不可用
	ErrStorageUnavailable = errors.New("chunk upload storage unavailable")
	// ErrSessionNotFound 上传会话不存在或已过期
	ErrSessionNotFound = errors.New("upload session not found or expired")
	// ErrSessionCorrupted 会话元数据与规则不一致
	ErrSessionCorrupted = errors.New("upload session corrupted")
	// ErrUploadForbidden 上传会话不属于当前用户
	ErrUploadForbidden = errors.New("upload session does not belong to user")
	// ErrSessionBusy 会话正在被其他分片写入或合并
	ErrSessionBusy = errors.New("upload session is busy")
	// ErrInvalidUploadRequest 创建会话参数不合法
	ErrInvalidUploadRequest = errors.New("invalid upload request")
	// ErrInvalidChunkIndex 分片下标越界
	ErrInvalidChunkIndex = errors.New("invalid chunk index")
	// ErrInvalidChunkSize 分片大小与预期不一致（网络截断或重复上传错片）
	ErrInvalidChunkSize = errors.New("invalid chunk size")
	// ErrChunksIncomplete 仍有分片未上传
	ErrChunksIncomplete = errors.New("chunks incomplete")
	// ErrChunkMissing 分片标记为已上传，但落盘文件缺失或大小不符
	ErrChunkMissing = errors.New("chunk file missing or corrupted")
	// ErrFileHashMismatch 合并结果与客户端声明的 sha256 不一致
	ErrFileHashMismatch = errors.New("file hash mismatch")

	uploadIDRegexp = regexp.MustCompile(uploadIDPattern)
)

// ChunkService 分片上传业务层，缓存、校验、合并逻辑放这里。
// 存储路径统一由 utils/file 根据 config.AppConfig.StorageRoot 推导。
type ChunkService struct {
	repo *repo.ChunkRepo
}

func NewChunkService(repo *repo.ChunkRepo) *ChunkService {
	return &ChunkService{repo: repo}
}

// CreateUploadSession 创建上传会话。
// 创建前完成文件名、后缀、大小、hash 格式与分片数量校验，异常请求不占用 Redis/磁盘。
func (s *ChunkService) CreateUploadSession(ctx context.Context, accountID uint, filename string, fileSize int64, fileHash string) (*model.ChunkUploadSession, error) {
	if accountID == 0 {
		return nil, fmt.Errorf("%w: account id is required", ErrInvalidUploadRequest)
	}
	if !s.repo.Enabled() {
		return nil, ErrStorageUnavailable
	}

	// 仅保留文件名本身，禁止路径分隔符进入会话元数据
	filename = strings.TrimSpace(filepath.Base(strings.TrimSpace(filename)))
	if filename == "" || filename == "." || len(filename) > 255 {
		return nil, fmt.Errorf("%w: invalid filename", ErrInvalidUploadRequest)
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if !file.IsAllowedExt(ext, file.VideoUploadSpec.AllowedExts) {
		return nil, fmt.Errorf("%w: unsupported video extension %q", ErrInvalidUploadRequest, ext)
	}
	if fileSize <= 0 || fileSize > model.MaxChunkUploadSize {
		return nil, fmt.Errorf("%w: file size %d out of range", ErrInvalidUploadRequest, fileSize)
	}
	totalChunks := int((fileSize + model.ChunkSize - 1) / model.ChunkSize)
	if totalChunks <= 0 || totalChunks > model.MaxChunkCount {
		return nil, fmt.Errorf("%w: invalid chunk count %d", ErrInvalidUploadRequest, totalChunks)
	}
	if !isValidSHA256(fileHash) {
		return nil, fmt.Errorf("%w: invalid file hash", ErrInvalidUploadRequest)
	}

	uploadID := uuid.NewString()
	// 会话创建即建立 chunks/{upload_id} 目录，便于 GC 从目录维度统计未完成任务；
	// 目录创建失败时不必写 Redis，避免产生无法落盘的“幽灵会话”。
	if err := file.EnsureDir(file.ChunkUploadDir(uploadID)); err != nil {
		return nil, fmt.Errorf("create chunk directory: %w", err)
	}

	now := time.Now()
	sess := &model.ChunkUploadSession{
		UploadID:     uploadID,
		AccountID:    accountID,
		Filename:     filename,
		FileSize:     fileSize,
		ChunkSize:    model.ChunkSize,
		TotalChunks:  totalChunks,
		FileHash:     strings.ToLower(fileHash),
		UploadedBits: make([]bool, totalChunks),
		ExpireAt:     now.Add(model.ChunkSessionTTL),
	}
	if err := s.repo.SaveSession(ctx, sess, model.ChunkSessionTTL); err != nil {
		_ = os.RemoveAll(file.ChunkUploadDir(uploadID))
		return nil, fmt.Errorf("save upload session: %w", err)
	}
	return sess, nil
}

// SaveChunk 保存单个分片。
//
// 网络中断处理：
//   - 请求体直接流式写入同目录临时文件，fsync 后再原子 rename，失败不会留下半截分片；
//   - 超过预期字节数立即判定为非法分片，不把整个分片读进堆内存；
//   - 严格校验分片下标与字节数（最后一片为剩余大小），截断/错片直接拒绝且不标记；
//   - 客户端重试同一分片会覆盖原文件，操作幂等；
//   - 整个“读会话-落盘-回写标记”过程持有会话锁，避免并发上传丢标记。
func (s *ChunkService) SaveChunk(ctx context.Context, accountID uint, uploadID string, chunkIndex int, chunkBody io.Reader) (*model.ChunkUploadSession, error) {
	if !s.repo.Enabled() {
		return nil, ErrStorageUnavailable
	}
	if !isValidUploadID(uploadID) {
		return nil, fmt.Errorf("%w: invalid upload id", ErrSessionNotFound)
	}

	token, err := s.acquireLock(ctx, uploadID, chunkLockTTL)
	if err != nil {
		return nil, err
	}
	defer s.releaseLock(uploadID, token)

	sess, err := s.getOwnedSession(ctx, accountID, uploadID)
	if err != nil {
		return nil, err
	}
	if chunkIndex < 0 || chunkIndex >= sess.TotalChunks {
		return nil, fmt.Errorf("%w: index=%d total=%d", ErrInvalidChunkIndex, chunkIndex, sess.TotalChunks)
	}
	expected := sess.ExpectedChunkSize(chunkIndex)
	if expected <= 0 {
		return nil, fmt.Errorf("%w: index=%d invalid expected size %d", ErrInvalidChunkSize, chunkIndex, expected)
	}

	chunkDir := file.ChunkUploadDir(sess.UploadID)
	if err := file.EnsureDir(chunkDir); err != nil {
		return nil, fmt.Errorf("ensure chunk directory: %w", err)
	}
	if err := writeChunkFromReader(chunkDir, chunkIndex, chunkBody, expected); err != nil {
		return nil, fmt.Errorf("write chunk %d: %w", chunkIndex, err)
	}

	// 分片已可靠落盘后再更新标记；Redis 写入失败时客户端重试即可，
	// 由于重试会覆盖同一分片文件，不会产生重复数据。
	sess.UploadedBits[chunkIndex] = true
	// 滑动续期：持续上传的任务不会因固定 TTL 在中途过期
	sess.ExpireAt = time.Now().Add(model.ChunkSessionTTL)
	if err := s.repo.SaveSession(ctx, sess, model.ChunkSessionTTL); err != nil {
		return nil, fmt.Errorf("update upload session: %w", err)
	}
	return sess, nil
}

// MergeChunks 合并所有分片并校验 sha256，成功返回前端可播放的 /upload/videos/... 路径。
//
// 合并前逐片校验落盘文件是否存在且大小正确，避免“标记位已上传但文件丢失”导致
// 合并出损坏视频；合并先写临时文件，校验通过后再原子重命名，失败不会暴露半成品。
func (s *ChunkService) MergeChunks(ctx context.Context, accountID uint, uploadID string) (string, error) {
	if !s.repo.Enabled() {
		return "", ErrStorageUnavailable
	}
	if !isValidUploadID(uploadID) {
		return "", fmt.Errorf("%w: invalid upload id", ErrSessionNotFound)
	}

	token, err := s.acquireLock(ctx, uploadID, mergeLockTTL)
	if err != nil {
		return "", err
	}
	defer s.releaseLock(uploadID, token)

	sess, err := s.getOwnedSession(ctx, accountID, uploadID)
	if err != nil {
		return "", err
	}

	ext := strings.ToLower(filepath.Ext(sess.Filename))
	if !file.IsAllowedExt(ext, file.VideoUploadSpec.AllowedExts) {
		return "", fmt.Errorf("%w: unsupported video extension", ErrSessionCorrupted)
	}
	videoDir := file.UserUploadDir(file.VideosSubDir, sess.AccountID)
	finalName := sess.UploadID + ext
	finalPath := filepath.Join(videoDir, finalName)
	publicURL := file.PublicUploadPath(file.VideosSubDir, sess.AccountID, finalName)
	chunkDir := file.ChunkUploadDir(sess.UploadID)

	// 幂等补偿：上次合并已重命名成品，但删除分片/会话前进程退出。
	// 成品文件大小一致时直接完成清理并返回，避免客户端因重试拿到“分片缺失”。
	if info, statErr := os.Stat(finalPath); statErr == nil && info.Mode().IsRegular() && info.Size() == sess.FileSize {
		s.cleanupAfterMerge(chunkDir, sess.UploadID)
		return publicURL, nil
	}

	if !sess.IsAllDone() {
		return "", ErrChunksIncomplete
	}
	// 合并前预检：标记位为 true 不代表文件一定还在（磁盘清理、人工误删、半截写入）
	for i := 0; i < sess.TotalChunks; i++ {
		if err := verifyChunkFile(chunkDir, i, sess.ExpectedChunkSize(i)); err != nil {
			return "", err
		}
	}

	if err := file.EnsureDir(videoDir); err != nil {
		return "", fmt.Errorf("ensure video directory: %w", err)
	}
	tmpFile, err := os.CreateTemp(videoDir, ".merge-"+sess.UploadID+"-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create merge temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmpFile.Close()
			_ = os.Remove(tmpName)
		}
	}()

	hash := sha256.New()
	writer := io.MultiWriter(tmpFile, hash)
	for i := 0; i < sess.TotalChunks; i++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		if err := appendChunk(writer, chunkDir, i, sess.ExpectedChunkSize(i)); err != nil {
			return "", err
		}
	}

	if err := tmpFile.Sync(); err != nil {
		return "", fmt.Errorf("sync merged file: %w", err)
	}
	if err := tmpFile.Chmod(0o644); err != nil {
		return "", fmt.Errorf("set merged file permission: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return "", fmt.Errorf("close merged file: %w", err)
	}

	// hash 为空表示客户端未提供校验值，跳过比对但仍计算（成本很低）
	if sess.FileHash != "" {
		gotHash := hex.EncodeToString(hash.Sum(nil))
		if !strings.EqualFold(gotHash, sess.FileHash) {
			// 保留分片与会话，客户端可重新上传缺失/损坏的分片后再合并
			return "", fmt.Errorf("%w: got=%s want=%s", ErrFileHashMismatch, gotHash, sess.FileHash)
		}
	}

	if err := renameReplace(tmpName, finalPath); err != nil {
		return "", fmt.Errorf("commit merged video: %w", err)
	}
	committed = true

	// 合并成功立即回收分片目录与 Redis 会话，避免碎片长期占用 StorageRoot
	s.cleanupAfterMerge(chunkDir, sess.UploadID)
	return publicURL, nil
}

// GetSession 查询会话状态（前端据此续传缺失分片）。
func (s *ChunkService) GetSession(ctx context.Context, accountID uint, uploadID string) (*model.ChunkUploadSession, error) {
	if !s.repo.Enabled() {
		return nil, ErrStorageUnavailable
	}
	if !isValidUploadID(uploadID) {
		return nil, fmt.Errorf("%w: invalid upload id", ErrSessionNotFound)
	}
	return s.getOwnedSession(ctx, accountID, uploadID)
}

// CancelUpload 取消上传，清理临时文件与 Redis 会话。
func (s *ChunkService) CancelUpload(ctx context.Context, accountID uint, uploadID string) error {
	if !s.repo.Enabled() {
		return ErrStorageUnavailable
	}
	if !isValidUploadID(uploadID) {
		return fmt.Errorf("%w: invalid upload id", ErrSessionNotFound)
	}

	token, err := s.acquireLock(ctx, uploadID, cancelLockTTL)
	if err != nil {
		return err
	}
	defer s.releaseLock(uploadID, token)

	sess, err := s.getOwnedSession(ctx, accountID, uploadID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(file.ChunkUploadDir(sess.UploadID)); err != nil {
		return fmt.Errorf("remove chunk directory: %w", err)
	}
	if err := s.repo.DeleteSession(ctx, uploadID); err != nil {
		return fmt.Errorf("delete upload session: %w", err)
	}
	return nil
}

// RunGarbageCollector 启动分片垃圾回收循环，阻塞直到 ctx 取消。
// 启动时先执行一次，之后按 interval 周期扫描。
func (s *ChunkService) RunGarbageCollector(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	logger.Info("chunk garbage collector started",
		logger.String("chunks_root", file.ChunksRoot()),
		logger.Duration("incomplete_ttl", chunkIncompleteTTL),
		logger.Duration("interval", interval))

	s.CleanExpiredChunks(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("chunk garbage collector stopped")
			return
		case <-ticker.C:
			s.CleanExpiredChunks(ctx)
		}
	}
}

// CleanExpiredChunks 删除超过 24 小时仍未完成合并的分片目录，并同步清理 Redis 会话。
// 会话仍在有效期内时跳过，避免误删正在断点续传的任务。
func (s *ChunkService) CleanExpiredChunks(ctx context.Context) {
	if !s.repo.Enabled() {
		logger.Warn("skip chunk garbage collection: redis unavailable")
		return
	}
	entries, err := os.ReadDir(file.ChunksRoot())
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warn("read chunks root failed", logger.Err(err), logger.String("dir", file.ChunksRoot()))
		}
		return
	}

	now := time.Now()
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			logger.Warn("stat chunk directory failed", logger.Err(err), logger.String("dir", entry.Name()))
			continue
		}
		if now.Sub(info.ModTime()) < chunkIncompleteTTL {
			continue
		}

		uploadID := entry.Name()
		sess, sessErr := s.repo.GetSession(ctx, uploadID)
		switch {
		case sessErr == nil && now.Before(sess.ExpireAt):
			// 会话仍然有效，说明客户端可能正在断点续传
			continue
		case sessErr != nil && !repo.IsSessionNotFound(sessErr):
			// Redis 抖动时保守跳过，避免误删有效上传
			logger.Warn("skip chunk cleanup: read session failed",
				logger.Err(sessErr), logger.String("upload_id", uploadID))
			continue
		}

		chunkDir := file.ChunkUploadDir(uploadID)
		if err := os.RemoveAll(chunkDir); err != nil {
			logger.Warn("remove expired chunk directory failed",
				logger.Err(err), logger.String("upload_id", uploadID), logger.String("dir", chunkDir))
			continue
		}
		if err := s.repo.DeleteSession(ctx, uploadID); err != nil {
			// 目录已删除，会话残留会随 TTL 自动过期，记录日志即可
			logger.Warn("delete expired upload session failed",
				logger.Err(err), logger.String("upload_id", uploadID))
		}
		removed++
	}
	if removed > 0 {
		logger.Info("expired chunk directories cleaned", logger.Int("removed", removed))
	}
}

// getOwnedSession 读取会话并校验归属与元数据一致性。
func (s *ChunkService) getOwnedSession(ctx context.Context, accountID uint, uploadID string) (*model.ChunkUploadSession, error) {
	sess, err := s.repo.GetSession(ctx, uploadID)
	if err != nil {
		if repo.IsSessionNotFound(err) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("get upload session: %w", err)
	}
	if sess.UploadID != uploadID || sess.TotalChunks <= 0 || len(sess.UploadedBits) != sess.TotalChunks {
		return nil, ErrSessionCorrupted
	}
	if sess.AccountID != accountID {
		return nil, ErrUploadForbidden
	}
	return sess, nil
}

// acquireLock 在限定时间内争抢 upload_id 维度的分布式锁。
func (s *ChunkService) acquireLock(ctx context.Context, uploadID string, ttl time.Duration) (string, error) {
	deadline := time.Now().Add(chunkLockWait)
	for {
		token, ok, err := s.repo.LockSession(ctx, uploadID, ttl)
		if err != nil {
			return "", fmt.Errorf("lock upload session: %w", err)
		}
		if ok {
			return token, nil
		}
		if time.Now().After(deadline) {
			return "", ErrSessionBusy
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(chunkLockRetryInterval):
		}
	}
}

// releaseLock 使用独立 context 释放锁，避免请求取消后锁只能等 TTL 自然过期。
func (s *ChunkService) releaseLock(uploadID, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.repo.UnlockSession(ctx, uploadID, token); err != nil {
		logger.Warn("release upload session lock failed",
			logger.Err(err), logger.String("upload_id", uploadID))
	}
}

// writeChunkAtomic 把内存中的分片写入同目录临时文件，fsync 后原子 rename 为正式分片名。
// 保留该函数供内部工具与单测使用，实际请求链路走 writeChunkFromReader 流式落盘。
func writeChunkAtomic(chunkDir string, chunkIndex int, data []byte) error {
	return writeChunkFromReader(chunkDir, chunkIndex, bytes.NewReader(data), int64(len(data)))
}

// writeChunkFromReader 把分片请求体流式写入同目录临时文件，fsync 后原子 rename。
// 使用 LimitReader(expected+1) 判断实际字节数：多读 1 字节即可发现超长请求，
// 同时避免把整个分片读进堆内存；任何一步失败都会删除临时文件，
// 正式分片要么完整存在，要么不存在。
func writeChunkFromReader(chunkDir string, chunkIndex int, src io.Reader, expectedSize int64) (err error) {
	tmpFile, err := os.CreateTemp(chunkDir, fmt.Sprintf(".%d.tmp-*", chunkIndex))
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()
	defer func() {
		if err != nil {
			_ = tmpFile.Close()
			_ = os.Remove(tmpName)
		}
	}()

	written, err := io.Copy(tmpFile, io.LimitReader(src, expectedSize+1))
	if err != nil {
		// 请求体超过 MaxBytesReader 上限时按分片大小不合法返回，客户端应修正分片大小
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return fmt.Errorf("%w: %v", ErrInvalidChunkSize, err)
		}
		return err
	}
	if written != expectedSize {
		return fmt.Errorf("%w: got=%d want=%d", ErrInvalidChunkSize, written, expectedSize)
	}
	if err = tmpFile.Sync(); err != nil {
		return err
	}
	if err = tmpFile.Chmod(0o644); err != nil {
		return err
	}
	if err = tmpFile.Close(); err != nil {
		return err
	}

	finalPath := filepath.Join(chunkDir, strconv.Itoa(chunkIndex))
	// 重传同一分片时覆盖旧文件。会话锁保证同一 upload_id 不会并发写同一分片。
	if err = renameReplace(tmpName, finalPath); err != nil {
		return err
	}
	return nil
}

// renameReplace 原子替换目标文件。
// POSIX 的 rename 可直接覆盖；Windows 会返回“文件已存在”，此时删除旧文件后重试。
func renameReplace(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(src, dst)
}

// verifyChunkFile 校验分片文件存在、为普通文件且大小符合预期。
func verifyChunkFile(chunkDir string, index int, expectedSize int64) error {
	path := filepath.Join(chunkDir, strconv.Itoa(index))
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: index=%d", ErrChunkMissing, index)
		}
		return fmt.Errorf("stat chunk %d: %w", index, err)
	}
	if !info.Mode().IsRegular() || info.Size() != expectedSize {
		return fmt.Errorf("%w: index=%d got=%d want=%d", ErrChunkMissing, index, info.Size(), expectedSize)
	}
	return nil
}

// appendChunk 顺序读取一个分片写入合并目标，并校验实际拷贝字节数。
func appendChunk(writer io.Writer, chunkDir string, index int, expectedSize int64) error {
	src, err := os.Open(filepath.Join(chunkDir, strconv.Itoa(index)))
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: index=%d", ErrChunkMissing, index)
		}
		return fmt.Errorf("open chunk %d: %w", index, err)
	}
	defer src.Close()

	written, err := io.Copy(writer, src)
	if err != nil {
		return fmt.Errorf("merge chunk %d: %w", index, err)
	}
	if written != expectedSize {
		return fmt.Errorf("%w: index=%d got=%d want=%d", ErrChunkMissing, index, written, expectedSize)
	}
	return nil
}

// cleanupAfterMerge 合并成功后立即删除整个分片目录并清理 Redis 会话。
// 使用独立 context，避免客户端断开导致清理动作被取消。
func (s *ChunkService) cleanupAfterMerge(chunkDir, uploadID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := os.RemoveAll(chunkDir); err != nil {
		logger.Ctx(ctx).Warn("remove merged chunk directory failed",
			logger.Err(err), logger.String("upload_id", uploadID), logger.String("dir", chunkDir))
	}
	if err := s.repo.DeleteSession(ctx, uploadID); err != nil {
		logger.Ctx(ctx).Warn("delete merged upload session failed",
			logger.Err(err), logger.String("upload_id", uploadID))
	}
}

// isValidUploadID 校验 upload_id 字符集与长度，防止路径穿越。
func isValidUploadID(uploadID string) bool {
	return uploadIDRegexp.MatchString(uploadID)
}

// isValidSHA256 校验客户端提供的 sha256 十六进制串；空串表示不校验。
func isValidSHA256(hash string) bool {
	if hash == "" {
		return true
	}
	if len(hash) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}
