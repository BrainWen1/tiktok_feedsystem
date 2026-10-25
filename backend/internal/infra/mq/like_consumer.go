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

	"github.com/redis/go-redis/v9"
)

// StartLikeConsumer 启动点赞消费，给worker调用
func StartLikeConsumer(lmq *LikeMQ, likeRepo *repo.LikeRepo, redisCache *cache.RedisCache) error {
	if !lmq.IsEnabled() {
		log.Printf("WARN: LikeMQ disabled, skip like consumer")
		return ErrMQDisabled // MQ禁用时不启动消费循环
	}
	if lmq.ch == nil {
		return errors.New("like mq is not initialized")
	}
	if redisCache == nil {
		return errors.New("like cache is not initialized")
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
			key := cache.LikeEventIDKey(event.EventID)
			val, err := redisCache.Get(ctx, key)
			if err != nil && err.Error() != "redis: nil" {
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
					err = redisCache.Set(ctx, key, "1", cache.EventIDTTL) // 过期时间为24小时
					if err != nil {
						// 再次写入失败直接跳过，避免陷入死循环，后续重复消费会再次尝试写入redis
						log.Printf("redis set error: %v", err)
					}
					// 重复消费说明该点赞已在库中，幂等重放一次缓存同步，修复首次消费可能漏写计数的问题
					if err := syncLikeCaches(ctx, redisCache, likeRepo, event); err != nil {
						log.Printf("sync like cache on duplicate failed: %v", err)
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
			if err := redisCache.Set(ctx, key, "1", cache.EventIDTTL); err != nil {
				// Redis异常，但数据库操作已经成功；继续同步缓存视图，最后仍 ACK 避免重复消费
				log.Printf("redis set event id error: %v", err)
			}

			// 同步缓存视图：用户点赞集合、视频点赞明细 ZSet、视频点赞热度榜单 ZSet。
			// 缓存失败不影响数据库已成功的结论，只记录日志，避免消息因缓存问题堆积。
			if err := syncLikeCaches(ctx, redisCache, likeRepo, event); err != nil {
				log.Printf("sync like cache failed: %v", err)
			}

			_ = msg.Ack(false) // 成功处理后 ACK 掉消息
		}
	}()

	log.Println("like consumer running...")
	return nil
}

// syncLikeCaches 把数据库已落库的点赞状态同步到 Redis：
//  1. like:user:{uid}：用户点赞视频集合（Set）
//  2. like:video:{vid}：视频点赞明细 ZSet，member=uid，ZCard 即点赞总数
//  3. video:videos：视频点赞热度榜单 ZSet，member=vid，score=当前点赞总数
func syncLikeCaches(ctx context.Context, redisCache *cache.RedisCache, likeRepo *repo.LikeRepo, event LikeEvent) error {
	if redisCache == nil || !redisCache.IsEnabled() {
		return nil // 缓存禁用时保持降级，全部以数据库为准
	}

	// 用户维度点赞集合，结构与 service 查询端保持一致（P0-8/P0-9）
	userKey := cache.LikeUserSetKey(event.UserID)
	switch event.Action {
	case "like":
		if err := redisCache.AddToSet(ctx, userKey, event.VideoID, cache.LikeUserSetTTL); err != nil {
			return fmt.Errorf("add video %d to user %d like set: %w", event.VideoID, event.UserID, err)
		}
	case "unlike":
		if err := redisCache.RemoveFromSet(ctx, userKey, event.VideoID); err != nil {
			return fmt.Errorf("remove video %d from user %d like set: %w", event.VideoID, event.UserID, err)
		}
		// 空占位缓存，避免短时间内重复点击取消点赞造成缓存穿透
		placeholderKey := cache.LikePlaceholderKey(event.UserID, event.VideoID)
		if err := redisCache.Set(ctx, placeholderKey, "1", cache.LikePlaceholderTTL); err != nil {
			return fmt.Errorf("set unlike placeholder for user %d video %d: %w", event.UserID, event.VideoID, err)
		}
	default:
		return fmt.Errorf("unknown like action %q", event.Action)
	}

	// 视频维度点赞明细 ZSet
	videoKey := cache.LikeVideoZSetKey(event.VideoID)
	exists, err := redisCache.Exists(ctx, videoKey)
	if err != nil {
		return fmt.Errorf("check video %d like zset: %w", event.VideoID, err)
	}
	if !exists {
		// 首次写入或 TTL 到期：以数据库为准重建完整成员集合，
		// 避免只从当前一条点赞开始计数导致总数偏小。
		userIDs, err := likeRepo.FindUserIDsByVideo(ctx, event.VideoID)
		if err != nil {
			return fmt.Errorf("load like users for video %d: %w", event.VideoID, err)
		}
		zList := make([]redis.Z, 0, len(userIDs))
		now := float64(time.Now().Unix())
		for _, uid := range userIDs {
			zList = append(zList, redis.Z{Score: now, Member: uid})
		}
		if err := redisCache.ZAdd(ctx, videoKey, zList, cache.LikeVideoZSetTTL); err != nil {
			return fmt.Errorf("rebuild video %d like zset: %w", event.VideoID, err)
		}
	} else {
		switch event.Action {
		case "like":
			z := redis.Z{Score: float64(time.Now().Unix()), Member: event.UserID}
			if err := redisCache.ZAdd(ctx, videoKey, []redis.Z{z}, cache.LikeVideoZSetTTL); err != nil {
				return fmt.Errorf("add user %d to video %d like zset: %w", event.UserID, event.VideoID, err)
			}
		case "unlike":
			if err := redisCache.ZRem(ctx, videoKey, event.UserID); err != nil {
				return fmt.Errorf("remove user %d from video %d like zset: %w", event.UserID, event.VideoID, err)
			}
		}
	}

	// 榜单 score 必须等于当前点赞总数
	likeCnt, err := redisCache.ZCard(ctx, videoKey)
	if err != nil {
		return fmt.Errorf("count video %d likes: %w", event.VideoID, err)
	}
	rankKey := cache.VideoRankZSetKey()
	if likeCnt <= 0 {
		return redisCache.ZRem(ctx, rankKey, event.VideoID)
	}
	rankZ := redis.Z{Score: float64(likeCnt), Member: event.VideoID}
	if err := redisCache.ZAdd(ctx, rankKey, []redis.Z{rankZ}, cache.VideoRankZSetTTL); err != nil {
		return fmt.Errorf("update video rank for video %d: %w", event.VideoID, err)
	}
	return nil
}
