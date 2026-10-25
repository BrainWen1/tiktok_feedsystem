package mq

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"feedsystem/internal/infra/cache"
	"feedsystem/internal/repo"
	"feedsystem/internal/utils/logger"

	amqp "github.com/rabbitmq/amqp091-go"
)

// commentConsumerTag 评论消费者标签，用于优雅退出时停止投递
const commentConsumerTag = "comment-consumer"

// StartCommentConsumer 启动评论消费，给worker调用。
// ctx 取消后停止拉取新消息并等待在途消息处理完成；wg 用于通知调用方消费协程已退出。
func StartCommentConsumer(ctx context.Context, cmq *CommentMQ, commentRepo *repo.CommentRepo, redisCache *cache.RedisCache, wg *sync.WaitGroup) error {
	if !cmq.IsEnabled() {
		logger.Warn("CommentMQ disabled, skip comment consumer")
		return ErrMQDisabled // MQ禁用时不启动消费循环
	}
	if cmq.ch == nil {
		return errors.New("comment mq is not initialized")
	}
	if commentRepo == nil {
		return errors.New("comment repo is not initialized")
	}
	if wg == nil {
		return errors.New("wait group is not initialized")
	}

	// 声明死信队列和绑定
	ch := cmq.ch
	if err := DeclareDLX(ch, commentQueue); err != nil {
		return err
	}

	// 开始消费评论事件
	msgs, err := ch.Consume(commentQueue, commentConsumerTag, false, false, false, false, nil)
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
			if err := ch.Cancel(commentConsumerTag, false); err != nil {
				logger.Error("cancel comment consumer failed", logger.Err(err))
			}
		}()

		for {
			select {
			case <-consumerCtx.Done():
				logger.Info("comment consumer stopped")
				return
			case msg, ok := <-msgs:
				if !ok {
					logger.Warn("comment consumer delivery channel closed")
					return
				}
				handleCommentMessage(consumerCtx, ch, msg, commentRepo, redisCache)
			}
		}
	}()

	logger.Info("comment consumer running")
	return nil
}

// handleCommentMessage 处理单条评论事件。
// 每条消息使用独立的 2s 超时上下文；WithoutCancel 保证优雅关闭时不中断在途处理。
func handleCommentMessage(parent context.Context, ch *amqp.Channel, msg amqp.Delivery, commentRepo *repo.CommentRepo, redisCache *cache.RedisCache) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 2*time.Second)
	defer cancel()

	logger.Debug("received comment event", logger.String("body", string(msg.Body)))
	var event CommentEvent
	// 解析消息体为CommentEvent结构体
	if err := json.Unmarshal(msg.Body, &event); err != nil {
		logger.Error("parse comment event failed", logger.Err(err))
		_ = msg.Ack(false) // 解析失败的消息直接ACK掉，避免无限重试
		return
	}

	// 用 Redis 缓存消息ID做幂等：先检查是否已处理，未处理才落库，处理完成后写入消息ID并设置过期时间
	key := cache.CommentEventIDKey(event.EventID)
	val, err := redisCache.Get(ctx, key)
	if err != nil && !errors.Is(err, cache.ErrCacheMiss) {
		logger.Error("redis get error", logger.Err(err))
		RetryOrDeadLetter(ch, msg, err) // Redis异常，退避重试，超过上限转死信
		return
	}
	if val != "" {
		// 如果Redis中存在该消息ID，说明已经处理过，直接ACK掉消息
		logger.Debug("comment event already processed", logger.String("event_id", event.EventID))
		_ = msg.Ack(false)
		return
	}

	// 根据事件类型调用相应的repo方法处理评论
	var consumeErr error
	switch event.Action {
	case "create":
		consumeErr = commentRepo.CreateComment(ctx, event.Comment)
	case "delete":
		consumeErr = commentRepo.DeleteComment(ctx, event.Comment.ID)
	default:
		logger.Warn("unknown comment event action", logger.String("action", event.Action))
		_ = msg.Ack(false) // 未知的事件类型直接ACK掉，避免无限重试
		return
	}

	if consumeErr != nil {
		// 如果是数据库唯一键冲突（Duplicate entry / UNIQUE），说明这条评论记录已存在，
		// 这属于幂等场景，可以安全忽略并 ACK 掉消息，避免无意义重试。
		// 不同数据库/驱动返回的错误字符串可能不同，这里做宽松字符串匹配。
		errStr := consumeErr.Error()
		if strings.Contains(errStr, "Duplicate entry") ||
			strings.Contains(errStr, "duplicate key") ||
			strings.Contains(errStr, "UNIQUE") {
			logger.Debug("comment already exists, treat as success", logger.Uint("user_id", event.Comment.UserID), logger.Uint("video_id", event.Comment.VideoID), logger.Err(consumeErr))
			_ = msg.Ack(false)

			// 在这里尝试写入redis，即使第一次成功处理后redis写入失败，也可以在这里再次尝试写入，
			// 避免后续所有重复消费全部缓存miss进入数据库
			err = redisCache.Set(ctx, key, "1", cache.EventIDTTL) // 过期时间为24小时
			if err != nil {
				// 再次写入失败直接跳过，避免陷入死循环，后续重复消费会再次尝试写入redis
				logger.Error("redis set error", logger.Err(err))
			}
			// 重复消费说明该评论已在库中，按数据库真实计数回写，修复首次消费可能漏写缓存的问题
			refreshCommentCountCache(ctx, redisCache, commentRepo, event.Comment.VideoID)

			return
		}

		// 永久错误：视频不存在或用户不存在，直接丢弃消息到死信队列
		if strings.Contains(errStr, "video not found") || strings.Contains(errStr, "user not exist") {
			logger.Error("permanent error, drop to DLX", logger.Err(consumeErr))
			_ = msg.Nack(false, false) //不再放回原队列，进入死信队列
			return
		}
		// 临时故障：数据库抖动、锁超时，退避重试，超过上限转死信
		logger.Warn("consume comment event failed, retry", logger.String("action", event.Action), logger.Err(consumeErr))
		RetryOrDeadLetter(ch, msg, consumeErr)

		return
	}

	// 成功处理后，将消息ID存入Redis，设置过期时间，避免重复消费
	if err := redisCache.Set(ctx, key, "1", cache.EventIDTTL); err != nil {
		// Redis异常，但数据库操作已经成功；继续回写计数缓存，最后仍 ACK 避免重复消费
		logger.Error("redis set event id error", logger.Err(err))
	}

	// 评论新增/删除已落库，按数据库真实计数回写视频评论计数器。
	// key 与 feed 读取端共用 cache.CommentVideoCntKey，保证 feeds.comments_cnt 能读到真实计数。
	refreshCommentCountCache(ctx, redisCache, commentRepo, event.Comment.VideoID)

	_ = msg.Ack(false) // 成功处理后 ACK 掉消息
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
		logger.Error("count comments for video failed", logger.Uint("video_id", vid), logger.Err(err))
		return
	}
	if err := redisCache.Set(ctx, cache.CommentVideoCntKey(vid), cnt, cache.CommentVideoCntTTL); err != nil {
		logger.Error("set comment count cache for video failed", logger.Uint("video_id", vid), logger.Err(err))
	}
}
