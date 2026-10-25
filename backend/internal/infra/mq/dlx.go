// DLX (Dead Letter Exchange) 死信交换机相关操作
package mq

import (
	"context"
	"errors"
	"log"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	DLXExchange   = "dlx.events"
	MaxRetryCount = 3

	// RetryCountHeader 消费失败重投时携带的重试次数头。
	// Nack(requeue=true) 不会写入 x-death，也无法在 Nack 时改写消息头，
	// 因此重投路径显式 republish 并累加该头，才能让 MaxRetryCount 真正生效。
	RetryCountHeader = "x-retry-count"

	maxRetryBackoff = 2 * time.Second // 单次退避上限，避免阻塞消费循环过久
)

// DeclareDLX 声明死信交换机和对应的死信队列
func DeclareDLX(ch *amqp.Channel, queueName string) error {
	if ch == nil {
		return nil
	}
	// 声明死信交换机
	if err := ch.ExchangeDeclare(
		DLXExchange, // 死信交换机名称
		"topic",     // 交换机类型为 topic
		true,        // 持久化
		false,       // 不自动删除
		false,       // 非内部交换机
		false,       // 不等待确认
		nil,         // 额外参数
	); err != nil {
		return err
	}
	// 声明死信队列
	dlxQueue := queueName + ".dlx"
	_, err := ch.QueueDeclare(
		dlxQueue,
		true,  // 持久化
		false, // 自动删除
		false, // 排他性
		false, // 不等待确认
		nil,   // 额外参数
	)
	if err != nil {
		return err
	}
	// 绑定死信队列到死信交换机，使用通配符 "#" 以接收所有路由键的消息
	if err := ch.QueueBind(dlxQueue, "#", DLXExchange, false, nil); err != nil {
		return err
	}
	log.Printf("DLX ready: exchange=%s queue=%s", DLXExchange, dlxQueue)
	return nil
}

// GetRetryCount 从消息头中提取当前消息已被重试的次数。
// 优先读重投路径写入的 RetryCountHeader，其次兼容 x-death（经 DLX 重新投递的场景）。
func GetRetryCount(d amqp.Delivery) int {
	if v, ok := d.Headers[RetryCountHeader]; ok {
		switch n := v.(type) {
		case int:
			return n
		case int32:
			return int(n)
		case int64:
			return int(n)
		}
	}

	// 检查 x-death header 是否存在，并尝试解析重试次数
	deaths, ok := d.Headers["x-death"].([]interface{})
	if !ok || len(deaths) == 0 {
		return 0
	}
	// x-death header 是一个数组，取第一个元素，它是一个 amqp.Table
	death, ok := deaths[0].(amqp.Table)
	if !ok {
		return 0
	}
	// 从 amqp.Table 中获取 "count" 字段，它表示消息被拒绝的次数
	count, ok := death["count"].(int64)
	if !ok {
		return 0
	}
	return int(count)
}

// RetryOrDeadLetter 处理消费者遇到的可重试故障：
//   - 重试次数达到 MaxRetryCount：Nack 不重回原队列，由队列的 x-dead-letter-exchange 转入死信 DLX；
//   - 未达上限：按次数指数退避后重新投递，并累加 RetryCountHeader，成功后 ACK 原消息。
//
// 重投不直接使用 Nack(requeue=true)，因为 requeue 无法携带重试计数，会导致消息在
// 故障期间无限热循环；republish + Ack 才能让重试次数在消息头上持久化。
func RetryOrDeadLetter(ch *amqp.Channel, msg amqp.Delivery, reason error) {
	retry := GetRetryCount(msg)
	if retry >= MaxRetryCount {
		log.Printf("retry exhausted (%d/%d), move message to DLX: %v", retry, MaxRetryCount, reason)
		if err := msg.Nack(false, false); err != nil {
			log.Printf("nack to DLX failed: %v", err)
		}
		return
	}

	if err := requeueWithBackoff(ch, msg, retry); err != nil {
		// 重投失败时退回 requeue，至少保证消息不丢；下轮仍会重新计数
		log.Printf("republish for retry failed, fallback to requeue: %v", err)
		if nackErr := msg.Nack(false, true); nackErr != nil {
			log.Printf("nack requeue failed: %v", nackErr)
		}
		return
	}

	log.Printf("message requeued for retry %d/%d: %v", retry+1, MaxRetryCount, reason)
	if err := msg.Ack(false); err != nil {
		log.Printf("ack original message after republish failed: %v", err)
	}
}

// requeueWithBackoff 退避后将消息重新发布回原交换机/路由键，并把重试次数加一写入消息头
func requeueWithBackoff(ch *amqp.Channel, msg amqp.Delivery, retry int) error {
	if ch == nil {
		return errors.New("amqp channel is not initialized")
	}
	time.Sleep(retryBackoff(retry))

	headers := make(amqp.Table, len(msg.Headers)+1)
	for k, v := range msg.Headers {
		headers[k] = v
	}
	headers[RetryCountHeader] = int32(retry + 1)

	pubCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	return ch.PublishWithContext(pubCtx, msg.Exchange, msg.RoutingKey, false, false, amqp.Publishing{
		ContentType:  msg.ContentType,
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now(),
		Headers:      headers,
		Body:         msg.Body,
	})
}

// retryBackoff 计算简单指数退避：200ms、400ms、800ms，最多 2s
func retryBackoff(retry int) time.Duration {
	delay := time.Duration(1<<uint(retry)) * 200 * time.Millisecond
	if delay > maxRetryBackoff {
		return maxRetryBackoff
	}
	return delay
}
