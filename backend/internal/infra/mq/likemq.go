package mq

import (
	"context"
	"errors"
	"log"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type LikeMQ struct {
	ch *amqp.Channel // AMQP Channel，用于与RabbitMQ进行通信
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

func NewLikeMQ(base *RabbitMQ) (*LikeMQ, error) {
	if base == nil {
		log.Println("RabbitMQ base is nil, cannot create LikeMQ")
		return nil, errors.New("rabbitmq base is nil")
	}

	// 创建一个新的 channel
	ch, err := base.NewChannel()
	if err != nil {
		log.Printf("Failed to create channel for LikeMQ: %v", err)
		return nil, err
	}
	// 声明交换机和队列，并绑定它们
	if err := DeclareTopic(ch, likeExchange, likeQueue, likeBindingKey); err != nil {
		ch.Close()
		log.Printf("Failed to declare exchange/queue for LikeMQ: %v", err)
		return nil, err
	}

	return &LikeMQ{ch: ch}, nil
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
	if l == nil || l.ch == nil {
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
