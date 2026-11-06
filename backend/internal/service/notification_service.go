package service

import (
	"context"
	"encoding/json"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/infra/mq"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"feedsystem/internal/utils/logger"
	"fmt"
	"strconv"
	"strings"
)

const (
	TypeLike    = "like"
	TypeComment = "comment"
	TypeFollow  = "follow"
)

type NotificationService struct {
	redisCache       *cache.RedisCache
	notificationRepo *repo.NotificationRepo
	videoRepo        *repo.VideoRepo
	commentRepo      *repo.CommentRepo
}

func NewNotificationService(redisCache *cache.RedisCache, notificationRepo *repo.NotificationRepo, videoRepo *repo.VideoRepo, commentRepo *repo.CommentRepo) *NotificationService {
	return &NotificationService{
		redisCache:       redisCache,
		notificationRepo: notificationRepo,
		videoRepo:        videoRepo,
		commentRepo:      commentRepo,
	}
}

// ProcessNotification 返回 error，方便外部调用方（如 worker 和 降级逻辑）统一处理
// 后续如果在函数内部加入重试机制，则需要使用 eventID 做幂等，目前这个字段没作用；全链路追踪已经使用注入到 context 中的 requestID 来做了
func (s *NotificationService) ProcessNotification(ctx context.Context, event mq.NotificationEvent) error {
	var notif *model.Notification
	var err error
	// 根据 action 分发组装通知任务
	switch event.Action {
	case TypeLike:
		notif, err = s.buildLikeNotification(ctx, event)
	case TypeComment:
		notif, err = s.buildCommentNotification(ctx, event)
	default:
		logger.Ctx(ctx).Warn("unknown notification action", logger.String("action", event.Action))
		return nil // 未知类型直接结束
	}

	// 错误处理
	if err != nil {
		logger.Ctx(ctx).Error("build notification failed", logger.Err(err))
		return err
	}
	if notif == nil {
		// 说明是自己给自己点赞的情况，已经在 buildLikeNotification 里过滤掉了
		return nil
	}

	// 落库
	if err := s.notificationRepo.CreateNotification(ctx, notif); err != nil {
		// 如果是唯一键冲突，说明通知已经存在了（MQ 重复消费），直接返回即可
		errStr := err.Error()
		if strings.Contains(errStr, "Duplicate entry") ||
			strings.Contains(errStr, "duplicate key") ||
			strings.Contains(errStr, "UNIQUE") {
			logger.Ctx(ctx).Debug("notification already exists, skip", logger.Err(err))
			return nil // 直接返回，不用再 Increase 未读数和推 SSE 了
		}

		// 其他真正的数据库错误
		logger.Ctx(ctx).Error("create notification failed", logger.Err(err))
		return err
	}

	// 更新 Redis 未读数
	unreadKey := cache.UnreadCountKey(notif.ReceiverID)
	newUnreadCount, err := s.redisCache.Increase(ctx, unreadKey, 1, cache.UnreadCountTTL)
	if err != nil {
		logger.Ctx(ctx).Error("incr unread count failed", logger.Err(err))
	}

	// 如果 newUnreadCount == 1，说明 Redis 里之前没这个 key（过期或宕机了）
	// 这个时候去 DB 查一次真源，把总数拉回来
	if newUnreadCount == 1 {
		dbCount, err := s.notificationRepo.CountUnreadByReceiverID(ctx, notif.ReceiverID)
		if err == nil && dbCount > 1 {
			newUnreadCount = dbCount
			// 把真实的库数据回填到 Redis 中
			err = s.redisCache.SetNX(ctx, unreadKey, strconv.FormatInt(dbCount, 10), cache.UnreadCountTTL)
			if err != nil {
				logger.Ctx(ctx).Error("set unread count to redis failed", logger.Err(err))
			}
		}
	}

	// 触发 SSE 推送
	s.pushSSEViaRedis(ctx, notif.ReceiverID, notif.Content, newUnreadCount)

	return nil
}

func (s *NotificationService) pushSSEViaRedis(ctx context.Context, receiverID uint, content string, unreadCount int64) {
	// 公共频道
	channel := cache.SSEPushChannel()

	// 构造 SSE 消息
	type sseMessage struct {
		UserID      uint   `json:"user_id"`
		Content     string `json:"content"`
		UnreadCount int64  `json:"unread_count,omitempty"`
	}
	data, err := json.Marshal(sseMessage{UserID: receiverID, Content: content, UnreadCount: unreadCount})
	if err != nil {
		logger.Ctx(ctx).Error("marshal sse message failed", logger.Err(err))
		return
	}

	// 通过 Redis 发布消息到公共频道，SSEHandler 会订阅这个频道并推送给对应的用户
	err = s.redisCache.Publish(ctx, channel, string(data))
	if err != nil {
		logger.Ctx(ctx).Error("publish sse message failed", logger.Err(err))
	}
}

// buildLikeNotification 构建点赞通知内容
func (s *NotificationService) buildLikeNotification(ctx context.Context, event mq.NotificationEvent) (*model.Notification, error) {
	// 查视频作者
	video, err := s.videoRepo.FindByID(ctx, event.VideoID)
	if err != nil {
		logger.Ctx(ctx).Error("find video by ID failed", logger.Err(err))
		return nil, err
	}
	if video == nil {
		logger.Ctx(ctx).Error("video not found", logger.Uint("video_id", event.VideoID))
		return nil, fmt.Errorf("video not found: %d", event.VideoID)
	}
	// 过滤掉自己给自己点赞的情况
	if video.AuthorID == event.ActorID {
		logger.Ctx(ctx).Debug("user liked their own video, skip notification", logger.Uint("user_id", event.ActorID), logger.Uint("video_id", event.VideoID))
		return nil, nil
	}

	// 使用点赞者的用户名构建通知内容
	content := fmt.Sprintf("用户 %s 点赞了你的视频", event.UserName)

	// 返回通知对象
	return &model.Notification{
		ReceiverID: video.AuthorID,
		ActorID:    event.ActorID,
		Type:       event.Action,
		TargetID:   event.VideoID,
		Content:    content,
		IsRead:     false,
	}, nil
}

// buildCommentNotification 构建评论通知内容
func (s *NotificationService) buildCommentNotification(ctx context.Context, event mq.NotificationEvent) (*model.Notification, error) {
	var receiverID uint
	// 根据 ParentID 判断是顶级评论还是回复评论
	if event.ParentID == 0 {
		// 如果 pid 为 0，说明是顶级评论，直接查视频作者作为通知接收者
		video, err := s.videoRepo.FindByID(ctx, event.VideoID)
		if err != nil {
			logger.Ctx(ctx).Error("find video by ID failed", logger.Err(err))
			return nil, err
		}
		if video == nil {
			logger.Ctx(ctx).Error("video not found", logger.Uint("video_id", event.VideoID))
			return nil, fmt.Errorf("video not found: %d", event.VideoID)
		}
		// 接收者是视频作者
		receiverID = video.AuthorID
	} else {
		// 如果 pid 不为 0，说明是回复评论，查父评论的作者作为通知接收者
		parentComment, err := s.commentRepo.FindByID(ctx, event.ParentID)
		if err != nil {
			logger.Ctx(ctx).Error("find parent comment by ID failed", logger.Err(err))
			return nil, err
		}
		if parentComment == nil {
			logger.Ctx(ctx).Error("parent comment not found", logger.Uint("comment_id", event.ParentID))
			return nil, fmt.Errorf("parent comment not found: %d", event.ParentID)
		}
		// 接收者是父评论的作者
		receiverID = parentComment.UserID
	}

	// 使用评论者的用户名构建通知内容
	content := fmt.Sprintf("用户 %s 评论了你: %s", event.UserName, event.Content)

	// 返回通知对象
	return &model.Notification{
		ReceiverID: receiverID,
		ActorID:    event.ActorID,
		Type:       event.Action,
		TargetID:   event.CommentID, // 这里必须是评论ID，因为要在数据库中唯一标识一条评论通知，不能用视频ID
		Content:    content,
		IsRead:     false,
	}, nil
}

// GetUnreadCount 获取用户未读通知数，先查 Redis，没命中再查 MySQL
func (s *NotificationService) GetUnreadCount(ctx context.Context, userID uint) (int64, error) {
	// 先查 Redis
	unreadKey := cache.UnreadCountKey(userID)
	val, err := s.redisCache.Get(ctx, unreadKey)
	if err == nil && val != "" {
		// 命中缓存，直接返回
		count, _ := strconv.ParseInt(val, 10, 64)
		return count, nil
	}

	// Redis 没命中（挂了或者过期了），去 MySQL 兜底查
	logger.Ctx(ctx).Info("redis unread count miss, fallback to db", logger.Uint("user_id", userID))

	// 这里的 repo 方法你需要自己写一个：SELECT count(*) WHERE receiver_id = ? AND is_read = false
	count, err := s.notificationRepo.CountUnreadByReceiverID(ctx, userID)
	if err != nil {
		return 0, err
	}

	// 回填 Redis
	// 这里用 SetNX，不要用 Incr，直接覆盖旧值
	// 用 SetNX 而不是 Set 是为了避免在高并发下，多个请求同时回填 Redis，导致计数不准确的问题。因为 SetNX 只有在 key 不存在时才会设置成功。
	err = s.redisCache.SetNX(ctx, unreadKey, strconv.FormatInt(count, 10), cache.UnreadCountTTL)
	if err != nil {
		logger.Ctx(ctx).Error("set unread count to redis failed", logger.Err(err))
		// Redis 异常不影响数据库已成功的结论，只记录日志，避免消息因缓存问题堆积。
	}

	return count, nil
}
