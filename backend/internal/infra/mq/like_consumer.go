package mq

import (
	"context"
	"encoding/json"
	"errors"
	"feedsystem/internal/repo"
	"log"
	"strings"
)

// StartLikeConsumer 启动点赞消费，给worker调用
func StartLikeConsumer(lmq *LikeMQ, likeRepo *repo.LikeRepo) error {
	if lmq == nil || lmq.ch == nil {
		return errors.New("like mq is not initialized")
	}
	if likeRepo == nil {
		return errors.New("like repo is not initialized")
	}

	// 声明死信队列和绑定
	ch := lmq.ch
	if err := DeclareDLX(ch, likeQueue); err != nil {
		return err
	}

	// 开始消费点赞事件
	msgs, err := ch.Consume(likeQueue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	// 使用goroutine处理消息
	go func() {
		for msg := range msgs {
			log.Printf("received like event: %s", msg.Body)
			var event LikeEvent
			// 解析消息体为LikeEvent结构体s
			if err := json.Unmarshal(msg.Body, &event); err != nil {
				log.Println("parse like event failed:", err)
				_ = msg.Ack(false) // 解析失败的消息直接ACK掉，避免无限重试
				continue
			}

			// 根据事件类型调用相应的repo方法处理点赞或取消点赞
			var consumeErr error
			switch event.Action {
			case "like":
				consumeErr = likeRepo.CreateLike(context.Background(), event.UserID, event.VideoID)
			case "unlike":
				consumeErr = likeRepo.DeleteLike(context.Background(), event.UserID, event.VideoID)
			default:
				log.Printf("unknown like event action: %s", event.Action)
				_ = msg.Ack(false) // 未知的事件类型直接ACK掉，避免无限重试
				continue
			}

			if consumeErr != nil {
				// 如果是数据库唯一键冲突（Duplicate entry / UNIQUE），说明这条点赞记录已存在，
				// 这属于幂等场景，可以安全忽略并 ACK 掉消息，避免无意义重试。
				// 不同数据库/驱动返回的错误字符串可能不同，这里做宽松字符串匹配。
				errStr := consumeErr.Error()
				if strings.Contains(errStr, "Duplicate entry") ||
					strings.Contains(errStr, "duplicate key") ||
					strings.Contains(errStr, "UNIQUE") {
					log.Printf("like already exists, treat as success: user=%d video=%d, err=%v", event.UserID, event.VideoID, consumeErr)
					_ = msg.Ack(false)
					continue
				}

				// 永久错误：视频不存在或用户不存在，直接丢弃消息到死信队列
				if strings.Contains(errStr, "video not found") || strings.Contains(errStr, "user not exist") {
					log.Printf("permanent error, drop to DLX: %v", consumeErr)
					_ = msg.Nack(false, false) //不再放回原队列，进入死信队列
					continue
				}
				// 临时故障：数据库抖动、锁超时，放回队列重试
				log.Printf("consume %s failed, retry, err=%v", event.Action, consumeErr)
				_ = msg.Nack(false, true)

				continue
			}
			_ = msg.Ack(false) // 成功处理后 ACK 掉消息
		}
	}()

	log.Println("like consumer running...")
	return nil
}
