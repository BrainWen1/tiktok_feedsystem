package mq

import (
	"context"
	"encoding/json"
	"errors"
	"feedsystem/internal/repo"
	"log"
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
				_ = msg.Ack(false)
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
				_ = msg.Ack(false)
				continue
			}

			if consumeErr != nil {
				log.Printf("consume %s failed, retry, err=%v", event.Action, consumeErr)
				_ = msg.Nack(false, true)
				continue
			}
			_ = msg.Ack(false)
		}
	}()

	log.Println("like consumer running...")
	return nil
}
