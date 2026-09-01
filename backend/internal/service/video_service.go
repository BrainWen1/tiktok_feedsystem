package service

import (
	"context"
	"encoding/json"
	"feedsystem/internal/dto"
	"feedsystem/internal/infra/cache"
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
	"time"
)

// 折中的耦合方案，后期解耦把部分用户信息写到video表里，配合mq做异步更新
type VideoService struct {
	VideoRepo   *repo.VideoRepo
	UserService *UserService
	cache       *cache.RedisCache
}

func NewVideoService(videoRepo *repo.VideoRepo, userService *UserService, cache *cache.RedisCache) *VideoService {
	return &VideoService{VideoRepo: videoRepo, UserService: userService, cache: cache}
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
	// 调用带Redis缓存+分布式锁的底层方法，拿到公共视频数据
	video, err := s.getDetail(ctx, videoID)
	if err != nil {
		return nil, err
	}
	// 通过视频里的AuthorID查询作者信息
	author, err := s.UserService.FindByID(ctx, video.AuthorID)
	if err != nil {
		return nil, err
	}

	// 组装返回DTO
	resp := &dto.VideoDetailResponse{
		ID:          video.ID,
		Title:       video.Title,
		Description: video.Description,
		VideoURL:    video.VideoURL,
		CoverURL:    video.CoverURL,
		LikesCount:  video.LikesCount,
		AuthorInfo: dto.AuthorBrief{
			UserID:    author.ID,
			UserName:  author.Username,
			AvatarURL: author.AvatarURL,
		},
		IsLiked: false, //游客默认false
	}

	// 登录用户才判断点赞状态，uid!=0代表携带有效token
	if uid != 0 {
		//TODO：后续接入点赞repo，去数据库/redis查询当前用户是否点赞这条视频
		resp.IsLiked = true
	}
	return resp, nil
}

// getCached 封装读取并反序列化缓存的逻辑
func (s *VideoService) getCached(ctx context.Context, cacheKey string) (*model.Video, bool) {
	// 设置一个短超时，避免Redis阻塞过久
	opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	// 尝试从Redis获取缓存
	val, err := s.cache.Get(opCtx, cacheKey)
	if err != nil {
		return nil, false
	}
	// 反序列化缓存数据
	b := []byte(val)
	var cached model.Video
	if err := json.Unmarshal(b, &cached); err != nil {
		log.Printf("Failed to unmarshal cached video data: %v", err)
		return nil, false
	}
	return &cached, true
}

// setCached 将视频结构体序列化写入redis缓存
func (s *VideoService) setCached(ctx context.Context, cacheKey string, video *model.Video) error {
	// 序列化视频结构体
	b, err := json.Marshal(video)
	if err != nil {
		log.Printf("Failed to marshal video for caching: %v", err)
		return err
	}
	opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	// 写入Redis缓存，设置过期时间为10分钟
	err = s.cache.Set(opCtx, cacheKey, b, 10*time.Minute)
	if err != nil {
		log.Printf("Failed to set video cache in Redis: %v", err)
		return err
	}
	return nil
}

// getDetail 底层方法：仅获取视频基础公共元数据，带Redis缓存+分布式锁防击穿
func (s *VideoService) getDetail(ctx context.Context, vid uint) (*model.Video, error) {
	cacheKey := fmt.Sprintf("video_detail:%d", vid) // Redis缓存key

	if s.cache != nil {
		//第一次读取缓存
		if v, ok := s.getCached(ctx, cacheKey); ok {
			log.Printf("Cache hit for video %d", vid) // [日志] 缓存命中
			return v, nil
		}
		log.Printf("Cache miss for video %d, attempting to acquire lock", vid) // [日志] 缓存未命中
		//缓存未命中，加分布式锁防止缓存击穿
		lockKey := fmt.Sprintf("lock:%d", vid)
		lockCtx, lockCancel := context.WithTimeout(ctx, 50*time.Millisecond)
		token, locked, lockErr := s.cache.Lock(lockCtx, lockKey, 2*time.Second)
		lockCancel()

		if lockErr == nil && locked {
			// 拿到锁，查询数据库并更新缓存
			log.Printf("Acquired lock for video %d, querying database", vid) // [日志] 拿到锁
			defer func() {                                                   // 注册延迟释放锁
				err := s.cache.Unlock(context.Background(), lockKey, token)
				if err != nil {
					log.Printf("unlock failed key=%s err=%v", lockKey, err)
				}
			}()
			//拿到锁后二次校验缓存
			if v, ok := s.getCached(ctx, cacheKey); ok {
				// 成功拿到缓存，直接返回
				return v, nil
			}
			// 缓存仍然未命中，查询数据库
			video, err := s.VideoRepo.FindByID(ctx, vid)
			if err != nil {
				return nil, err
			}
			log.Printf("Fetched video %d from database, updating cache", vid) // [日志] 查询数据库
			// 将查询到的视频数据写入缓存
			err = s.setCached(ctx, cacheKey, video)
			if err != nil {
				return nil, err
			}
			log.Printf("Cache miss for video %d, fetched from DB and cached", vid) // [日志] 设置缓存
			return video, nil
		}
		//抢锁失败，轮询等待缓存构建完成
		for i := 0; i < 5; i++ {
			select {
			case <-ctx.Done(): // 上下文超时或取消
				return nil, ctx.Err()
			case <-time.After(20 * time.Millisecond): // 短暂等待后重试
			}
			// 尝试再次读取缓存
			if v, ok := s.getCached(ctx, cacheKey); ok {
				return v, nil
			}
		}
	}
	//兜底：缓存不可用/重试失败直接查库
	log.Printf("warn: cache wait timeout, fallback to db for video %d", vid)
	video, err := s.VideoRepo.FindByID(ctx, vid)
	if err != nil {
		return nil, err
	}
	if s.cache != nil {
		err = s.setCached(ctx, cacheKey, video)
		if err != nil {
			return nil, err
		}
	}
	return video, nil
}
