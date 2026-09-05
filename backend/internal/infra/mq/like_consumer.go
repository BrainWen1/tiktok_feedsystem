package mq

import (
	"context"
	"encoding/json"
	"errors"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/repo"
	"fmt"
	"log"
	"strings"
	"time"
)

// StartLikeConsumer 启动点赞消费，给worker调用
func StartLikeConsumer(lmq *LikeMQ, likeRepo *repo.LikeRepo, cache *cache.RedisCache) error {
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
			// 设置一个超时上下文，避免消息处理时间过长导致阻塞
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			log.Printf("received like event: %s", msg.Body)
			var event LikeEvent
			// 解析消息体为LikeEvent结构体s
			if err := json.Unmarshal(msg.Body, &event); err != nil {
				log.Println("parse like event failed:", err)
				_ = msg.Ack(false) // 解析失败的消息直接ACK掉，避免无限重试
				continue
			}

			// 引入Redis缓存消息ID，先检查是否已经处理过，没有的话再处理，处理完后将消息ID存入Redis，设置过期时间，避免重复消费
			key := fmt.Sprintf("mq_like_event_id:%s", event.EventID)
			val, err := cache.Get(ctx, key)
			if err != nil {
				log.Printf("redis get error: %v", err)
				_ = msg.Nack(false, true) // Redis异常，放回队列重试
				continue
			}
			if val != "" {
				// 如果Redis中存在该消息ID，说明已经处理过，直接ACK掉消息
				log.Printf("like event already processed: %s", event.EventID)
				_ = msg.Ack(false)
				continue
			}

			// 根据事件类型调用相应的repo方法处理点赞或取消点赞
			var consumeErr error
			switch event.Action {
			case "like":
				consumeErr = likeRepo.CreateLike(ctx, event.UserID, event.VideoID)
			case "unlike":
				consumeErr = likeRepo.DeleteLike(ctx, event.UserID, event.VideoID)
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

					// 在这里尝试写入redis，即使第一次成功处理后redis写入失败，也可以在这里再次尝试写入，
					// 避免后续所有重复消费全部缓存miss进入数据库
					err = cache.Set(ctx, key, "1", 24*time.Hour) // 过期时间为24小时
					if err != nil {
						// 再次写入失败直接跳过，避免陷入死循环，后续重复消费会再次尝试写入redis
						log.Printf("redis set error: %v", err)
					}

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

			// 成功处理后，将消息ID存入Redis，设置过期时间，避免重复消费
			err = cache.Set(ctx, key, "1", 24*time.Hour) // 过期时间为24小时
			if err != nil {
				log.Printf("redis set error: %v", err)
				_ = msg.Ack(false) // Redis异常，但是数据库操作已经成功，仍然ACK掉消息，避免重复消费
				continue
			}

			// 根据事件类型更新Redis缓存，便于快速查询用户是否点赞过某个视频
			key = fmt.Sprintf("user_liked_videos:%d", event.UserID)
			switch event.Action {
			case "like":
				{
					// 将 <uid, vid_set> 存入redis，便于快捷查询是否点赞，永不过期
					err = cache.AddToSet(ctx, key, event.VideoID, 0) // 0表示永不过期
					if err != nil {
						// 此操作与MQ消费的幂等性无关，失败了也不影响数据库操作成功，所以直接打印日志并继续
						log.Printf("redis add to set error: %v", err)
						continue
					}
				}
			case "unlike":
				{
					// 将当前vid从 <uid, vid_set> 中删除
					err = cache.RemoveFromSet(ctx, key, event.VideoID)
					if err != nil {
						log.Printf("redis remove from set error: %v", err)
						continue
					}
				}
			default:
				log.Printf("unknown like event action: %s", event.Action)
				continue
			}

			_ = msg.Ack(false) // 成功处理后 ACK 掉消息
		}
	}()

	log.Println("like consumer running...")
	return nil
}
