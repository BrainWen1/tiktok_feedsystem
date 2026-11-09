package mq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"feedsystem/internal/infra/cache"
	"feedsystem/internal/repo"
	"feedsystem/internal/utils/logger"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
)

// likeConsumerTag 点赞消费者标签，用于优雅退出时停止投递
const likeConsumerTag = "like-consumer"

// NotificationProcessor 定义了处理通知的接口，便于在 like_consumer.go 中解耦通知逻辑
type NotificationProcessor interface {
	ProcessNotification(ctx context.Context, event NotificationEvent) error
}

// StartLikeConsumer 启动点赞消费，给worker调用。
// ctx 取消后停止拉取新消息并等待在途消息处理完成；wg 用于通知调用方消费协程已退出。
func StartLikeConsumer(ctx context.Context, lmq *LikeMQ, likeRepo *repo.LikeRepo, redisCache *cache.RedisCache, notificationProcessor NotificationProcessor, wg *sync.WaitGroup) error {
	if !lmq.IsEnabled() {
		logger.Warn("LikeMQ disabled, skip like consumer")
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
	if wg == nil {
		return errors.New("wait group is not initialized")
	}

	// 声明死信队列和绑定
	ch := lmq.ch
	if err := DeclareDLX(ch, likeQueue); err != nil {
		return err
	}

	// 开始消费点赞事件
	msgs, err := ch.Consume(likeQueue, likeConsumerTag, false, false, false, false, nil)
	if err != nil {
		return err
	}

	// 使用goroutine处理消息
	wg.Add(1)
	go func() {
		defer wg.Done()

		// 消费者级上下文：与消费循环同生命周期，goroutine 退出时统一释放
		consumerCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		// 优雅退出时停止投递，避免关闭阶段继续拉取新消息
		defer func() {
			if err := ch.Cancel(likeConsumerTag, false); err != nil {
				logger.Error("cancel like consumer failed", logger.Err(err))
			}
		}()

		for {
			select {
			case <-consumerCtx.Done():
				logger.Info("like consumer stopped")
				return
			case msg, ok := <-msgs:
				if !ok {
					logger.Warn("like consumer delivery channel closed")
					return
				}
				handleLikeMessage(consumerCtx, ch, msg, likeRepo, redisCache, notificationProcessor)
			}
		}
	}()

	logger.Info("like consumer running")
	return nil
}

// handleLikeMessage 处理单条点赞事件。
// 每条消息使用独立的超时上下文；WithoutCancel 保证优雅关闭时不中断在途处理。
func handleLikeMessage(parent context.Context, ch *amqp.Channel, msg amqp.Delivery, likeRepo *repo.LikeRepo, redisCache *cache.RedisCache, notificationProcessor NotificationProcessor) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), messageHandleTimeout)
	defer cancel()

	logger.Debug("received like event", logger.String("body", string(msg.Body)))
	var event LikeEvent
	// 解析消息体为 LikeEvent 结构体
	if err := json.Unmarshal(msg.Body, &event); err != nil {
		logger.Error("parse like event failed", logger.Err(err))
		_ = msg.Ack(false) // 解析失败的消息直接ACK掉，避免无限重试
		return
	}

	// 用 Redis 缓存消息ID做幂等：先检查是否已处理，未处理才落库，处理完成后写入消息ID并设置过期时间
	key := cache.LikeEventIDKey(event.EventID)
	val, err := redisCache.Get(ctx, key)
	if err != nil && !errors.Is(err, cache.ErrCacheMiss) {
		logger.Error("redis get error", logger.Err(err))
		RetryOrDeadLetter(ch, msg, err) // Redis异常，退避重试，超过上限转死信
		return
	}
	if val != "" {
		// 如果Redis中存在该消息ID，说明已经处理过，直接ACK掉消息
		logger.Debug("like event already processed", logger.String("event_id", event.EventID))
		_ = msg.Ack(false)
		return
	}

	// 根据事件类型调用相应的repo方法处理点赞或取消点赞。
	// 点赞行与 videos.likes_count 在 repo 事务内同步更新，保证视频表计数与点赞表一致。
	var (
		consumeErr error
		removed    int64
	)
	switch event.Action {
	case "like":
		consumeErr = likeRepo.CreateLike(ctx, event.UserID, event.VideoID)
	case "unlike":
		removed, consumeErr = likeRepo.DeleteLike(ctx, event.UserID, event.VideoID)
	default:
		logger.Warn("unknown like event action", logger.String("action", event.Action))
		_ = msg.Ack(false) // 未知的事件类型直接ACK掉，避免无限重试
		return
	}

	if consumeErr != nil {
		// 如果是数据库唯一键冲突（Duplicate entry / UNIQUE），说明这条点赞记录已存在，
		// 这属于幂等场景，可以安全忽略并 ACK 掉消息，避免无意义重试。
		// 不同数据库/驱动返回的错误字符串可能不同，这里做宽松字符串匹配。
		errStr := consumeErr.Error()
		if strings.Contains(errStr, "Duplicate entry") ||
			strings.Contains(errStr, "duplicate key") ||
			strings.Contains(errStr, "UNIQUE") {
			logger.Debug("like already exists, treat as success", logger.Uint("user_id", event.UserID), logger.Uint("video_id", event.VideoID), logger.Err(consumeErr))
			_ = msg.Ack(false)

			// 在这里尝试写入redis，即使第一次成功处理后redis写入失败，也可以在这里再次尝试写入，
			// 避免后续所有重复消费全部缓存miss进入数据库
			err = redisCache.Set(ctx, key, "1", cache.EventIDTTL) // 过期时间为24小时
			if err != nil {
				// 再次写入失败直接跳过，避免陷入死循环，后续重复消费会再次尝试写入redis
				logger.Error("redis set error", logger.Err(err))
			}
			// 重复消费说明该点赞已在库中，幂等重放一次缓存同步，修复首次消费可能漏写计数的问题
			if err := SyncLikeCaches(ctx, redisCache, likeRepo, event); err != nil {
				logger.Error("sync like cache on duplicate failed", logger.Err(err))
			}
			// 首次消费可能已插入点赞行但未完成计数更新（进程退出等），以点赞表为准重算一次
			if err := likeRepo.SyncLikesCount(ctx, event.VideoID); err != nil {
				logger.Error("sync likes count on duplicate failed", logger.Uint("video_id", event.VideoID), logger.Err(err))
			}
			// 点赞数已重算，同步刷新热度值，保证热度榜 (popularity, id) 排序数据正确
			if err := likeRepo.SyncPopularity(ctx, event.VideoID); err != nil {
				logger.Error("sync video popularity on duplicate failed", logger.Uint("video_id", event.VideoID), logger.Err(err))
			}
			// 点赞数已变化，删除详情缓存，避免详情接口在 TTL 内继续返回旧计数
			invalidateVideoDetailCache(ctx, redisCache, event.VideoID)

			return
		}

		// 永久错误：视频不存在或用户不存在，直接丢弃消息到死信队列
		if strings.Contains(errStr, "video not found") || strings.Contains(errStr, "user not exist") {
			logger.Error("permanent error, drop to DLX", logger.Err(consumeErr))
			_ = msg.Nack(false, false) //不再放回原队列，进入死信队列
			return
		}
		// 临时故障：数据库抖动、锁超时，退避重试，超过上限转死信
		logger.Warn("consume like event failed, retry", logger.String("action", event.Action), logger.Err(consumeErr))
		RetryOrDeadLetter(ch, msg, consumeErr)

		return
	}

	// 成功处理后，将消息ID存入Redis，设置过期时间，避免重复消费
	if err := redisCache.Set(ctx, key, "1", cache.EventIDTTL); err != nil {
		// Redis异常，但数据库操作已经成功；继续同步缓存视图，最后仍 ACK 避免重复消费
		logger.Error("redis set event id error", logger.Err(err))
	}

	// 同步缓存视图：用户点赞集合（Set）、视频点赞明细 ZSet、视频点赞热度榜单 ZSet。
	// 缓存失败不影响数据库已成功的结论，只记录日志，避免消息因缓存问题堆积。
	if err := SyncLikeCaches(ctx, redisCache, likeRepo, event); err != nil {
		logger.Error("sync like cache failed", logger.Err(err))
	}

	// 取消点赞未命中任何行（重复消息或点赞行已不存在）时，点赞数不会被事务递减，
	// 这里以点赞表为准重算一次，避免计数长期偏移。
	if event.Action == "unlike" && removed == 0 {
		if err := likeRepo.SyncLikesCount(ctx, event.VideoID); err != nil {
			logger.Error("sync likes count on repeated unlike failed", logger.Uint("video_id", event.VideoID), logger.Err(err))
		}
	}

	// 点赞/取消点赞已在事务内更新 videos.likes_count，这里同步刷新热度值，
	// 让 /feed/listByPopularity 有真实排序数据；失败只记录日志，不影响消息 ACK。
	if err := likeRepo.SyncPopularity(ctx, event.VideoID); err != nil {
		logger.Error("sync video popularity failed", logger.Uint("video_id", event.VideoID), logger.Err(err))
	}

	// 点赞数已变化，删除详情缓存，避免详情接口在 10 分钟 TTL 内继续返回旧计数
	invalidateVideoDetailCache(ctx, redisCache, event.VideoID)

	// 在这里触发异步通知逻辑
	if event.Action == "like" {
		// 建议用 go 开个协程异步去发通知
		// 不要因为通知逻辑报错影响原本的 ACK
		go func() {
			// 这里使用 WithoutCancel 保证通知逻辑不会被即将关闭的消费上下文中断，避免通知逻辑被取消。
			nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()

			// err 使用 := 而不是 =，避免覆盖外层变量，不会产生竞态
			err := notificationProcessor.ProcessNotification(nctx, NotificationEvent{
				EventID:  event.EventID,
				Action:   event.Action,
				ActorID:  event.UserID,
				UserName: event.UserName,
				VideoID:  event.VideoID,
			})
			if err != nil {
				logger.Ctx(nctx).Error("process notification failed", logger.Err(err))
			}
		}()
	}

	_ = msg.Ack(false) // 成功处理后 ACK 掉消息
}

// invalidateVideoDetailCache 删除视频详情缓存。
// 详情缓存里保存了点赞数，点赞/取消点赞落库后必须失效，否则详情接口会在 TTL 内返回旧值。
func invalidateVideoDetailCache(ctx context.Context, redisCache *cache.RedisCache, vid uint) {
	if err := redisCache.Delete(ctx, cache.VideoDetailKey(vid)); err != nil {
		logger.Error("invalidate video detail cache failed", logger.Uint("video_id", vid), logger.Err(err))
	}
}

// SyncLikeCaches 把数据库已落库的点赞状态同步到 Redis：
//  1. like:user:{userID}：用户点赞视频集合（Set）
//  2. like:video:{vid}：视频点赞明细 ZSet，member=userID，ZCard 即点赞总数
//  3. video:videos：视频点赞热度榜单 ZSet，member=vid，score=当前点赞总数
//
// 导出给 service 层 MQ 降级直写数据库的分支复用，保证两条写入路径产出同一套缓存结构（P0-5）。
func SyncLikeCaches(ctx context.Context, redisCache *cache.RedisCache, likeRepo *repo.LikeRepo, event LikeEvent) error {
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
		for _, userID := range userIDs {
			zList = append(zList, redis.Z{Score: now, Member: userID})
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
