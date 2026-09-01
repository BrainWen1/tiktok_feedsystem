package service

import (
	"context"
	"feedsystem/internal/dto"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"feedsystem/internal/utils/file"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

type VideoService struct {
	VideoRepo *repo.VideoRepo
}

func NewVideoService(videoRepo *repo.VideoRepo) *VideoService {
	return &VideoService{VideoRepo: videoRepo}
}

// UploadVideo 处理视频上传逻辑
func (s *VideoService) UploadVideo(ctx context.Context, userID uint, fh *multipart.FileHeader) (string, error) {
	const maxSize = 100 << 20 // 100MB
	// 检查文件大小
	if fh.Size <= 0 || fh.Size > maxSize {
		log.Printf("User %d attempted to upload a file which is out of size bounds", userID)
		return "", fmt.Errorf("file size must be between 0 and 100MB")
	}

	// 检查文件扩展名是否合法
	ext := strings.ToLower(filepath.Ext(fh.Filename)) // 获取文件扩展名并转换为小写
	switch ext {
	case ".mp4", ".avi", ".mov", ".mkv":
	default:
		log.Printf("Invalid file type in UploadVideo: %v", ext)
		return "", fmt.Errorf("invalid file type")
	}

	// 创建视频存储目录
	dir := filepath.Join(".run", "upload", "videos", strconv.FormatUint(uint64(userID), 10)) // 拼接目录路径
	if err := os.MkdirAll(dir, 0o755); err != nil {                                          // 创建目录，如果目录已存在则不会报错
		log.Printf("Failed to create directory in UploadVideo: %v", err)
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	// 生成随机文件名
	filename, err := file.RandHex(16) // 生成16字节的随机十六进制字符串作为文件名
	if err != nil {
		log.Printf("Failed to generate random filename in UploadVideo: %v", err)
		return "", fmt.Errorf("failed to generate random filename: %w", err)
	}
	filename = filename + ext // 拼接完整文件名

	// 拼接完整文件路径
	absPath := filepath.Join(dir, filename)

	// 保存上传的文件到指定路径，由于service层不能绑定gin.Context，所以这里使用io.Copy来保存文件
	// 打开上传的文件
	srcFile, err := fh.Open()
	if err != nil {
		return "", fmt.Errorf("open upload file failed: %w", err)
	}
	defer srcFile.Close() // 注册延迟关闭文件句柄

	// 创建本地目标文件
	dstFile, err := os.Create(absPath)
	if err != nil {
		return "", fmt.Errorf("create target file failed: %w", err)
	}
	defer dstFile.Close()

	// 拷贝流写入磁盘
	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return "", fmt.Errorf("copy file content failed: %w", err)
	}

	// 返回相对路径，供handler层拼接成http可访问URL
	urlPath := path.Join("/upload", "videos", strconv.FormatUint(uint64(userID), 10), filename)

	return urlPath, nil
}

// UploadCover 处理视频封面上传逻辑
func (s *VideoService) UploadCover(ctx context.Context, userID uint, fh *multipart.FileHeader) (string, error) {
	const maxSize = 10 << 20 // 最大文件大小为10MB
	if fh.Size <= 0 || fh.Size > maxSize {
		log.Printf("File size is invalid in UploadCover: %v", fh.Size)
		return "", fmt.Errorf("invalid file size")
	}

	// 检查文件扩展名是否合法
	ext := strings.ToLower(filepath.Ext(fh.Filename)) // 获取文件扩展名并转换为小写
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
	default:
		log.Printf("Invalid file type in UploadCover: %v", ext)
		return "", fmt.Errorf("invalid file type")
	}

	// 创建用户头像存储目录
	dir := filepath.Join(".run", "upload", "covers", strconv.FormatUint(uint64(userID), 10)) // 拼接目录路径
	if err := os.MkdirAll(dir, 0o755); err != nil {                                          // 创建目录，如果目录已存在则不会报错
		log.Printf("Failed to create directory in UploadCover: %v", err)
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	// 生成随机文件名
	filename, err := file.RandHex(16) // 生成16字节的随机十六进制字符串作为文件名
	if err != nil {
		log.Printf("Failed to generate random filename in UploadCover: %v", err)
		return "", fmt.Errorf("failed to generate random filename: %w", err)
	}
	filename = filename + ext // 拼接完整文件名

	// 拼接完整文件路径
	absPath := filepath.Join(dir, filename)

	// 保存上传的文件到指定路径，由于service层不能绑定gin.Context，所以这里使用io.Copy来保存文件
	// 打开上传的文件
	srcFile, err := fh.Open()
	if err != nil {
		return "", fmt.Errorf("open upload file failed: %w", err)
	}
	defer srcFile.Close() // 注册延迟关闭文件句柄

	// 创建本地目标文件
	dstFile, err := os.Create(absPath)
	if err != nil {
		return "", fmt.Errorf("create target file failed: %w", err)
	}
	defer dstFile.Close()

	// 拷贝流写入磁盘
	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return "", fmt.Errorf("copy file content failed: %w", err)
	}

	// 返回相对路径，供handler层拼接成http可访问URL
	urlPath := path.Join("/upload", "covers", strconv.FormatUint(uint64(userID), 10), filename)

	return urlPath, nil
}

// PublishVideo 处理视频发布逻辑
func (s *VideoService) PublishVideo(ctx context.Context, userID uint, title, description, videoURL, coverURL string) (*dto.PublishVideoResponse, error) {
	// 验证输入参数
	if title == "" || videoURL == "" || coverURL == "" {
		log.Printf("Invalid input parameters in PublishVideo: title=%v, videoURL=%v, coverURL=%v", title, videoURL, coverURL)
		return nil, fmt.Errorf("title, video URL, and cover URL cannot be empty")
	}

	if !strings.HasPrefix(videoURL, "/upload/videos/") || !strings.HasPrefix(coverURL, "/upload/covers/") {
		log.Printf("Invalid URL format in PublishVideo: videoURL=%v, coverURL=%v", videoURL, coverURL)
		return nil, fmt.Errorf("invalid video or cover URL format")
	}

	// 创建视频模型实例
	video := &model.Video{
		AuthorID:    userID,
		Title:       title,
		Description: description,
		VideoURL:    videoURL,
		CoverURL:    coverURL,
	}

	// 保存视频信息到数据库
	if err := s.VideoRepo.Create(ctx, video); err != nil {
		log.Printf("Failed to publish video in PublishVideo: %v", err)
		return nil, fmt.Errorf("failed to publish video: %w", err)
	}

	return &dto.PublishVideoResponse{VideoID: video.ID}, nil
}

// VideoDetail 获取单条视频详情，软鉴权
func (s *VideoService) VideoDetail(ctx context.Context, videoID uint, uid uint) (*dto.VideoDetailResponse, error) {
	// repo查询视频+联表拿到作者基础信息
	video, author, err := s.VideoRepo.FindWithAuthor(ctx, videoID)
	if err != nil {
		return nil, err
	}
	// 组装基础返回数据
	resp := &dto.VideoDetailResponse{
		ID:          video.ID,
		Title:       video.Title,
		Description: video.Description,
		VideoURL:    video.VideoURL,
		CoverURL:    video.CoverURL,
		LikesCount:  video.LikesCount,
		AuthorInfo: dto.AuthorBrief{
			UserId:    author.ID,
			UserName:  author.UserName,
			AvatarURL: author.AvatarURL,
		},
		IsLiked: false, // 默认false，游客到此结束
	}

	// 只有登录用户才查询点赞状态，暂时没做like模块，先假设所有用户都点赞了
	if uid != 0 {
		resp.IsLiked = true
	}

	return resp, nil
}
