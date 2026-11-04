package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"feedsystem/internal/config"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/infra/database"
	"feedsystem/internal/infra/mq"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"feedsystem/internal/utils/logger"

	"github.com/joho/godotenv"
)

// shutdownTimeout 等待在途消费任务完成的最长时间
const shutdownTimeout = 5 * time.Second

func main() {
	// 先加载 .env（本地开发），让 APP_ENV/LOG_LEVEL 在初始化 logger 前可见
	if err := godotenv.Load(); err != nil {
		logger.Info(".env not found; continuing")
	}

	// 日志必须在启动消费者 goroutine 之前初始化，避免启动期日志丢失。
	// 与 API 服务保持一致：dev=文本日志，prod=JSON 日志 + Gin release 模式。
	if err := logger.InitWithEnv(os.Getenv("APP_ENV"), os.Getenv("LOG_LEVEL")); err != nil {
		logger.Warn("invalid logger config, fallback to default", logger.Err(err))
	}
	defer logger.Sync()

	// run 返回后再 Fatal，保证 run 内注册的 defer（关闭 DB/Redis/MQ）一定执行
	if err := run(); err != nil {
		logger.Fatal("worker exit", logger.Err(err))
	}
}

func run() error {
	// 加载配置
	if err := config.LoadConfig(); err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// 使用配置连接数据库服务
	db, err := database.ConnectDB(config.AppConfig.DB_dsn)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}

	database.AutoMigrate(db,
		&model.User{},
		&model.Video{},
		&model.Like{},
		&model.Comment{},
		&model.Social{},
		&model.VideoTag{},
		&model.Notification{},
	) // 自动迁移数据库表结构

	defer database.CloseDB() // 注册关闭数据库连接的延迟调用

	// 初始化MQ
	rmq, err := mq.NewRabbitMQ()
	if err != nil {
		return fmt.Errorf("initialize RabbitMQ: %w", err)
	}
	defer rmq.Close() // 退出时释放MQ连接

	likeMQ, err := mq.NewLikeMQ(rmq)
	if err != nil {
		return fmt.Errorf("initialize LikeMQ: %w", err)
	}
	commentMQ, err := mq.NewCommentMQ(rmq)
	if err != nil {
		return fmt.Errorf("initialize CommentMQ: %w", err)
	}

	// 初始化仓库
	likeRepo := repo.NewLikeRepo(db)
	commentRepo := repo.NewCommentRepo(db)
	videoRepo := repo.NewVideoRepo(db)               // 用户视频仓库，用于获取视频作者ID
	notificationRepo := repo.NewNotificationRepo(db) // 通知仓库，用于创建通知记录

	// 初始化Redis缓存
	redisCache := cache.NewRedisCache(config.AppConfig.Redis_addr, config.AppConfig.Redis_password, config.AppConfig.Redis_db)
	defer redisCache.Close() // 退出时释放Redis连接

	// worker 强依赖 Redis 做消费幂等与缓存同步，启动前必须校验连通性，
	// 否则消费者会在 Redis 故障期间反复重试，无法正常消费。
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 3*time.Second)
	err = redisCache.Ping(pingCtx)
	pingCancel()
	if err != nil {
		return fmt.Errorf("redis unavailable: %w", err)
	}
	logger.Info("redis connected")

	// 信号监听：SIGINT/SIGTERM 触发优雅退出
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 等待点赞、评论消费协程退出
	var wg sync.WaitGroup

	// 启动点赞消费
	if err := mq.StartLikeConsumer(ctx, likeMQ, likeRepo, redisCache, videoRepo, notificationRepo, &wg); err != nil {
		return fmt.Errorf("start like consumer: %w", err)
	}

	// 启动评论消费
	if err := mq.StartCommentConsumer(ctx, commentMQ, commentRepo, redisCache, &wg); err != nil {
		return fmt.Errorf("start comment consumer: %w", err)
	}

	logger.Info("worker is running for like and comment events")
	<-ctx.Done()
	logger.Info("shutdown signal received, stopping consumers")

	// 等待在途消息处理完成，超时后强制退出
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		logger.Info("consumers stopped gracefully")
	case <-time.After(shutdownTimeout):
		logger.Warn("consumer shutdown timed out")
	}
	// run 返回后按 LIFO 顺序执行 defer：关闭 MQ -> Redis -> DB
	return nil
}
