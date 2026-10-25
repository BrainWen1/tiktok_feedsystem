package service

import (
	"context"
	"encoding/json"
	"errors"
	"feedsystem/internal/dto"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"feedsystem/internal/utils/file"
	"feedsystem/internal/utils/logger"
	"fmt"
	"mime/multipart"
	"strconv"
	"strings"
	"time"
)

// 折中的耦合方案，后期解耦把部分用户信息写到video表里，配合mq做异步更新
type VideoService struct {
	VideoRepo   *repo.VideoRepo
	UserService *UserService
	cache       *cache.RedisCache
	LikeService *LikeService
}

func NewVideoService(videoRepo *repo.VideoRepo, userService *UserService, cache *cache.RedisCache, likeService *LikeService) *VideoService {
	return &VideoService{VideoRepo: videoRepo, UserService: userService, cache: cache, LikeService: likeService}
}

// UploadVideo 处理视频上传逻辑
func (s *VideoService) UploadVideo(ctx context.Context, userID uint, fh *multipart.FileHeader) (string, error) {
	// 统一复用 utils/file 中的大小、后缀与真实 MIME 校验及落盘逻辑
	return file.SaveUpload(fh, userID, file.VideoUploadSpec)
}

// UploadCover 处理视频封面上传逻辑
func (s *VideoService) UploadCover(ctx context.Context, userID uint, fh *multipart.FileHeader) (string, error) {
	// 统一复用 utils/file 中的大小、后缀与真实 MIME 校验及落盘逻辑
	return file.SaveUpload(fh, userID, file.CoverUploadSpec)
}

const (
	maxVideoTags = 10 // 单条视频最多关联的标签数
	maxTagLength = 64 // 单个标签最大字符数，与 video_tag.tag 字段长度一致
)

// normalizeVideoTags 清洗标签：去空白、去重、截断超长标签、限制标签总数。
// 去重和限流属于业务规则，放在 service 层；repo 只负责写入。
func normalizeVideoTags(tags []string) []string {
	result := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))

	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if runes := []rune(tag); len(runes) > maxTagLength {
			tag = string(runes[:maxTagLength])
		}
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
		if len(result) == maxVideoTags {
			break
		}
	}
	return result
}

// PublishVideo 处理视频发布逻辑
func (s *VideoService) PublishVideo(ctx context.Context, userID uint, title, description, videoURL, coverURL string, tags []string) (*dto.PublishVideoResponse, error) {
	// 验证输入参数
	if title == "" || videoURL == "" || coverURL == "" {
		logger.Warn("invalid publish video parameters", logger.String("title", title), logger.String("video_url", videoURL), logger.String("cover_url", coverURL))
		return nil, fmt.Errorf("title, video URL, and cover URL cannot be empty")
	}

	if !strings.HasPrefix(videoURL, "/upload/videos/") || !strings.HasPrefix(coverURL, "/upload/covers/") {
		logger.Warn("invalid publish video URL format", logger.String("video_url", videoURL), logger.String("cover_url", coverURL))
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

	// 保存视频信息及标签关联到数据库。标签写入与视频创建在同一事务内，
	// 避免视频发布成功但标签丢失，导致标签流查不到该视频。
	if err := s.VideoRepo.CreateWithTags(ctx, video, normalizeVideoTags(tags)); err != nil {
		logger.Error("publish video failed", logger.Err(err))
		return nil, fmt.Errorf("failed to publish video: %w", err)
	}

	return &dto.PublishVideoResponse{VideoID: video.ID}, nil
}

// VideoDetail 获取单条视频详情，软鉴权
func (s *VideoService) VideoDetail(ctx context.Context, videoID uint, userID uint) (*dto.VideoDetailResponse, error) {
	// 调用带Redis缓存+分布式锁的底层方法，拿到公共视频数据
	video, err := s.getDetail(ctx, videoID)
	if err != nil {
		return nil, fmt.Errorf("get video %d detail: %w", videoID, err)
	}
	// 通过视频里的AuthorID查询作者信息
	author, err := s.UserService.FindByID(ctx, video.AuthorID)
	if err != nil {
		return nil, fmt.Errorf("get author %d for video %d: %w", video.AuthorID, videoID, err)
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

	// 登录用户才判断点赞状态，userID!=0代表携带有效token
	if userID != 0 {
		// 查询用户是否点赞过该视频
		// 先查redis缓存，缓存未命中再查数据库
		// 与 worker 消费端共用同一套 key 构造函数，结构固定为 Set（P0-8/P0-9）
		key := cache.LikeUserSetKey(userID)
		// 用户在空占位缓存未过期的时间里再次点赞的话，worker会在点赞集合里写入这个vid，再次查询会直接在这里命中缓存，不会被空占位误判
		isLiked, err := s.cache.IsMemberOfSet(ctx, key, videoID)
		if err != nil {
			logger.Error("check like status in redis failed", logger.Uint("user_id", userID), logger.Uint("video_id", videoID), logger.Err(err))
			// Redis异常，兜底查数据库
			isLikedResp, dbErr := s.LikeService.IsLiked(ctx, userID, videoID)
			isLiked = isLikedResp.IsLiked
			if dbErr != nil {
				logger.Error("check like status in DB failed", logger.Uint("user_id", userID), logger.Uint("video_id", videoID), logger.Err(dbErr))
				return nil, fmt.Errorf("failed to check like status: %w", dbErr)
			}
		} else if !isLiked {
			// 先检查空缓存占位，避免大量缓存穿透
			placeholderKey := cache.LikePlaceholderKey(userID, videoID)
			exists, err := s.cache.Exists(ctx, placeholderKey)

			if err == nil && exists {
				// Redis中存在空占位，说明用户未点赞过该视频，直接返回false
				isLiked = false
			} else {
				// 要么是Redis异常，要么是缓存未命中，兜底查数据库
				isLikedResp, dbErr := s.LikeService.IsLiked(ctx, userID, videoID)
				if dbErr != nil {
					logger.Error("check like status in DB failed", logger.Uint("user_id", userID), logger.Uint("video_id", videoID), logger.Err(dbErr))
					return nil, fmt.Errorf("failed to check like status: %w", dbErr)
				}
				isLiked = isLikedResp.IsLiked

				// 如果redis正常才尝试写回redis
				if err == nil {
					logger.Debug("write like status back to redis", logger.Uint("user_id", userID), logger.Uint("video_id", videoID))
					if isLikedResp.IsLiked {
						// 已点赞，写入点赞集合，TTL 兜底过期，避免脏 member 永久驻留
						err = s.cache.AddToSet(ctx, key, videoID, cache.LikeUserSetTTL)
						if err != nil {
							logger.Error("add video to like set failed", logger.Uint("video_id", videoID), logger.Uint("user_id", userID), logger.Err(err))
						}
					} else {
						// 未点赞，写入空占位
						err = s.cache.Set(ctx, placeholderKey, "1", cache.LikePlaceholderTTL)
						if err != nil {
							logger.Error("set like placeholder failed", logger.String("key", placeholderKey), logger.Err(err))
						}
					}
				}
			}
		}
		// Redis缓存命中，直接使用
		logger.Debug("like status resolved", logger.Bool("is_liked", isLiked))
		resp.IsLiked = isLiked
	}
	return resp, nil
}

// getCached 封装读取并反序列化缓存的逻辑
func (s *VideoService) getCached(ctx context.Context, cacheKey string) (*model.Video, bool) {
	// 设置一个短超时，避免Redis阻塞过久
	opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	// 尝试从Redis获取缓存；未命中属于正常路径，其余错误记录告警后回源数据库
	val, err := s.cache.Get(opCtx, cacheKey)
	if err != nil {
		if !errors.Is(err, cache.ErrCacheMiss) {
			logger.Warn("get video cache failed", logger.String("key", cacheKey), logger.Err(err))
		}
		return nil, false
	}
	// 反序列化缓存数据
	b := []byte(val)
	var cached model.Video
	if err := json.Unmarshal(b, &cached); err != nil {
		logger.Error("unmarshal cached video failed", logger.Err(err))
		return nil, false
	}
	return &cached, true
}

// setCached 将视频结构体序列化写入redis缓存
func (s *VideoService) setCached(ctx context.Context, cacheKey string, video *model.Video) error {
	// 序列化视频结构体
	b, err := json.Marshal(video)
	if err != nil {
		logger.Error("marshal video for cache failed", logger.Err(err))
		return fmt.Errorf("marshal video for cache: %w", err)
	}
	opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	// 写入Redis缓存，设置过期时间为10分钟
	err = s.cache.Set(opCtx, cacheKey, b, 10*time.Minute)
	if err != nil {
		logger.Error("set video cache failed", logger.Err(err))
		return fmt.Errorf("set video cache key %s: %w", cacheKey, err)
	}
	return nil
}

// getDetail 底层方法：仅获取视频基础公共元数据，带Redis缓存+分布式锁防击穿
func (s *VideoService) getDetail(ctx context.Context, vid uint) (*model.Video, error) {
	cacheKey := fmt.Sprintf("video_detail:%d", vid) // Redis缓存key

	// 缓存禁用时跳过缓存与分布式锁，直接走数据库
	if s.cache.IsEnabled() {
		//第一次读取缓存
		if v, ok := s.getCached(ctx, cacheKey); ok {
			logger.Debug("video cache hit", logger.Uint("video_id", vid))
			return v, nil
		}
		logger.Debug("video cache miss, acquiring lock", logger.Uint("video_id", vid))
		//缓存未命中，加分布式锁防止缓存击穿
		lockKey := fmt.Sprintf("lock:%d", vid)
		lockCtx, lockCancel := context.WithTimeout(ctx, 50*time.Millisecond)
		token, locked, lockErr := s.cache.Lock(lockCtx, lockKey, 2*time.Second)
		lockCancel()

		if lockErr == nil && locked {
			// 拿到锁，查询数据库并更新缓存
			logger.Debug("video cache lock acquired, querying database", logger.Uint("video_id", vid))
			defer func() { // 注册延迟释放锁
				// 使用带短超时的独立 ctx，避免 Redis 抖动时 Unlock 长时间阻塞请求返回
				unlockCtx, unlockCancel := context.WithTimeout(context.WithoutCancel(ctx), 200*time.Millisecond)
				defer unlockCancel()
				if err := s.cache.Unlock(unlockCtx, lockKey, token); err != nil {
					logger.Error("unlock failed", logger.String("key", lockKey), logger.Err(err))
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
				return nil, fmt.Errorf("query video %d: %w", vid, err)
			}
			logger.Debug("video fetched from database, updating cache", logger.Uint("video_id", vid))
			// 将查询到的视频数据写入缓存；写缓存失败只告警，不能影响本次数据库读取结果
			if err := s.setCached(ctx, cacheKey, video); err != nil {
				logger.Warn("set video cache failed", logger.String("key", cacheKey), logger.Uint("video_id", vid), logger.Err(err))
			}
			logger.Debug("video cache miss, fetched from DB", logger.Uint("video_id", vid))
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
	logger.Warn("cache wait timeout, fallback to DB", logger.Uint("video_id", vid))
	video, err := s.VideoRepo.FindByID(ctx, vid)
	if err != nil {
		return nil, fmt.Errorf("query video %d: %w", vid, err)
	}
	if s.cache.IsEnabled() {
		// 缓存只是加速层，写入失败保留数据库读取结果，仅记录告警
		if err := s.setCached(ctx, cacheKey, video); err != nil {
			logger.Warn("set video cache failed", logger.String("key", cacheKey), logger.Uint("video_id", vid), logger.Err(err))
		}
	}
	return video, nil
}

// VideoList 获取视频列表，使用 (created_at, id) 复合游标分页。
func (s *VideoService) VideoList(ctx context.Context, authorID uint, userID uint, cursor dto.FeedCursor, pageSize int) (*dto.VideoListResponse, error) {
	// 参数保护，限制最大条数，防止恶意拉取大量数据
	if pageSize <= 0 || pageSize > 20 {
		pageSize = 20
	}

	lastCreatedAt, lastVid, _, err := parseTimeFeedCursor(cursor)
	if err != nil {
		return nil, err
	}

	// 查询视频列表
	videos, total, err := s.VideoRepo.VideoList(ctx, authorID, lastCreatedAt, lastVid, pageSize)
	if err != nil {
		return nil, fmt.Errorf("list videos for author %d: %w", authorID, err)
	}
	if len(videos) == 0 {
		return &dto.VideoListResponse{Total: total, Videos: []dto.VideoDetailResponse{}}, nil
	}
	// 只查询一次作者信息，避免N+1
	author, err := s.UserService.FindByID(ctx, authorID)
	if err != nil {
		return nil, fmt.Errorf("get author %d for video list: %w", authorID, err)
	}
	authorDTO := dto.AuthorBrief{
		UserID:    author.ID,
		UserName:  author.Username,
		AvatarURL: author.AvatarURL,
	}

	// 收集所有vid，如果用户已登录，批量查询点赞
	var vidList []interface{}
	for _, v := range videos {
		vidList = append(vidList, v.ID)
	}

	// 登录用户才判断点赞状态，userID!=0代表携带有效token
	likedMap := make(map[uint]bool) // <vid, isLiked>
	if userID != 0 {
		// 查询用户是否点赞过这些视频
		// 与 worker 消费端共用同一套 key 构造函数，结构固定为 Set（P0-8/P0-9）
		key := cache.LikeUserSetKey(userID)
		// 批量查询
		isLikedList, err := s.cache.MIsMemberOfSet(ctx, key, vidList)
		if err != nil {
			logger.Error("check like status in redis failed", logger.Uint("user_id", userID), logger.Err(err))
			// Redis异常，兜底查数据库
			for _, vid := range vidList {
				isLikedResp, dbErr := s.LikeService.IsLiked(ctx, userID, vid.(uint))
				if dbErr != nil {
					logger.Error("check like status in DB failed", logger.Uint("user_id", userID), logger.Any("video_id", vid), logger.Err(dbErr))
					return nil, fmt.Errorf("failed to check like status: %w", dbErr)
				}
				likedMap[vid.(uint)] = isLikedResp.IsLiked
			}
		} else {
			logger.Debug("redis like status batch query succeeded", logger.Uint("user_id", userID))
			for i, vid := range vidList {
				isLiked := isLikedList[i]
				if !isLiked {
					// 点赞集合的缓存未命中
					logger.Debug("like status cache miss", logger.Uint("user_id", userID), logger.Any("video_id", vid))
					// 检查空占位缓存，避免缓存穿透
					placeholderKey := cache.LikePlaceholderKey(userID, vid.(uint))
					exists, err := s.cache.Exists(ctx, placeholderKey)
					if err == nil && exists {
						likedMap[vid.(uint)] = false
						continue // 空占位缓存命中，说明用户未点赞过该视频，直接返回false
					}

					// 空占位缓存也未命中，兜底查数据库
					isLikedResp, dbErr := s.LikeService.IsLiked(ctx, userID, vid.(uint))
					if dbErr != nil {
						logger.Error("check like status in DB failed", logger.Uint("user_id", userID), logger.Any("video_id", vid), logger.Err(dbErr))
						return nil, fmt.Errorf("failed to check like status: %w", dbErr)
					}
					likedMap[vid.(uint)] = isLikedResp.IsLiked

					// 将数据库查询结果写入Redis缓存，避免下次重复查询
					if isLikedResp.IsLiked {
						// 已点赞，写入点赞集合缓存，TTL 兜底过期，避免脏 member 永久驻留
						logger.Debug("write like status back to redis", logger.Uint("user_id", userID), logger.Any("video_id", vid))
						err = s.cache.AddToSet(ctx, key, vid.(uint), cache.LikeUserSetTTL)
						if err != nil {
							logger.Error("add video to like set failed", logger.Any("video_id", vid), logger.Uint("user_id", userID), logger.Err(err))
						}
					} else {
						// 未点赞，写入空占位缓存
						err = s.cache.Set(ctx, placeholderKey, "1", cache.LikePlaceholderTTL)
						if err != nil {
							logger.Error("set like placeholder failed", logger.String("key", placeholderKey), logger.Err(err))
						}
					}
				} else {
					// Redis缓存命中，直接使用
					likedMap[vid.(uint)] = true
					logger.Debug("like status cache hit", logger.Uint("user_id", userID), logger.Any("video_id", vid))
				}
			}
		}
	}

	// 循环组装DTO，所有查询已经完成，这里只有内存操作，没有DB访问
	var itemList []dto.VideoDetailResponse
	for _, v := range videos {
		item := dto.VideoDetailResponse{
			ID:          v.ID,
			Title:       v.Title,
			Description: v.Description,
			VideoURL:    v.VideoURL,
			CoverURL:    v.CoverURL,
			LikesCount:  v.LikesCount,
			IsLiked:     likedMap[v.ID],
			AuthorInfo:  authorDTO,
		}
		itemList = append(itemList, item)
	}
	resp := &dto.VideoListResponse{
		Total:  total,
		Videos: itemList,
	}
	// 只有取满一页才返回下一页游标，避免客户端多发一次空请求
	if len(videos) == pageSize {
		last := videos[len(videos)-1]
		resp.NextCursor = &dto.FeedCursor{
			LastVid:       strconv.FormatUint(uint64(last.ID), 10),
			LastCreatedAt: last.CreatedAt,
		}
	}
	return resp, nil
}
