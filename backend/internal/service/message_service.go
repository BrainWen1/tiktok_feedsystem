package service

import (
	"context"
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

// SendMessage 发送消息
func (s *MessageService) SendMessage(ctx context.Context, senderID uint, senderName string, receiverID uint, content string) error {
	message := &model.Message{
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
		Type:      "message",
		UserID:    receiverID,
		Content:   fmt.Sprintf("%s: %s", senderName, content), // 前端直接显示这个字段即可
		CreatedAt: message.CreatedAt.Unix(),
	}

	// 调用 infra 层的公共方法 PublishSSEMessage，向 Redis 发布消息
	err = infra.PublishSSEMessage(ctx, s.redisCache, payload)
	if err != nil {
		logger.Ctx(ctx).Error("publish SSE message to redis failed", logger.Err(err))
	}

	return nil
}
