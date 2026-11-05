package mq

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"feedsystem/internal/utils/logger"

	amqp "github.com/rabbitmq/amqp091-go"
)

type LikeMQ struct {
	ch      *amqp.Channel // AMQP Channel，用于与RabbitMQ进行通信
	enabled atomic.Bool   // MQ是否可用；禁用时投递入口直接短路
}

// IsEnabled 返回LikeMQ是否可用，nil实例视为不可用
func (l *LikeMQ) IsEnabled() bool {
	return l != nil && l.enabled.Load()
}

// Enable 启用LikeMQ
func (l *LikeMQ) Enable() {
	if l != nil {
		l.enabled.Store(true)
	}
}

// Disable 禁用LikeMQ，后续投递全部跳过
func (l *LikeMQ) Disable() {
	if l != nil {
		l.enabled.Store(false)
	}
}

const (
	likeExchange   = "like.events" // 交换机名称
	likeQueue      = "like.events" // 队列名称
	likeBindingKey = "like.*"      // 绑定键，匹配所有like相关的事件

	likeLikeRK   = "like.like"   // 路由键，用于发布点赞事件
	likeUnlikeRK = "like.unlike" // 路由键，用于发布取消点赞事件
)

type LikeEvent struct {
	// 点赞事件结构体
	EventID    string    `json:"event_id"` // 全局唯一的消息ID，用于幂等处理，避免MQ重复投递
	Action     string    `json:"action"`
	UserID     uint      `json:"user_id"`
	VideoID    uint      `json:"video_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

// NewLikeMQ 创建LikeMQ；初始化失败时返回已禁用的合法实例而非nil
func NewLikeMQ(base *RabbitMQ) (*LikeMQ, error) {
	l := &LikeMQ{}
	if base == nil || !base.IsEnabled() {
		logger.Warn("rabbitmq disabled, LikeMQ disabled")
		l.Disable()
		return l, ErrMQDisabled
	}

	// 创建一个新的 channel
	ch, err := base.NewChannel()
	if err != nil {
		logger.Error("create channel for LikeMQ failed, LikeMQ disabled", logger.Err(err))
		l.Disable()
		return l, err
	}
	// 声明交换机和队列，并绑定它们
	if err := DeclareTopic(ch, likeExchange, likeQueue, likeBindingKey); err != nil {
		ch.Close()
		logger.Error("declare exchange/queue for LikeMQ failed, LikeMQ disabled", logger.Err(err))
		l.Disable()
		return l, err
	}

	l.ch = ch
	l.Enable()
	return l, nil
}

// Like 发布点赞事件到 RabbitMQ
func (l *LikeMQ) Like(ctx context.Context, userID, videoID uint) error {
	return l.publish(ctx, "like", likeLikeRK, userID, videoID)
}

// Unlike 发布取消点赞事件到 RabbitMQ
func (l *LikeMQ) Unlike(ctx context.Context, userID, videoID uint) error {
	return l.publish(ctx, "unlike", likeUnlikeRK, userID, videoID)
}

// publish 发布点赞或取消点赞事件到 RabbitMQ
func (l *LikeMQ) publish(ctx context.Context, action, routingKey string, userID, videoID uint) error {
	if !l.IsEnabled() {
		return ErrMQDisabled // MQ已禁用，跳过投递，由service层降级写数据库
	}
	if l.ch == nil {
		return errors.New("like mq is not initialized")
	}
	if userID == 0 || videoID == 0 {
		return errors.New("userID and videoID are required")
	}

	// 生成一个唯一的事件ID
	id, err := newEventID(16)
	if err != nil {
		return err
	}
	// 创建事件对象
	event := LikeEvent{
		EventID:    id,
		Action:     action,
		UserID:     userID,
		VideoID:    videoID,
		OccurredAt: time.Now(),
	}
	// 发布事件到 RabbitMQ
	return PublishJSON(ctx, l.ch, likeExchange, routingKey, event)
}
