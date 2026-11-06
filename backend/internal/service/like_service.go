package service

import (
	"context"
	"errors"
	"feedsystem/internal/dto"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/infra/mq"
	"feedsystem/internal/repo"
	"feedsystem/internal/utils/logger"
	"fmt"
	"strconv"
	"time"
)

// 点赞视频列表分页参数。
const (
	defaultLikedVideosPageSize = 20
	maxLikedVideosPageSize     = 30
)

// VideoDetailer 只暴露 LikeService 需要的最小视频详情能力。
// 这里不要直接依赖完整的 VideoService，避免和 VideoService 形成初始化循环。
type VideoDetailer interface {
	VideoDetail(ctx context.Context, videoID uint, userID uint) (*dto.VideoDetailResponse, error)
}

type LikeService struct {
	likeRepo            *repo.LikeRepo
	likeMQ              *mq.LikeMQ
	UserService         *UserService         // 引入UserService以便查询作者信息
	notificationService *NotificationService // 引入NotificationService以便处理通知逻辑
	videoDetailer       VideoDetailer
	cache               *cache.RedisCache
}

func NewLikeService(likeRepo *repo.LikeRepo, likeMQ *mq.LikeMQ, userService *UserService, notificationService *NotificationService, cache *cache.RedisCache) *LikeService {
	return &LikeService{likeRepo: likeRepo, likeMQ: likeMQ, UserService: userService, notificationService: notificationService, videoDetailer: nil, cache: cache}
}

// SetVideoDetailer 在 VideoService 创建完成后注入。
// 这一步把双向依赖拆成“先创建 LikeService，再把 VideoService 作为接口塞回去”。
func (s *LikeService) SetVideoDetailer(videoDetailer VideoDetailer) {
	s.videoDetailer = videoDetailer
}

// LikeVideo 点赞视频
func (s *LikeService) LikeVideo(ctx context.Context, userID, vid uint) error {
	if userID == 0 || vid == 0 {
		logger.Ctx(ctx).Warn("invalid userID or vid", logger.Uint("user_id", userID), logger.Uint("video_id", vid))
		return errors.New("uid and vid must be non-zero")
	}
	// 前置查询：拦截重复点击，减少无效MQ消息
	isLiked, err := s.likeRepo.IsLiked(ctx, userID, vid)
	if err != nil {
		return fmt.Errorf("check like status for user %d video %d: %w", userID, vid, err)
	}
	if isLiked {
		return errors.New("video already liked by user")
	}
	// 检查视频是否存在
	_, err = s.videoDetailer.VideoDetail(ctx, vid, userID)
	if err != nil {
		return fmt.Errorf("get video detail for user %d video %d: %w", userID, vid, err)
	}

	//组装消息
	err = s.likeMQ.Like(ctx, userID, vid)
	if err != nil {
		logger.Ctx(ctx).Error("publish like event to MQ failed", logger.Err(err))
		// MQ投递失败，降级方案：同步直接写数据库
		if createErr := s.likeRepo.CreateLike(ctx, userID, vid); createErr != nil {
			return fmt.Errorf("create like fallback for user %d video %d: %w", userID, vid, createErr)
		}
		// 同步写数据库成功后，异步处理通知逻辑
		go func() {
			nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()

			// err 使用 := 创建局部变量，避免覆盖外层的 err，防止以后可能发生的竞态
			err := s.notificationService.ProcessNotification(nctx, mq.NotificationEvent{
				// 目前 eventID 先传空串，后续如果在函数内部加入重试机制，则需要使用 eventID 做幂等
				// 这里不使用 uuid.NewString()，因为在函数内部也不会使用，会产生垃圾
				EventID:  "",
				Action:   "like",
				ActorID:  userID,
				TargetID: vid,
			})
			if err != nil {
				logger.Ctx(nctx).Error("process notification failed", logger.Err(err))
			}
		}()
		return nil
	}

	// MQ投递成功，返回nil，后台慢慢消费入库
	return nil
}

// UnlikeVideo 取消点赞视频
func (s *LikeService) UnlikeVideo(ctx context.Context, userID, vid uint) error {
	if userID == 0 || vid == 0 {
		logger.Ctx(ctx).Warn("invalid userID or vid", logger.Uint("user_id", userID), logger.Uint("video_id", vid))
		return errors.New("uid and vid must be non-zero")
	}
	// 前置查询：拦截重复点击，减少无效MQ消息
	isLiked, err := s.likeRepo.IsLiked(ctx, userID, vid)
	if err != nil {
		return fmt.Errorf("check like status for user %d video %d: %w", userID, vid, err)
	}
	if !isLiked {
		return errors.New("video not liked by user")
	}

	//组装消息
	err = s.likeMQ.Unlike(ctx, userID, vid)
	if err != nil {
		logger.Ctx(ctx).Error("publish unlike event to MQ failed", logger.Err(err))
		// MQ投递失败，降级方案：同步直接写数据库
		if deleteErr := s.likeRepo.DeleteLike(ctx, userID, vid); deleteErr != nil {
			return fmt.Errorf("delete like fallback for user %d video %d: %w", userID, vid, deleteErr)
		}
		return nil
	}

	// MQ投递成功，返回nil，后台慢慢消费入库
	return nil
}

// IsLiked 检查用户是否点赞了视频
func (s *LikeService) IsLiked(ctx context.Context, userID, vid uint) (dto.IsLikedResponse, error) {
	isLiked, err := s.likeRepo.IsLiked(ctx, userID, vid)
	if err != nil {
		return dto.IsLikedResponse{}, fmt.Errorf("check like status for user %d video %d: %w", userID, vid, err)
	}
	return dto.IsLikedResponse{IsLiked: isLiked}, nil
}

// ListLikedVideos 列出用户点赞过的视频，使用 (created_at, video_id) 复合游标分页。
func (s *LikeService) ListLikedVideos(ctx context.Context, targetUserID uint, visitorUserID uint, cursor dto.FeedCursor, pageSize uint) (*dto.VideoListResponse, error) {
	if s.videoDetailer == nil {
		return nil, errors.New("video detail service is not initialized")
	}

	if pageSize == 0 || pageSize > maxLikedVideosPageSize {
		pageSize = defaultLikedVideosPageSize
	}

	lastVid, hasCursor, err := parseFeedCursorVid(cursor.LastVid)
	if err != nil {
		return nil, err
	}
	var lastCreatedAt time.Time
	if hasCursor {
		if cursor.LastCreatedAt.IsZero() {
			return nil, errors.New("incomplete like cursor: last_created_at is required when last_vid is set")
		}
		lastCreatedAt = cursor.LastCreatedAt
	}

	// 根据 targetUserID 查询用户点赞过的记录（含点赞时间，用于生成下一页游标）
	likes, total, err := s.likeRepo.FindLikedVideoPage(ctx, targetUserID, lastCreatedAt, lastVid, int(pageSize))
	if err != nil {
		return nil, fmt.Errorf("find liked videos for user %d: %w", targetUserID, err)
	}

	// 根据vids查询视频详情，并组装返回结果
	resList := make([]dto.VideoDetailResponse, 0, len(likes)) // 预分配容量，避免多次扩容
	for _, like := range likes {
		videoDetail, err := s.videoDetailer.VideoDetail(ctx, like.VideoID, visitorUserID)
		if err != nil {
			logger.Ctx(ctx).Error("get video detail failed", logger.Uint("video_id", like.VideoID), logger.Err(err))
			continue
		}
		// dto.VideoListResponse 里保存的是值类型，所以这里要解引用后再放入切片。
		resList = append(resList, *videoDetail)
	}

	resp := &dto.VideoListResponse{
		Videos: resList,
		Total:  total,
	}
	// 取满一页才返回下一页游标，游标使用最后一条点赞记录的时间 + video_id
	if len(likes) == int(pageSize) {
		last := likes[len(likes)-1]
		resp.NextCursor = &dto.FeedCursor{
			LastVid:       strconv.FormatUint(uint64(last.VideoID), 10),
			LastCreatedAt: last.CreatedAt,
		}
	}
	return resp, nil
}

// BatchGetIsLike 批量查询：loginUserID 是否点赞 vidList 里的视频
// 返回 map[vid]bool
func (s *LikeService) BatchGetIsLike(ctx context.Context, loginUserID uint, vidList []string) (map[string]bool, error) {
	resMap := make(map[string]bool)
	if loginUserID == 0 || len(vidList) == 0 {
		return resMap, nil
	}
	// 缓存禁用时直接按数据库点赞记录查询
	if !s.cache.IsEnabled() {
		for _, vid := range vidList {
			vidNum, err := strconv.ParseUint(vid, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid video id %q: %w", vid, err)
			}
			isLiked, err := s.likeRepo.IsLiked(ctx, loginUserID, uint(vidNum))
			if err != nil {
				return nil, fmt.Errorf("failed to query like status from db for user %d video %d: %w", loginUserID, vidNum, err)
			}
			resMap[vid] = isLiked
		}
		return resMap, nil
	}

	// 缓存优先：like:user:{userID} 固定为 Set，member 是 video id，
	// 与消费端 SADD / SREM 写入的数据结构完全一致（P0-9）。
	key := cache.LikeUserSetKey(loginUserID)
	members := make([]interface{}, 0, len(vidList))
	for _, vid := range vidList {
		members = append(members, vid)
	}
	isLikedList, err := s.cache.MIsMemberOfSet(ctx, key, members)
	if err != nil {
		return nil, fmt.Errorf("failed to batch check like status in cache for user %d: %w", loginUserID, err)
	}
	for i, vid := range vidList {
		resMap[vid] = isLikedList[i]
	}
	return resMap, nil
}

// BatchGetLikeCount 批量获取一批视频的点赞总数量 map[vid]count
func (s *LikeService) BatchGetLikeCount(ctx context.Context, vidList []string) (map[string]int64, error) {
	countMap := make(map[string]int64)
	if len(vidList) == 0 {
		return countMap, nil
	}
	// 缓存禁用时直接按数据库点赞表统计
	if !s.cache.IsEnabled() {
		for _, vid := range vidList {
			vidNum, err := strconv.ParseUint(vid, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid video id %q: %w", vid, err)
			}
			cnt, err := s.likeRepo.CountByVideo(ctx, uint(vidNum))
			if err != nil {
				return nil, fmt.Errorf("failed to count likes from db for video %d: %w", vidNum, err)
			}
			countMap[vid] = cnt
		}
		return countMap, nil
	}
	for _, vid := range vidList {
		vidNum, err := strconv.ParseUint(vid, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid video id %q: %w", vid, err)
		}
		key := cache.LikeVideoZSetKey(uint(vidNum))
		cnt, err := s.cache.ZCard(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("failed to get like count from cache for video %d: %w", vidNum, err)
		}
		if cnt == 0 {
			// key 不存在说明缓存未命中或 TTL 已到期，回源数据库兜底，避免冷视频点赞数恒为0
			exists, exErr := s.cache.Exists(ctx, key)
			if exErr != nil {
				return nil, fmt.Errorf("failed to check like count cache for video %d: %w", vidNum, exErr)
			}
			if !exists {
				cnt, err = s.likeRepo.CountByVideo(ctx, uint(vidNum))
				if err != nil {
					return nil, fmt.Errorf("failed to count likes from db for video %d: %w", vidNum, err)
				}
			}
		}
		countMap[vid] = cnt
	}
	return countMap, nil
}
