package mq

import (
	"context"
	"errors"
	"feedsystem/internal/model"
	"log"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type CommentMQ struct {
	ch *amqp.Channel // AMQP Channel，用于与RabbitMQ进行通信
}

const (
	commentExchange   = "comment.events" // 交换机名称
	commentQueue      = "comment.events" // 队列名称
	commentBindingKey = "comment.*"      // 绑定键，匹配所有comment相关的事件

	commentCreateRK = "comment.create" // 路由键，用于发布创建评论事件
	commentDeleteRK = "comment.delete" // 路由键，用于发布删除评论事件
)

type CommentEvent struct {
	// 评论事件结构体
	EventID    string         `json:"event_id"` // 全局唯一的消息ID，用于幂等处理，避免MQ重复投递
	Action     string         `json:"action"`   // 事件类型
	Comment    *model.Comment `json:"comment"`  // 评论对象
	OccurredAt time.Time      `json:"occurred_at"`
}

func NewCommentMQ(base *RabbitMQ) (*CommentMQ, error) {
	if base == nil {
		log.Println("RabbitMQ base is nil, cannot create CommentMQ")
		return nil, errors.New("rabbitmq base is nil")
	}

	// 创建一个新的 channel
	ch, err := base.NewChannel()
	if err != nil {
		log.Printf("Failed to create channel for CommentMQ: %v", err)
		return nil, err
	}
	// 声明交换机和队列，并绑定它们
	if err := DeclareTopic(ch, commentExchange, commentQueue, commentBindingKey); err != nil {
		ch.Close()
		log.Printf("Failed to declare exchange/queue for CommentMQ: %v", err)
		return nil, err
	}

	return &CommentMQ{ch: ch}, nil
}

// PublishComment 发布创建评论事件到 RabbitMQ
func (c *CommentMQ) PublishComment(ctx context.Context, comment *model.Comment) error {
	return c.publish(ctx, "create", commentCreateRK, comment)
}

// publish 发布评论事件到 RabbitMQ
func (c *CommentMQ) publish(ctx context.Context, action, routingKey string, comment *model.Comment) error {
	if c == nil || c.ch == nil {
		return errors.New("comment mq is not initialized")
	}
	if comment == nil {
		return errors.New("comment is required")
	}
	if comment.UserID == 0 || comment.VideoID == 0 {
		return errors.New("userID and videoID are required")
	}

	// 生成一个唯一的事件ID
	id, err := newEventID(16)
	if err != nil {
		return err
	}
	// 创建事件对象
	event := CommentEvent{
		EventID:    id,
		Action:     action,
		Comment:    comment,
		OccurredAt: time.Now(),
	}
	// 发布事件到 RabbitMQ
	return PublishJSON(ctx, c.ch, commentExchange, routingKey, event)
}
