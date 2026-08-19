// DLX (Dead Letter Exchange) 死信交换机相关操作
package mq

import (
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	DLXExchange   = "dlx.events"
	MaxRetryCount = 3
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

// GetRetryCount 从 AMQP x-death header 中提取当前消息已被重试的次数
func GetRetryCount(d amqp.Delivery) int {
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
