package file

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"

	"feedsystem/internal/config"
	"feedsystem/internal/utils/logger"
)

// 上传目录结构常量。目录布局属于对外约定，集中在这里维护，避免各处硬编码。
const (
	// UploadDirName 用户上传资源总目录，生产环境由 Nginx 直接托管 /upload/
	UploadDirName = "upload"
	// StaticDirName 项目内置静态资源目录（默认头像、占位图），非用户上传
	StaticDirName = "static"
	// AvatarsSubDir 用户头像
	AvatarsSubDir = "avatars"
	// CoversSubDir 视频封面
	CoversSubDir = "covers"
	// VideosSubDir 分片合并后的成品视频
	VideosSubDir = "videos"
	// ChunksSubDir 分片上传临时碎片目录
	ChunksSubDir = "chunks"

	dirPermission  = 0o755 // 所有目录统一 0755
	filePermission = 0o644 // 所有资源文件统一 0644，禁止执行权限
)

// StorageRoot 返回持久化存储根目录（用户上传资产 + 内置静态资源）。
func StorageRoot() string {
	if config.AppConfig.StorageRoot == "" {
		return config.DefaultStorageRoot
	}
	return config.AppConfig.StorageRoot
}

// RuntimeRoot 返回运行时目录（日志、pid、一次性临时文件）。
func RuntimeRoot() string {
	if config.AppConfig.RuntimeRoot == "" {
		return config.DefaultRuntimeRoot
	}
	return config.AppConfig.RuntimeRoot
}

// UploadRoot 返回用户上传资源根目录。
func UploadRoot() string {
	return filepath.Join(StorageRoot(), UploadDirName)
}

// StaticRoot 返回内置静态资源根目录。
func StaticRoot() string {
	return filepath.Join(StorageRoot(), StaticDirName)
}

// UserUploadDir 返回某类上传资源下按用户隔离的目录：
// StorageRoot/upload/{subDir}/{userID}
func UserUploadDir(subDir string, userID uint) string {
	return filepath.Join(UploadRoot(), subDir, strconv.FormatUint(uint64(userID), 10))
}

// ChunksRoot 返回分片临时根目录。
func ChunksRoot() string {
	return filepath.Join(UploadRoot(), ChunksSubDir)
}

// ChunkUploadDir 返回单个上传会话的分片目录：
// StorageRoot/upload/chunks/{uploadID}
// 注意：chunks 下不再按 user_id 建子目录，便于直接删除整个过期会话目录。
func ChunkUploadDir(uploadID string) string {
	return filepath.Join(ChunksRoot(), uploadID)
}

// TempDir 返回一次性临时文件目录（文件头嗅探等），位于 RuntimeRoot 下。
// 分片碎片禁止写入这里，避免被系统临时目录清理策略误删。
func TempDir() string {
	return filepath.Join(RuntimeRoot(), "tmp")
}

// PublicUploadPath 拼接上传资源的 HTTP 访问路径，供前端/Nginx 使用。
// 例如 /upload/videos/1/xxx.mp4，与实际存储根目录无关。
func PublicUploadPath(subDir string, userID uint, filename string) string {
	return path.Join("/", UploadDirName, subDir, strconv.FormatUint(uint64(userID), 10), filename)
}

// EnsureStorageDirs 初始化存储与运行时目录。
// 必须在配置加载完成后调用；已存在的目录也会校正为 0755，避免权限漂移。
func EnsureStorageDirs() error {
	dirs := []string{
		StaticRoot(),
		UploadRoot(),
		filepath.Join(UploadRoot(), AvatarsSubDir),
		filepath.Join(UploadRoot(), CoversSubDir),
		filepath.Join(UploadRoot(), VideosSubDir),
		ChunksRoot(),
		filepath.Join(RuntimeRoot(), "logs"),
		TempDir(),
		filepath.Join(RuntimeRoot(), "pid"),
	}
	for _, dir := range dirs {
		if err := EnsureDir(dir); err != nil {
			return fmt.Errorf("ensure directory %s: %w", dir, err)
		}
	}
	return nil
}

// EnsureDir 创建目录并强制目录权限为 0755（MkdirAll 受 umask 影响，需再 Chmod 校正）。
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, dirPermission); err != nil {
		return err
	}
	return os.Chmod(dir, dirPermission)
}

// SetFilePermission 将资源文件权限统一校正为 0644，禁止任何执行权限。
func SetFilePermission(filename string) error {
	return os.Chmod(filename, filePermission)
}

// CleanupTempDir 清空运行时临时目录中的残留文件，仅清理文件，保留目录本身。
// 供启动阶段兜底：上一次进程异常退出可能留下半截临时文件。
func CleanupTempDir() {
	entries, err := os.ReadDir(TempDir())
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warn("read runtime temp dir failed", logger.Err(err), logger.String("dir", TempDir()))
		}
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		target := filepath.Join(TempDir(), entry.Name())
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			logger.Warn("remove stale temp file failed", logger.Err(err), logger.String("path", target))
		}
	}
}
