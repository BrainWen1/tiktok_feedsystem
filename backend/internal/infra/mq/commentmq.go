package mq

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"feedsystem/internal/model"
	"feedsystem/internal/utils/logger"

	amqp "github.com/rabbitmq/amqp091-go"
)

type CommentMQ struct {
	ch      *amqp.Channel // AMQP Channel，用于与RabbitMQ进行通信
	enabled atomic.Bool   // MQ是否可用；禁用时投递入口直接短路
}

// IsEnabled 返回CommentMQ是否可用，nil实例视为不可用
func (c *CommentMQ) IsEnabled() bool {
	return c != nil && c.enabled.Load()
}

// Enable 启用CommentMQ
func (c *CommentMQ) Enable() {
	if c != nil {
		c.enabled.Store(true)
	}
}

// Disable 禁用CommentMQ，后续投递全部跳过
func (c *CommentMQ) Disable() {
	if c != nil {
		c.enabled.Store(false)
	}
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

// NewCommentMQ 创建CommentMQ；初始化失败时返回已禁用的合法实例而非nil
func NewCommentMQ(base *RabbitMQ) (*CommentMQ, error) {
	c := &CommentMQ{}
	if base == nil || !base.IsEnabled() {
		logger.Warn("rabbitmq disabled, CommentMQ disabled")
		c.Disable()
		return c, ErrMQDisabled
	}

	// 创建一个新的 channel
	ch, err := base.NewChannel()
	if err != nil {
		logger.Error("create channel for CommentMQ failed, CommentMQ disabled", logger.Err(err))
		c.Disable()
		return c, err
	}
	// 声明交换机和队列，并绑定它们
	if err := DeclareTopic(ch, commentExchange, commentQueue, commentBindingKey); err != nil {
		ch.Close()
		logger.Error("declare exchange/queue for CommentMQ failed, CommentMQ disabled", logger.Err(err))
		c.Disable()
		return c, err
	}

	c.ch = ch
	c.Enable()
	return c, nil
}

// PublishComment 发布创建评论事件到 RabbitMQ
func (c *CommentMQ) PublishComment(ctx context.Context, comment *model.Comment) error {
	return c.publish(ctx, "create", commentCreateRK, comment)
}

// DeleteComment 发布删除评论事件到 RabbitMQ
func (c *CommentMQ) DeleteComment(ctx context.Context, comment *model.Comment) error {
	return c.publish(ctx, "delete", commentDeleteRK, comment)
}

// publish 发布评论事件到 RabbitMQ
func (c *CommentMQ) publish(ctx context.Context, action, routingKey string, comment *model.Comment) error {
	if !c.IsEnabled() {
		return ErrMQDisabled // MQ已禁用，跳过投递，由service层降级写数据库
	}
	if c.ch == nil {
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
