package service

import (
	"context"
	"feedsystem/internal/dto"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/infra/mq"
	infra "feedsystem/internal/infra/sse"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"feedsystem/internal/utils/logger"
	"fmt"
	"strconv"
	"time"
)

const (
	TypeLike    = "like"
	TypeComment = "comment"
	TypeFollow  = "follow"

	// TypeUnlike / TypeUnfollow 只用于取消通知，不会落库为新通知。
	TypeUnlike   = "unlike"
	TypeUnfollow = "unfollow"
)

const (
	// 通知业务键分布式锁：TTL 需覆盖一次“查最新记录 + 插入/更新”，
	// 锁只用于减少并发竞争，正确性由 notifications.uk_active 唯一索引兜底。
	notificationLockTTL      = 5 * time.Second
	notificationLockWait     = 50 * time.Millisecond
	notificationLockAttempts = 3
)

type NotificationService struct {
	redisCache       *cache.RedisCache
	notificationRepo *repo.NotificationRepo
	videoRepo        *repo.VideoRepo
	commentRepo      *repo.CommentRepo
	// likeRepo / socialRepo 用于落通知前复核互动是否仍然有效，
	// 避免“点赞→取消”两条异步消息乱序时给已取消的互动发通知。
	likeRepo   *repo.LikeRepo
	socialRepo *repo.SocialRepo
}

func NewNotificationService(redisCache *cache.RedisCache, notificationRepo *repo.NotificationRepo, videoRepo *repo.VideoRepo, commentRepo *repo.CommentRepo, likeRepo *repo.LikeRepo, socialRepo *repo.SocialRepo) *NotificationService {
	return &NotificationService{
		redisCache:       redisCache,
		notificationRepo: notificationRepo,
		videoRepo:        videoRepo,
		commentRepo:      commentRepo,
		likeRepo:         likeRepo,
		socialRepo:       socialRepo,
	}
}

// ProcessNotification 返回 error，方便外部调用方（如 worker 和 降级逻辑）统一处理
// 后续如果在函数内部加入重试机制，则需要使用 eventID 做幂等，目前这个字段没作用；全链路追踪已经使用注入到 context 中的 requestID 来做了
func (s *NotificationService) ProcessNotification(ctx context.Context, event mq.NotificationEvent) error {
	switch event.Action {
	// 产生通知：点赞 / 评论 / 关注
	case TypeLike, TypeComment, TypeFollow:
		return s.createNotification(ctx, event)
	// 取消通知：只把已有记录置为 cancelled，不推 SSE
	case TypeUnlike:
		return s.cancelLikeNotification(ctx, event)
	case TypeUnfollow:
		return s.cancelNotification(ctx, event.ActorID, event.BloggerID, TypeFollow, event.BloggerID)
	default:
		logger.Ctx(ctx).Warn("unknown notification action", logger.String("action", event.Action))
		return nil // 未知类型直接结束
	}
}

// createNotification 处理“新增通知”的完整流程：
//  1. 组装通知（自己给自己的互动直接过滤）；
//  2. 按业务键加分布式锁；
//  3. 锁内复核互动关系是否仍然有效，规避点赞/取消乱序；
//  4. 查该业务键最新一条记录，active 则说明重复（MQ 重投/并发），直接返回；
//  5. 插入新的 active 记录，uk_active 唯一索引兜底并发；
//  6. 锁外更新未读数并推送 SSE。
func (s *NotificationService) createNotification(ctx context.Context, event mq.NotificationEvent) error {
	notif, err := s.buildNotification(ctx, event)
	if err != nil {
		logger.Ctx(ctx).Error("build notification failed", logger.Err(err))
		return err
	}
	if notif == nil {
		// 自己给自己点赞等情况，已在 build 阶段过滤
		return nil
	}

	lockKey := cache.NotificationLockKey(notif.ActorID, notif.ReceiverID, notif.Type, notif.TargetID)
	token, locked := s.acquireNotificationLock(ctx, lockKey)
	// 落库和未读数更新都在锁内完成，避免与取消路径的缓存清理交错导致计数漂移；
	// 推送 SSE 放到锁外，避免网络耗时拉长锁占用时间。
	inserted, unreadCount, err := s.insertActiveNotification(ctx, notif)
	if locked {
		s.releaseNotificationLock(ctx, lockKey, token)
	}
	if err != nil {
		return err
	}
	if !inserted {
		return nil
	}

	s.pushSSEViaRedis(ctx, notif, unreadCount)
	return nil
}

// insertActiveNotification 在分布式锁内完成“复核互动关系 → 查最新记录 → 插入 active 通知 → 更新未读数”。
// 返回 inserted=false 表示本次没有新增通知（重复、通知已存在或互动已取消）。
func (s *NotificationService) insertActiveNotification(ctx context.Context, notif *model.Notification) (bool, int64, error) {
	// 复核互动是否仍然有效：点赞和取消是两条独立的异步消息，可能乱序到达，
	// 不加这一步会给已经取消的点赞补发一条 active 通知。
	active, err := s.interactionStillActive(ctx, notif)
	if err != nil {
		return false, 0, err
	}
	if !active {
		logger.Ctx(ctx).Debug("interaction no longer active, skip notification",
			logger.Uint("actor_id", notif.ActorID),
			logger.Uint("receiver_id", notif.ReceiverID),
			logger.String("type", notif.Type),
			logger.Uint("target_id", notif.TargetID))
		return false, 0, nil
	}

	latest, err := s.notificationRepo.FindLatestByTuple(ctx, notif.ActorID, notif.ReceiverID, notif.Type, notif.TargetID)
	if err != nil {
		logger.Ctx(ctx).Error("find latest notification failed", logger.Err(err))
		return false, 0, err
	}
	// 最新一条仍是 active：说明这条互动已经通知过，直接返回，
	// 不再增加未读数、不再推 SSE（MQ 重复投递/并发重复触发）。
	if latest != nil && latest.Status == model.NotificationStatusActive {
		logger.Ctx(ctx).Debug("notification already active, skip",
			logger.Uint("actor_id", notif.ActorID),
			logger.Uint("receiver_id", notif.ReceiverID),
			logger.String("type", notif.Type),
			logger.Uint("target_id", notif.TargetID))
		return false, 0, nil
	}
	// latest 为 nil（首次通知）或 status=cancelled（取消后重新互动），都插入一条全新的 active 记录。

	activeKey := model.ActiveKeyFor(notif.ActorID, notif.ReceiverID, notif.Type, notif.TargetID)
	notif.Status = model.NotificationStatusActive
	notif.ActiveKey = &activeKey

	if err := s.notificationRepo.CreateNotification(ctx, notif); err != nil {
		// uk_active 唯一索引兜底：并发重复或锁失效时说明已经有一条 active 通知。
		if repo.IsDuplicateKey(err) {
			logger.Ctx(ctx).Debug("notification already active, skip duplicate insert", logger.Err(err))
			return false, 0, nil
		}
		logger.Ctx(ctx).Error("create notification failed", logger.Err(err))
		return false, 0, err
	}

	return true, s.increaseUnread(ctx, notif), nil
}

// buildNotification 根据 action 组装通知任务。
func (s *NotificationService) buildNotification(ctx context.Context, event mq.NotificationEvent) (*model.Notification, error) {
	switch event.Action {
	case TypeLike:
		return s.buildLikeNotification(ctx, event)
	case TypeComment:
		return s.buildCommentNotification(ctx, event)
	case TypeFollow:
		return s.buildFollowNotification(ctx, event)
	default:
		return nil, fmt.Errorf("unknown notification action: %s", event.Action)
	}
}

// interactionStillActive 复核通知对应的互动关系是否仍然存在。
// 评论没有取消语义，直接返回 true；点赞/关注以数据库关系表为准。
func (s *NotificationService) interactionStillActive(ctx context.Context, notif *model.Notification) (bool, error) {
	switch notif.Type {
	case TypeLike:
		if s.likeRepo == nil {
			return true, nil
		}
		liked, err := s.likeRepo.IsLiked(ctx, notif.ActorID, notif.TargetID)
		if err != nil {
			logger.Ctx(ctx).Error("check like relation failed", logger.Uint("actor_id", notif.ActorID), logger.Uint("video_id", notif.TargetID), logger.Err(err))
			return false, fmt.Errorf("check like relation for user %d video %d: %w", notif.ActorID, notif.TargetID, err)
		}
		return liked, nil
	case TypeFollow:
		if s.socialRepo == nil {
			return true, nil
		}
		following, err := s.socialRepo.IsFollowing(ctx, notif.ActorID, notif.TargetID)
		if err != nil {
			logger.Ctx(ctx).Error("check follow relation failed", logger.Uint("actor_id", notif.ActorID), logger.Uint("blogger_id", notif.TargetID), logger.Err(err))
			return false, fmt.Errorf("check follow relation for user %d blogger %d: %w", notif.ActorID, notif.TargetID, err)
		}
		return following, nil
	default:
		return true, nil
	}
}

// cancelLikeNotification 取消点赞通知：接收者是视频作者，需要先查视频。
func (s *NotificationService) cancelLikeNotification(ctx context.Context, event mq.NotificationEvent) error {
	video, err := s.videoRepo.FindByID(ctx, event.VideoID)
	if err != nil {
		logger.Ctx(ctx).Error("find video by ID failed", logger.Err(err))
		return err
	}
	if video == nil {
		// 视频已被删除，没有可取消的通知，按幂等处理
		logger.Ctx(ctx).Debug("video not found when cancelling like notification", logger.Uint("video_id", event.VideoID))
		return nil
	}
	// 自己给自己点赞本来就不产生通知
	if video.AuthorID == event.ActorID {
		return nil
	}
	return s.cancelNotification(ctx, event.ActorID, video.AuthorID, TypeLike, event.VideoID)
}

// cancelNotification 取消通知：把该业务键下最新的 active 记录置为 cancelled，
// 保留历史记录供通知中心展示；同时清理未读数缓存，保证红点数字准确。
// 取消动作本身不推送 SSE。
func (s *NotificationService) cancelNotification(ctx context.Context, actorID, receiverID uint, typ string, targetID uint) error {
	if actorID == 0 || receiverID == 0 {
		return nil
	}

	lockKey := cache.NotificationLockKey(actorID, receiverID, typ, targetID)
	token, locked := s.acquireNotificationLock(ctx, lockKey)
	if locked {
		defer s.releaseNotificationLock(ctx, lockKey, token)
	}

	cancelled, err := s.notificationRepo.CancelActiveByTuple(ctx, actorID, receiverID, typ, targetID)
	if err != nil {
		logger.Ctx(ctx).Error("cancel notification failed", logger.Uint("actor_id", actorID), logger.Uint("receiver_id", receiverID), logger.String("type", typ), logger.Uint("target_id", targetID), logger.Err(err))
		return err
	}
	if cancelled == nil {
		// 没有 active 记录：重复取消，或点赞通知还没来得及创建，按幂等处理
		logger.Ctx(ctx).Debug("no active notification to cancel", logger.Uint("actor_id", actorID), logger.Uint("receiver_id", receiverID), logger.String("type", typ), logger.Uint("target_id", targetID))
		return nil
	}

	// 未读数以数据库为准：直接删除缓存，下次查询自动回源并按 status=active 重算。
	unreadKey := cache.UnreadCountKey(receiverID)
	if err := s.redisCache.Delete(ctx, unreadKey); err != nil {
		logger.Ctx(ctx).Error("delete unread count cache failed", logger.Uint("receiver_id", receiverID), logger.Err(err))
	}
	return nil
}

// acquireNotificationLock 按业务键尝试获取分布式锁。
// 返回 locked=false 表示锁不可用或竞争激烈，此时调用方继续执行，
// 由 uk_active 唯一索引保证不会产生重复的 active 通知。
func (s *NotificationService) acquireNotificationLock(ctx context.Context, key string) (string, bool) {
	if s.redisCache == nil || !s.redisCache.IsEnabled() {
		return "", false
	}
	for attempt := 1; attempt <= notificationLockAttempts; attempt++ {
		token, ok, err := s.redisCache.Lock(ctx, key, notificationLockTTL)
		if err != nil {
			logger.Ctx(ctx).Error("acquire notification lock failed", logger.String("key", key), logger.Err(err))
			return "", false
		}
		if ok {
			return token, true
		}
		if attempt == notificationLockAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return "", false
		case <-time.After(notificationLockWait):
		}
	}
	logger.Ctx(ctx).Warn("notification lock busy, fallback to unique index", logger.String("key", key))
	return "", false
}

// releaseNotificationLock 释放分布式锁。释放动作不受上层请求取消影响。
func (s *NotificationService) releaseNotificationLock(ctx context.Context, key, token string) {
	if token == "" || s.redisCache == nil {
		return
	}
	unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	if err := s.redisCache.Unlock(unlockCtx, key, token); err != nil {
		logger.Ctx(ctx).Error("release notification lock failed", logger.String("key", key), logger.Err(err))
	}
}

// increaseUnread 通知成功落库后更新 Redis 未读数，返回最新未读数。
// 在分布式锁内调用，避免与取消路径删除缓存的动作交错产生计数漂移。
func (s *NotificationService) increaseUnread(ctx context.Context, notif *model.Notification) int64 {
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
	return newUnreadCount
}

// NotifSSEMessage 是一个结构体，用于封装 SSE 推送的消息内容
type NotifSSEMessage struct {
	Type        string    `json:"type"` // 核心区分：前端根据这个字段判断是 'notification' 还是 'message'
	UserID      uint      `json:"user_id"`
	Content     string    `json:"content"`
	UnreadCount int64     `json:"unread_count,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *NotificationService) pushSSEViaRedis(ctx context.Context, notif *model.Notification, unreadCount int64) {
	// 构造 SSE 消息
	msg := NotifSSEMessage{
		Type:        "notification",
		UserID:      notif.ReceiverID,
		Content:     notif.Content,
		UnreadCount: unreadCount,
		CreatedAt:   notif.CreatedAt,
	}

	// 调用 infra 层的公共方法 PublishSSEMessage，向 Redis 发布消息
	err := infra.PublishSSEMessage(ctx, s.redisCache, msg)
	if err != nil {
		logger.Ctx(ctx).Error("publish SSE message to redis failed", logger.Err(err))
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

func (s *NotificationService) buildFollowNotification(ctx context.Context, event mq.NotificationEvent) (*model.Notification, error) {
	content := fmt.Sprintf("用户 %s 关注了你", event.UserName)

	return &model.Notification{
		ReceiverID: event.BloggerID,
		ActorID:    event.ActorID,
		Type:       event.Action,
		TargetID:   event.BloggerID, // 关注通知的 TargetID 是被关注者的 ID，保证唯一性
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

	// 调用 repo 层的 CountUnreadByReceiverID 方法
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

// buildNotificationListResp 将原始model.Notification切片，组装成dto.ListNotificationsResponse，包含next_cursor
func buildNotificationListResp(notifications []model.Notification) dto.ListNotificationsResponse {
	resp := dto.ListNotificationsResponse{
		Notifications: notifications,
	}
	// 有数据才返回next_cursor；列表为空，next_cursor直接不返回（前端判定没有更多）
	if len(notifications) > 0 {
		last := notifications[len(notifications)-1]
		resp.NextLastTime = &last.CreatedAt
		resp.NextLastID = &last.ID
	}
	return resp
}

// ListNotifications 获取用户通知列表
func (s *NotificationService) ListNotifications(ctx context.Context, userID uint, lastTime time.Time, lastID uint, limit int) (dto.ListNotificationsResponse, error) {
	// 统一将 lastTime 转为 UTC，避免时区问题
	if !lastTime.IsZero() {
		lastTime = lastTime.UTC() // 统一转为 UTC 去查库
	}
	// 解析 limit，保证合理范围
	if limit <= 0 || limit > 100 {
		limit = 20 // 默认值
	}
	// 调用 repo 层获取通知列表
	notifications, err := s.notificationRepo.ListByCursor(ctx, userID, lastTime, lastID, limit)
	if err != nil {
		return dto.ListNotificationsResponse{}, err
	}

	// 组装成 dto.ListNotificationsResponse，包含 next_cursor
	resp := buildNotificationListResp(notifications)

	return resp, nil
}

// MarkAsRead 标记单条通知为已读
func (s *NotificationService) MarkAsRead(ctx context.Context, userID, notificationID uint) error {
	// 调用 repo 层的 MarkRead 方法，确保只更新未读的通知
	changed, err := s.notificationRepo.MarkRead(ctx, userID, notificationID)
	if err != nil {
		logger.Ctx(ctx).Error("mark notification as read failed", logger.Uint("user_id", userID), logger.Uint("notification_id", notificationID), logger.Err(err))
		return err
	}

	// 如果确实更新了，则处理缓存
	if changed {
		unreadKey := cache.UnreadCountKey(userID)
		// 简单粗暴且绝对安全的方法：直接删除缓存
		// 前端此时会下一次查 GetUnreadCount，发现 Redis 没了，自动去 DB 算一遍准确的
		// 这比维护 DECR 遇到并发和负数边界要靠谱得多
		err = s.redisCache.Delete(ctx, unreadKey)
		if err != nil {
			logger.Ctx(ctx).Error("delete unread count cache failed", logger.Uint("user_id", userID), logger.Err(err))
		}
	}
	return nil
}

// MarkAllAsRead 标记全部通知为已读
func (s *NotificationService) MarkAllAsRead(ctx context.Context, userID uint) error {
	// 调用 repo 层的 MarkAllRead 方法，将所有未读通知标记为已读
	if err := s.notificationRepo.MarkAllRead(ctx, userID); err != nil {
		return err
	}
	// 全部都已读，直接删除 Redis 缓存
	unreadKey := cache.UnreadCountKey(userID)
	err := s.redisCache.Delete(ctx, unreadKey)
	if err != nil {
		logger.Ctx(ctx).Error("delete unread count cache failed", logger.Uint("user_id", userID), logger.Err(err))
	}
	return nil
}
