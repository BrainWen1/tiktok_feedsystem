package service

import (
	"context"
	"errors"
	"feedsystem/internal/dto"
	"feedsystem/internal/infra/cache"
	infra "feedsystem/internal/infra/sse"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"feedsystem/internal/utils/logger"
	"fmt"
)

type MessageService struct {
	messageRepo *repo.MessageRepo
	redisCache  *cache.RedisCache
}

func NewMessageService(messageRepo *repo.MessageRepo, redisCache *cache.RedisCache) *MessageService {
	return &MessageService{messageRepo: messageRepo, redisCache: redisCache}
}

// generateSessionID 生成双向唯一的会话ID
// 规则：小的 ID 在前，大的 ID 在后，用下划线拼接。例如 A=10, B=20，结果为 "10_20"。
// 这样不管谁发给谁，计算出的 SessionID 永远是一致的。
func generateSessionID(userA, userB uint) string {
	if userA > userB {
		userA, userB = userB, userA
	}
	return fmt.Sprintf("%d_%d", userA, userB)
}

// SendMessage 发送消息
func (s *MessageService) SendMessage(ctx context.Context, senderID uint, senderName string, receiverID uint, content string) error {
	// 生成会话ID
	sessionID := generateSessionID(senderID, receiverID)

	message := &model.Message{
		SessionID:  sessionID,
		SenderID:   senderID,
		SenderName: senderName,
		ReceiverID: receiverID,
		Content:    content,
		IsRead:     false,
	}

	// 调用 Repo 层创建消息
	err := s.messageRepo.CreateMessage(ctx, message)
	if err != nil {
		logger.Ctx(ctx).Error("create message failed", logger.Err(err))
		return err
	}

	// 落库后通过向 redis 发布一条 pub/sub 消息，通知 SSEHandler 推送给接收者
	// 私信系统的 SSE 消息结构体
	type IMSseMessage struct {
		Type       string `json:"type"`        // "message" 区别于此前的 "notification"
		UserID     uint   `json:"user_id"`     // SendToUser 需要用这个 user_id 找到对应的连接 channel
		SenderID   uint   `json:"sender_id"`   // 用于前端根据 sender_id 调用接口跳转到私信会话页面
		SenderName string `json:"sender_name"` // 用于前端显示发送者昵称，以及个性化渲染
		Content    string `json:"content"`
		CreatedAt  int64  `json:"created_at"`
	}
	// 构造 SSE 消息
	payload := IMSseMessage{
		Type:       "message",
		UserID:     receiverID,
		SenderID:   senderID,
		SenderName: senderName,
		Content:    fmt.Sprintf("%s: %s", senderName, content), // 前端直接显示这个字段即可
		CreatedAt:  message.CreatedAt.Unix(),
	}

	// 调用 infra 层的公共方法 PublishSSEMessage，向 Redis 发布消息
	err = infra.PublishSSEMessage(ctx, s.redisCache, payload)
	if err != nil {
		logger.Ctx(ctx).Error("publish SSE message to redis failed", logger.Err(err))
	}

	return nil
}
func (s *MessageService) GetHistory(ctx context.Context, userID uint, req dto.GetHistoryRequest) (*dto.GetHistoryResponse, error) {
	// 过滤目标用户非法的情况
	if req.TargetID == 0 || req.TargetID == userID {
		return nil, errors.New("invalid target user id")
	}
	// 限制每页条数，防止前端传入过大值导致数据库压力过大
	limit := req.PageSize
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	// 生成会话ID
	sessionID := generateSessionID(userID, req.TargetID)

	// 查库 (limit + 1 用于快速判断是否还有更多消息)
	messages, err := s.messageRepo.GetHistory(ctx, sessionID, req.LastID, limit+1)
	if err != nil {
		logger.Ctx(ctx).Error("get message history failed", logger.Err(err))
		return nil, err
	}

	// 判断 has_more 并截断多余的数据
	hasMore := false
	if len(messages) > limit { // 如果 limit=20，传入limit+1获得了21条（即len(message)=21 > limit=20），则说明还有更多消息
		hasMore = true              // 直接把 hasMore 赋值为 true
		messages = messages[:limit] // 只要前 limit 条 [0, limit)
	}

	// 组装返回游标 (取当前列表的最后一条消息的 ID)
	var nextLastID uint
	if len(messages) > 0 {
		nextLastID = messages[len(messages)-1].ID // repo 里是倒序拉取的，所以最后一条就是最老的那条
	}

	return &dto.GetHistoryResponse{
		Messages:   messages,
		HasMore:    hasMore,
		NextLastID: nextLastID,
	}, nil
}
