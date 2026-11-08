package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"feedsystem/internal/config"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	fileutil "feedsystem/internal/utils/file"

	"github.com/alicebob/miniredis/v2"
)

// setupChunkService 组装本地分片上传测试环境：
//   - StorageRoot 指向 t.TempDir()，所有分片/成品文件写入临时目录并自动清理；
//   - miniredis 替代真实 Redis，覆盖会话、分片标记与分布式锁。
func setupChunkService(t *testing.T) (*ChunkService, *miniredis.Miniredis, string) {
	t.Helper()

	root := t.TempDir()
	oldRoot := config.AppConfig.StorageRoot
	config.AppConfig.StorageRoot = root
	t.Cleanup(func() { config.AppConfig.StorageRoot = oldRoot })

	mr := miniredis.RunT(t)
	redisCache := cache.NewRedisCache(mr.Addr(), "", 0)
	t.Cleanup(func() { _ = redisCache.Close() })

	return NewChunkService(repo.NewChunkRepo(redisCache)), mr, root
}

func TestChunkServiceUploadAndMerge(t *testing.T) {
	svc, mr, root := setupChunkService(t)
	ctx := context.Background()

	first := bytes.Repeat([]byte{0x11}, int(model.ChunkSize))
	last := bytes.Repeat([]byte{0x22}, 64)
	full := append(append([]byte{}, first...), last...)
	sum := sha256.Sum256(full)

	sess, err := svc.CreateUploadSession(ctx, 7, "clip.mp4", int64(len(full)), hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("CreateUploadSession() error = %v", err)
	}
	if sess.TotalChunks != 2 {
		t.Fatalf("TotalChunks = %d, want 2", sess.TotalChunks)
	}

	if _, err := svc.SaveChunk(ctx, 7, sess.UploadID, 0, first); err != nil {
		t.Fatalf("SaveChunk(0) error = %v", err)
	}
	// 重传同一分片必须幂等
	if _, err := svc.SaveChunk(ctx, 7, sess.UploadID, 0, first); err != nil {
		t.Fatalf("SaveChunk(0) retry error = %v", err)
	}

	status, err := svc.GetSession(ctx, 7, sess.UploadID)
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if got := status.UploadedChunks(); len(got) != 1 || got[0] != 0 {
		t.Fatalf("UploadedChunks() = %v, want [0]", got)
	}

	if _, err := svc.SaveChunk(ctx, 7, sess.UploadID, 1, last); err != nil {
		t.Fatalf("SaveChunk(1) error = %v", err)
	}

	videoURL, err := svc.MergeChunks(ctx, 7, sess.UploadID)
	if err != nil {
		t.Fatalf("MergeChunks() error = %v", err)
	}
	wantURL := fileutil.PublicUploadPath(fileutil.VideosSubDir, 7, sess.UploadID+".mp4")
	if videoURL != wantURL {
		t.Fatalf("MergeChunks() url = %q, want %q", videoURL, wantURL)
	}

	mergedPath := filepath.Join(root, fileutil.UploadDirName, fileutil.VideosSubDir, "7", sess.UploadID+".mp4")
	got, err := os.ReadFile(mergedPath)
	if err != nil {
		t.Fatalf("read merged video: %v", err)
	}
	if !bytes.Equal(got, full) {
		t.Fatalf("merged content mismatch: got %d bytes, want %d", len(got), len(full))
	}

	// 合并成功后必须立即回收分片目录与会话
	if _, err := os.Stat(fileutil.ChunkUploadDir(sess.UploadID)); !os.IsNotExist(err) {
		t.Fatalf("chunk dir should be removed after merge, stat err = %v", err)
	}
	if mr.Exists("chunk:upload:" + sess.UploadID) {
		t.Fatal("redis session should be deleted after merge")
	}
	if _, err := svc.GetSession(ctx, 7, sess.UploadID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("GetSession() after merge = %v, want ErrSessionNotFound", err)
	}
}

func TestChunkServiceSaveChunkRejectsWrongSize(t *testing.T) {
	svc, _, _ := setupChunkService(t)
	ctx := context.Background()

	sess, err := svc.CreateUploadSession(ctx, 7, "clip.mp4", model.ChunkSize, "")
	if err != nil {
		t.Fatalf("CreateUploadSession() error = %v", err)
	}

	if _, err := svc.SaveChunk(ctx, 7, sess.UploadID, 0, []byte("truncated")); !errors.Is(err, ErrInvalidChunkSize) {
		t.Fatalf("SaveChunk() wrong size error = %v, want ErrInvalidChunkSize", err)
	}
	// 校验失败不得写入标记位
	status, err := svc.GetSession(ctx, 7, sess.UploadID)
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if len(status.UploadedChunks()) != 0 {
		t.Fatalf("rejected chunk must not be marked uploaded, got %v", status.UploadedChunks())
	}

	// 下标越界
	if _, err := svc.SaveChunk(ctx, 7, sess.UploadID, 1, []byte("x")); !errors.Is(err, ErrInvalidChunkIndex) {
		t.Fatalf("SaveChunk() out-of-range error = %v, want ErrInvalidChunkIndex", err)
	}
}

func TestChunkServiceMergeIncomplete(t *testing.T) {
	svc, _, _ := setupChunkService(t)
	ctx := context.Background()

	sess, err := svc.CreateUploadSession(ctx, 7, "clip.mp4", 2*model.ChunkSize, "")
	if err != nil {
		t.Fatalf("CreateUploadSession() error = %v", err)
	}
	if _, err := svc.MergeChunks(ctx, 7, sess.UploadID); !errors.Is(err, ErrChunksIncomplete) {
		t.Fatalf("MergeChunks() incomplete error = %v, want ErrChunksIncomplete", err)
	}
}

func TestChunkServiceOwnershipIsolation(t *testing.T) {
	svc, _, _ := setupChunkService(t)
	ctx := context.Background()

	sess, err := svc.CreateUploadSession(ctx, 7, "clip.mp4", 64, "")
	if err != nil {
		t.Fatalf("CreateUploadSession() error = %v", err)
	}
	if _, err := svc.GetSession(ctx, 8, sess.UploadID); !errors.Is(err, ErrUploadForbidden) {
		t.Fatalf("GetSession() as other user = %v, want ErrUploadForbidden", err)
	}
	if _, err := svc.SaveChunk(ctx, 8, sess.UploadID, 0, make([]byte, 64)); !errors.Is(err, ErrUploadForbidden) {
		t.Fatalf("SaveChunk() as other user = %v, want ErrUploadForbidden", err)
	}
	if err := svc.CancelUpload(ctx, 8, sess.UploadID); !errors.Is(err, ErrUploadForbidden) {
		t.Fatalf("CancelUpload() as other user = %v, want ErrUploadForbidden", err)
	}
}

func TestChunkServiceHashMismatchKeepsSession(t *testing.T) {
	svc, _, _ := setupChunkService(t)
	ctx := context.Background()

	data := bytes.Repeat([]byte{0x33}, 64)
	sess, err := svc.CreateUploadSession(ctx, 7, "clip.mp4", int64(len(data)), strings.Repeat("0", 64))
	if err != nil {
		t.Fatalf("CreateUploadSession() error = %v", err)
	}
	if _, err := svc.SaveChunk(ctx, 7, sess.UploadID, 0, data); err != nil {
		t.Fatalf("SaveChunk() error = %v", err)
	}

	if _, err := svc.MergeChunks(ctx, 7, sess.UploadID); !errors.Is(err, ErrFileHashMismatch) {
		t.Fatalf("MergeChunks() hash mismatch error = %v, want ErrFileHashMismatch", err)
	}
	// 校验失败保留分片和会话，客户端可修复后重试
	if _, err := os.Stat(fileutil.ChunkUploadDir(sess.UploadID)); err != nil {
		t.Fatalf("chunk dir should be kept on hash mismatch: %v", err)
	}
	if _, err := svc.GetSession(ctx, 7, sess.UploadID); err != nil {
		t.Fatalf("session should be kept on hash mismatch: %v", err)
	}
}

func TestChunkServiceCancelUploadCleansUp(t *testing.T) {
	svc, mr, _ := setupChunkService(t)
	ctx := context.Background()

	sess, err := svc.CreateUploadSession(ctx, 7, "clip.mp4", 64, "")
	if err != nil {
		t.Fatalf("CreateUploadSession() error = %v", err)
	}
	if _, err := svc.SaveChunk(ctx, 7, sess.UploadID, 0, make([]byte, 64)); err != nil {
		t.Fatalf("SaveChunk() error = %v", err)
	}

	if err := svc.CancelUpload(ctx, 7, sess.UploadID); err != nil {
		t.Fatalf("CancelUpload() error = %v", err)
	}
	if _, err := os.Stat(fileutil.ChunkUploadDir(sess.UploadID)); !os.IsNotExist(err) {
		t.Fatalf("chunk dir should be removed after cancel, stat err = %v", err)
	}
	if mr.Exists("chunk:upload:" + sess.UploadID) {
		t.Fatal("redis session should be removed after cancel")
	}
}

func TestCreateUploadSessionValidation(t *testing.T) {
	svc, _, _ := setupChunkService(t)
	ctx := context.Background()

	cases := []struct {
		name     string
		account  uint
		filename string
		size     int64
		hash     string
	}{
		{name: "zero account", account: 0, filename: "clip.mp4", size: 64},
		{name: "empty filename", account: 7, filename: "", size: 64},
		{name: "unsupported extension", account: 7, filename: "clip.txt", size: 64},
		{name: "zero size", account: 7, filename: "clip.mp4", size: 0},
		{name: "oversize", account: 7, filename: "clip.mp4", size: model.MaxChunkUploadSize + 1},
		{name: "bad hash", account: 7, filename: "clip.mp4", size: 64, hash: "not-a-sha256"},
		{name: "filename too long", account: 7, filename: strings.Repeat("a", 300) + ".mp4", size: 64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.CreateUploadSession(ctx, tc.account, tc.filename, tc.size, tc.hash); !errors.Is(err, ErrInvalidUploadRequest) {
				t.Fatalf("CreateUploadSession() error = %v, want ErrInvalidUploadRequest", err)
			}
		})
	}
}

func TestAppendChunk(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "0"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := appendChunk(&buf, dir, 0, 3); err != nil {
		t.Fatalf("appendChunk() error = %v", err)
	}
	if buf.String() != "abc" {
		t.Fatalf("appendChunk() wrote %q, want %q", buf.String(), "abc")
	}
	if err := appendChunk(&buf, dir, 0, 4); !errors.Is(err, ErrChunkMissing) {
		t.Fatalf("appendChunk() size mismatch = %v, want ErrChunkMissing", err)
	}
	if err := appendChunk(&buf, dir, 1, 3); !errors.Is(err, ErrChunkMissing) {
		t.Fatalf("appendChunk() missing file = %v, want ErrChunkMissing", err)
	}
}

func TestWriteChunkAtomic(t *testing.T) {
	dir := t.TempDir()
	data := []byte("first-version")

	if err := writeChunkAtomic(dir, 0, data); err != nil {
		t.Fatalf("writeChunkAtomic() error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "0"))
	if err != nil {
		t.Fatalf("read chunk: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("chunk content = %q, want %q", got, data)
	}
	info, err := os.Stat(filepath.Join(dir, "0"))
	if err != nil {
		t.Fatalf("stat chunk: %v", err)
	}
	if info.Mode().Perm()&0o111 != 0 {
		t.Fatalf("chunk must not be executable, perm = %v", info.Mode().Perm())
	}

	// 模拟网络中断后重传同一分片：应覆盖旧内容，且不残留临时文件
	replay := []byte("second-version")
	if err := writeChunkAtomic(dir, 0, replay); err != nil {
		t.Fatalf("rewrite chunk: %v", err)
	}
	got, err = os.ReadFile(filepath.Join(dir, "0"))
	if err != nil {
		t.Fatalf("read rewritten chunk: %v", err)
	}
	if string(got) != string(replay) {
		t.Fatalf("rewritten chunk content = %q, want %q", got, replay)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read chunk dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("chunk dir should contain only final chunk, got %d entries", len(entries))
	}
}

func TestVerifyChunkFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "0"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := verifyChunkFile(dir, 0, 3); err != nil {
		t.Fatalf("verify existing chunk: %v", err)
	}
	if err := verifyChunkFile(dir, 0, 4); !errors.Is(err, ErrChunkMissing) {
		t.Fatalf("size mismatch error = %v, want ErrChunkMissing", err)
	}
	if err := verifyChunkFile(dir, 1, 3); !errors.Is(err, ErrChunkMissing) {
		t.Fatalf("missing chunk error = %v, want ErrChunkMissing", err)
	}
}

func TestIsValidUploadID(t *testing.T) {
	valid := []string{"550e8400-e29b-41d4-a716-446655440000", "chunk_123-abc"}
	invalid := []string{"", "short", "../escape", "a/b", strings.Repeat("a", 65)}
	for _, id := range valid {
		if !isValidUploadID(id) {
			t.Fatalf("upload id %q should be valid", id)
		}
	}
	for _, id := range invalid {
		if isValidUploadID(id) {
			t.Fatalf("upload id %q should be invalid", id)
		}
	}
}

func TestIsValidSHA256(t *testing.T) {
	if !isValidSHA256("") {
		t.Fatal("empty hash means skip verification and should be valid")
	}
	if !isValidSHA256(strings.Repeat("a", 64)) {
		t.Fatal("64-char hex hash should be valid")
	}
	for _, hash := range []string{"abc", strings.Repeat("z", 64), strings.Repeat("a", 63)} {
		if isValidSHA256(hash) {
			t.Fatalf("hash %q should be invalid", hash)
		}
	}
}
