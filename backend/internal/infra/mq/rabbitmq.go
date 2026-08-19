package mq

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"feedsystem/internal/config"
	"log"
	"strconv"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// RabbitMQ 只管理 Connection，Channel 由各组件按需创建
type RabbitMQ struct {
	Conn *amqp.Connection
}

// NewRabbitMQ 创建一个新的 RabbitMQ 连接实例
func NewRabbitMQ() (*RabbitMQ, error) {
	// 从全局配置中获取 RabbitMQ 连接信息
	cfg := &config.AppConfig
	// 组装 AMQP URL
	url := "amqp://" + cfg.MQ_Username + ":" + cfg.MQ_Password + "@" + cfg.MQ_Host + ":" + strconv.Itoa(cfg.MQ_Port) + "/"
	// 建立连接
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, err
	}
	return &RabbitMQ{Conn: conn}, nil
}

// Close 关闭 RabbitMQ 连接
func (r *RabbitMQ) Close() error {
	if r == nil {
		return nil
	}
	if r.Conn != nil {
		return r.Conn.Close()
	}
	return nil
}

// NewChannel 创建一个新的 AMQP Channel
func (r *RabbitMQ) NewChannel() (*amqp.Channel, error) {
	if r == nil || r.Conn == nil {
		return nil, errors.New("rabbitmq connection is not initialized")
	}
	return r.Conn.Channel()
}

// DeclareTopic 声明一个 topic 类型的交换机和队列，并绑定它们
func DeclareTopic(ch *amqp.Channel, exchange string, queue string, bindingKey string) error {
	if ch == nil {
		return errors.New("channel is not initialized")
	}
	if exchange == "" || queue == "" || bindingKey == "" {
		return errors.New("exchange/queue/bindingKey is required")
	}

	// 声明交换机
	if err := ch.ExchangeDeclare(
		exchange, // 交换机名称
		"topic",  // 交换机类型
		true,     // 持久化
		false,    // 自动删除
		false,    // 内部交换机
		false,    // 不等待确认
		nil,      // 额外参数
	); err != nil {
		return err
	}

	// 声明队列，并设置死信交换机
	q, err := ch.QueueDeclare(
		queue, // 队列名称
		true,  // 持久化
		false, // 自动删除
		false, // 排他性
		false, // 不等待确认
		amqp.Table{"x-dead-letter-exchange": DLXExchange}, // 设置死信交换机
	)
	if err != nil {
		return err
	}

	// 绑定队列到交换机
	if err := ch.QueueBind(
		q.Name,     // 队列名称
		bindingKey, // 绑定键
		exchange,   // 交换机名称
		false,      // 不等待确认
		nil,        // 额外参数
	); err != nil {
		return err
	}
	// 声明死信交换机和队列
	if err := DeclareDLX(ch, queue); err != nil {
		log.Printf("DLX declare failed for %s: %v", queue, err)
	}
	return nil
}

// PublishJSON 将 JSON 消息发布到指定的交换机和路由键
func PublishJSON(ctx context.Context, ch *amqp.Channel, exchange string, routingKey string, payload any) error {
	if ch == nil {
		return errors.New("channel is not initialized")
	}
	if exchange == "" || routingKey == "" {
		return errors.New("exchange and routingKey are required")
	}

	// 将 payload 序列化为 JSON
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	// 发布消息
	return ch.PublishWithContext(ctx, exchange, routingKey, false, false, amqp.Publishing{
		ContentType:  "application/json", // 设置内容类型为 JSON
		DeliveryMode: amqp.Persistent,    // 持久化消息
		Timestamp:    time.Now(),         // 设置时间戳
		Body:         body,               // 消息体
	})
}

// newEventID 生成一个随机的事件 ID，用于消息追踪
func newEventID(n int) (string, error) {
	// 生成 n 字节的随机数
	b := make([]byte, n)
	// 使用 crypto/rand 生成安全的随机数
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// 将字节切片编码为十六进制字符串
	return hex.EncodeToString(b), nil
}
