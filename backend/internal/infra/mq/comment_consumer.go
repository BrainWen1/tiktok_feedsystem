package mq

import (
	"context"
	"encoding/json"
	"errors"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/repo"
	"log"
	"strings"
	"time"
)

// StartCommentConsumer 启动评论消费，给worker调用
func StartCommentConsumer(cmq *CommentMQ, commentRepo *repo.CommentRepo, redisCache *cache.RedisCache) error {
	if !cmq.IsEnabled() {
		log.Printf("WARN: CommentMQ disabled, skip comment consumer")
		return ErrMQDisabled // MQ禁用时不启动消费循环
	}
	if cmq.ch == nil {
		return errors.New("comment mq is not initialized")
	}
	if commentRepo == nil {
		return errors.New("comment repo is not initialized")
	}

	// 声明死信队列和绑定
	ch := cmq.ch
	if err := DeclareDLX(ch, commentQueue); err != nil {
		return err
	}

	// 开始消费评论事件
	msgs, err := ch.Consume(commentQueue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	// 使用goroutine处理消息
	go func() {
		for msg := range msgs {
			// 设置一个超时上下文，避免消息处理时间过长导致阻塞
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			log.Printf("received comment event: %s", msg.Body)
			var event CommentEvent
			// 解析消息体为CommentEvent结构体
			if err := json.Unmarshal(msg.Body, &event); err != nil {
				log.Println("parse comment event failed:", err)
				_ = msg.Ack(false) // 解析失败的消息直接ACK掉，避免无限重试
				continue
			}

			// Redis缓存消息ID，先检查是否已经处理过，没有的话再处理，处理完后将消息ID存入Redis，设置过期时间，避免重复消费
			key := cache.CommentEventIDKey(event.EventID)
			val, err := redisCache.Get(ctx, key)
			if err != nil && err.Error() != "redis: nil" {
				log.Printf("redis get error: %v", err)
				_ = msg.Nack(false, true) // Redis异常，放回队列重试
				continue
			}
			if val != "" {
				// 如果Redis中存在该消息ID，说明已经处理过，直接ACK掉消息
				log.Printf("comment event already processed: %s", event.EventID)
				_ = msg.Ack(false)
				continue
			}

			// 根据事件类型调用相应的repo方法处理评论
			var consumeErr error
			switch event.Action {
			case "create":
				consumeErr = commentRepo.CreateComment(ctx, event.Comment)
			case "delete":
				consumeErr = commentRepo.DeleteComment(ctx, event.Comment.ID)
			default:
				log.Printf("unknown comment event action: %s", event.Action)
				_ = msg.Ack(false) // 未知的事件类型直接ACK掉，避免无限重试
				continue
			}

			if consumeErr != nil {
				// 如果是数据库唯一键冲突（Duplicate entry / UNIQUE），说明这条评论记录已存在，
				// 这属于幂等场景，可以安全忽略并 ACK 掉消息，避免无意义重试。
				// 不同数据库/驱动返回的错误字符串可能不同，这里做宽松字符串匹配。
				errStr := consumeErr.Error()
				if strings.Contains(errStr, "Duplicate entry") ||
					strings.Contains(errStr, "duplicate key") ||
					strings.Contains(errStr, "UNIQUE") {
					log.Printf("comment already exists, treat as success: user=%d video=%d, err=%v", event.Comment.UserID, event.Comment.VideoID, consumeErr)
					_ = msg.Ack(false)

					// 在这里尝试写入redis，即使第一次成功处理后redis写入失败，也可以在这里再次尝试写入，
					// 避免后续所有重复消费全部缓存miss进入数据库
					err = redisCache.Set(ctx, key, "1", cache.EventIDTTL) // 过期时间为24小时
					if err != nil {
						// 再次写入失败直接跳过，避免陷入死循环，后续重复消费会再次尝试写入redis
						log.Printf("redis set error: %v", err)
					}
					// 重复消费说明该评论已在库中，按数据库真实计数回写，修复首次消费可能漏写缓存的问题
					refreshCommentCountCache(ctx, redisCache, commentRepo, event.Comment.VideoID)

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
			if err := redisCache.Set(ctx, key, "1", cache.EventIDTTL); err != nil {
				// Redis异常，但数据库操作已经成功；继续回写计数缓存，最后仍 ACK 避免重复消费
				log.Printf("redis set event id error: %v", err)
			}

			// 评论新增/删除已落库，按数据库真实计数回写视频评论计数器。
			// key 与 feed 读取端共用 cache.CommentVideoCntKey，保证 feeds.comments_cnt 能读到真实计数。
			refreshCommentCountCache(ctx, redisCache, commentRepo, event.Comment.VideoID)

			_ = msg.Ack(false) // 成功处理后 ACK 掉消息
		}
	}()

	log.Println("comment consumer running...")
	return nil
}

// refreshCommentCountCache 用数据库真实评论数回写 Redis 计数器。
// 相比 INCR/DECR，DB COUNT 可以避免缓存 key 过期后从 0 开始计数产生漂移或负数，
// 也与 feed 读取端共用 cache.CommentVideoCntKey，保证 comments_cnt 与实际一致。
func refreshCommentCountCache(ctx context.Context, redisCache *cache.RedisCache, commentRepo *repo.CommentRepo, vid uint) {
	if redisCache == nil || !redisCache.IsEnabled() {
		return // 缓存禁用时保持降级，读取端直接走数据库
	}
	cnt, err := commentRepo.CountByVideo(ctx, vid)
	if err != nil {
		log.Printf("count comments for video %d failed: %v", vid, err)
		return
	}
	if err := redisCache.Set(ctx, cache.CommentVideoCntKey(vid), cnt, cache.CommentVideoCntTTL); err != nil {
		log.Printf("set comment count cache for video %d failed: %v", vid, err)
	}
}
