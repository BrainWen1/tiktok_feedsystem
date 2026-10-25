package file

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"feedsystem/internal/utils/logger"
)

// sniffLen http.DetectContentType 需要的最大嗅探字节数
const sniffLen = 512

var (
	// ErrInvalidFileSize 上传文件大小不合法
	ErrInvalidFileSize = errors.New("invalid file size")
	// ErrInvalidFileType 上传文件后缀或真实内容类型不合法
	ErrInvalidFileType = errors.New("invalid file type")
)

// UploadSpec 描述一类上传文件的存储位置与校验策略。
type UploadSpec struct {
	Label       string   // 日志中的文件类型标识，如 avatar/video/cover
	SubDir      string   // 上传根目录下的子目录，如 avatars/videos/covers
	MaxSize     int64    // 允许的最大文件字节数
	AllowedExts []string // 允许的文件后缀，包含前导点
}

var (
	// AvatarUploadSpec 头像上传策略：最大 10MB，仅允许图片
	AvatarUploadSpec = UploadSpec{
		Label:       "avatar",
		SubDir:      "avatars",
		MaxSize:     10 << 20,
		AllowedExts: []string{".jpg", ".jpeg", ".png", ".webp"},
	}
	// VideoUploadSpec 视频上传策略：最大 100MB，仅允许视频
	VideoUploadSpec = UploadSpec{
		Label:       "video",
		SubDir:      "videos",
		MaxSize:     100 << 20,
		AllowedExts: []string{".mp4", ".avi", ".mov", ".mkv"},
	}
	// CoverUploadSpec 视频封面上传策略：最大 10MB，仅允许图片
	CoverUploadSpec = UploadSpec{
		Label:       "cover",
		SubDir:      "covers",
		MaxSize:     10 << 20,
		AllowedExts: []string{".jpg", ".jpeg", ".png", ".webp"},
	}
)

// SaveUpload 校验并保存上传文件，返回 http 可访问的相对路径。
// 校验包含大小、后缀以及文件头嗅探出的真实 MIME，三者全部通过才落盘；
// 写入失败时会清理半截文件，避免残留。
func SaveUpload(fh *multipart.FileHeader, userID uint, spec UploadSpec) (string, error) {
	if fh.Size <= 0 || fh.Size > spec.MaxSize {
		logger.Warn("invalid upload file size",
			logger.String("kind", spec.Label),
			logger.Uint("user_id", userID),
			logger.Int64("size", fh.Size))
		return "", fmt.Errorf("%w: size=%d", ErrInvalidFileSize, fh.Size)
	}

	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if !isAllowedExt(ext, spec.AllowedExts) {
		logger.Warn("invalid upload file type",
			logger.String("kind", spec.Label),
			logger.Uint("user_id", userID),
			logger.String("ext", ext))
		return "", fmt.Errorf("%w: ext=%s", ErrInvalidFileType, ext)
	}

	// 按用户隔离存储目录
	dir := filepath.Join(".run", "upload", spec.SubDir, strconv.FormatUint(uint64(userID), 10))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logger.Error("create upload directory failed",
			logger.String("kind", spec.Label),
			logger.String("dir", dir), logger.Err(err))
		return "", fmt.Errorf("create upload directory %s: %w", dir, err)
	}

	// 随机文件名，避免覆盖与路径猜测
	filename, err := RandHex(16)
	if err != nil {
		logger.Error("generate upload filename failed", logger.String("kind", spec.Label), logger.Err(err))
		return "", fmt.Errorf("generate upload filename: %w", err)
	}
	filename += ext
	absPath := filepath.Join(dir, filename)

	srcFile, err := fh.Open()
	if err != nil {
		logger.Error("open upload file failed", logger.String("kind", spec.Label), logger.Err(err))
		return "", fmt.Errorf("open upload file: %w", err)
	}
	defer srcFile.Close()

	// 读取文件头嗅探真实 MIME，并与后缀交叉校验，防止伪造后缀上传任意内容
	mimeType, body, err := detectUploadMIME(srcFile)
	if err != nil {
		logger.Error("detect upload content type failed", logger.String("kind", spec.Label), logger.Err(err))
		return "", fmt.Errorf("detect upload content type: %w", err)
	}
	if !mimeMatchesExt(ext, mimeType) {
		logger.Warn("upload content type does not match extension",
			logger.String("kind", spec.Label),
			logger.Uint("user_id", userID),
			logger.String("ext", ext),
			logger.String("mime", mimeType))
		return "", fmt.Errorf("%w: ext=%s content=%s", ErrInvalidFileType, ext, mimeType)
	}

	dstFile, err := os.Create(absPath)
	if err != nil {
		logger.Error("create upload target file failed", logger.String("path", absPath), logger.Err(err))
		return "", fmt.Errorf("create upload target file: %w", err)
	}

	// 拷贝流写入磁盘；失败时关闭并清理半截文件
	_, copyErr := io.Copy(dstFile, body)
	closeErr := dstFile.Close()
	if copyErr != nil {
		removePartialFile(absPath, spec.Label)
		return "", fmt.Errorf("copy upload content: %w", copyErr)
	}
	if closeErr != nil {
		removePartialFile(absPath, spec.Label)
		return "", fmt.Errorf("close upload target file: %w", closeErr)
	}

	// 返回相对路径，供 handler 层拼接成 http 可访问 URL
	return path.Join("/upload", spec.SubDir, strconv.FormatUint(uint64(userID), 10), filename), nil
}

// isAllowedExt 判断后缀是否在允许列表中。
func isAllowedExt(ext string, allowed []string) bool {
	for _, item := range allowed {
		if ext == item {
			return true
		}
	}
	return false
}

// removePartialFile 清理写入失败留下的半截文件，删除失败仅记录日志。
func removePartialFile(absPath, kind string) {
	if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
		logger.Error("remove partial upload file failed",
			logger.String("kind", kind),
			logger.String("path", absPath),
			logger.Err(err))
	}
}

// detectUploadMIME 读取文件头用于内容嗅探，并返回可继续读取完整内容的 Reader。
// 调用方应基于嗅探结果做类型判断，而不是只看文件扩展名。
func detectUploadMIME(src io.Reader) (string, io.Reader, error) {
	head := make([]byte, sniffLen)
	n, err := io.ReadFull(src, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", nil, fmt.Errorf("read upload header: %w", err)
	}
	head = head[:n]
	return http.DetectContentType(head), io.MultiReader(bytes.NewReader(head), src), nil
}

// mimeMatchesExt 校验真实内容类型与文件后缀是否匹配。
// 仅靠 http.DetectContentType 的前缀判断不足以阻止“改名绕过”，
// 这里按后缀限定允许的内容类型；.mkv 与 webm 共用 EBML 头，嗅探结果可能是 video/webm。
func mimeMatchesExt(ext, mimeType string) bool {
	switch ext {
	case ".jpg", ".jpeg":
		return mimeType == "image/jpeg"
	case ".png":
		return mimeType == "image/png"
	case ".webp":
		return mimeType == "image/webp"
	case ".mp4":
		return mimeType == "video/mp4"
	case ".mov":
		// Go 的 MIME 嗅探没有 quicktime 签名，部分 .mov 会被识别为 application/octet-stream
		return mimeType == "video/quicktime" || mimeType == "video/mp4" || mimeType == "application/octet-stream"
	case ".avi":
		return mimeType == "video/avi" || mimeType == "video/x-msvideo"
	case ".mkv":
		return mimeType == "video/webm" || mimeType == "video/x-matroska" || mimeType == "application/octet-stream"
	default:
		return false
	}
}
