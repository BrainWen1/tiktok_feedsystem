package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"feedsystem/internal/config"
	"feedsystem/internal/handler/middleware"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/infra/database"
	"feedsystem/internal/infra/mq"
	infra "feedsystem/internal/infra/sse"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"feedsystem/internal/router"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/file"
	"feedsystem/internal/utils/logger"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

const (
	serverAddr      = ":8080"
	shutdownTimeout = 10 * time.Second
)

func main() {
	// 先加载 .env（本地开发），让 APP_ENV/LOG_LEVEL 在初始化 logger 前可见
	if err := godotenv.Load(); err != nil {
		logger.Info(".env not found; continuing")
	}

	// 加载配置：config.LoadConfig 会读取 configs/.env.dev，把 APP_ENV/LOG_LEVEL 注入进程环境。
	// 必须放在日志初始化之前，否则 logger 读到空值，prod 环境下无法切换到 JSON 格式。
	err := config.LoadConfig()
	if err != nil {
		logger.Fatal("failed to load config", logger.Err(err))
	}

	// 日志在配置加载之后初始化，才能读到正确的 APP_ENV/LOG_LEVEL：
	// prod => JSON 结构化日志，dev/其他 => 文本日志；LOG_LEVEL 可覆盖环境默认级别。
	// 仍需在启动任何 goroutine 之前完成，避免启动期日志丢失。
	if err := logger.InitWithEnv(os.Getenv("APP_ENV"), os.Getenv("LOG_LEVEL")); err != nil {
		logger.Warn("invalid logger config, fallback to default", logger.Err(err))
	}
	defer logger.Sync()

	// 初始化 StorageRoot/RuntimeRoot 目录结构，并清理上次进程残留的一次性临时文件。
	// 必须在任何上传/日志写入前完成，确保目录权限为 0755。
	if err := file.EnsureStorageDirs(); err != nil {
		logger.Fatal("failed to initialize storage directories", logger.Err(err))
	}
	file.CleanupTempDir()

	// Gin 模式必须在 gin.Default() 之前确定，而 gin.Default() 由 router.SetupRouter 内部调用。
	// 配置加载后 APP_ENV 已可用，这里据此设置：prod => release（关闭 [GIN-debug] 路由打印），
	// 其余（dev/未配置）=> debug。不引入 GIN_MODE，统一由 APP_ENV 管控环境。
	switch strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))) {
	case "prod", "production":
		gin.SetMode(gin.ReleaseMode)
	default:
		gin.SetMode(gin.DebugMode)
	}

	// 使用配置连接数据库、Redis等服务
	db, err := database.ConnectDB(config.AppConfig.DB_dsn)
	if err != nil {
		logger.Fatal("failed to connect to database", logger.Err(err))
	}

	database.AutoMigrate(db,
		&model.User{},
		&model.Video{},
		&model.Like{},
		&model.Comment{},
		&model.Social{},
		&model.VideoTag{},
		&model.Notification{},
		&model.Message{},
	) // 自动迁移数据库表结构

	defer database.CloseDB() // 注册关闭数据库连接的延迟调用

	// Redis
	redisCache := cache.NewRedisCache(config.AppConfig.Redis_addr, config.AppConfig.Redis_password, config.AppConfig.Redis_db)
	defer redisCache.Close() // 注册关闭Redis连接的延迟调用

	pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second) // 设置超时时间为3秒
	defer cancel()                                                              // 确保在函数退出时取消上下文，以释放资源

	if err := redisCache.Ping(pingCtx); err != nil { // 检查Redis连接是否正常
		// Redis不可用时降级运行：禁用缓存，业务走纯数据库，不阻断服务启动
		logger.Error("redis not available, cache disabled, fallback to database", logger.Err(err))
		redisCache.Disable()
	} else {
		logger.Info("redis connected", logger.Bool("cache_enabled", true))
	}

	// RabbitMQ
	rmq, err := mq.ConnectRabbitMQWithRetry(6) // 尝试连接 RabbitMQ，失败时按指数退避重试，最多重试5次（2s, 4s, 8s，10s，10s，10s）
	if err != nil {
		// MQ不可用时降级运行：NewRabbitMQ已返回禁用的合法实例，投递由service层回退数据库
		logger.Warn("rabbitmq remained unavailable after waiting for 6 times retry", logger.Err(err))
	} else {
		logger.Info("rabbitmq connected")
	}
	defer rmq.Close() // 禁用实例的Close为空操作，统一延迟关闭

	// 初始化中间件
	authMiddleware := middleware.NewAuthMiddleware(redisCache)

	// 信号监听：SIGINT/SIGTERM 触发优雅退出；同时作为后台任务的生命周期控制
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 分片上传垃圾回收：定期删除超过 24h 未完成合并的 chunks/{upload_id}，
	// 并同步清理 Redis 会话。多实例同时运行也安全（删除操作幂等）。
	chunkService := service.NewChunkService(repo.NewChunkRepo(redisCache))
	go chunkService.RunGarbageCollector(ctx, time.Hour)

	// 启用 SSE 订阅器，监听 Redis Pub/Sub 的 SSE 推送频道
	hub := infra.NewHub()
	hub.StartSSESubscriber(ctx, redisCache)

	// 设置路由
	r := router.SetupRouter(db, redisCache, rmq, authMiddleware, hub)

	// 显式配置 http.Server 超时，避免慢连接耗尽连接资源
	srv := &http.Server{
		Addr:              serverAddr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,   // 读取请求头上限
		ReadTimeout:       120 * time.Second, // 覆盖大文件上传的请求体读取
		WriteTimeout:      120 * time.Second, // 恢复120秒，保护普通请求，避免慢客户端拖垮服务
		IdleTimeout:       60 * time.Second,  // keep-alive 空闲连接上限
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("server is running", logger.String("addr", serverAddr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		logger.Error("server error", logger.Err(err))
	case <-ctx.Done():
		logger.Info("shutdown signal received, shutting down server")
	}

	// 停止接收新请求，等待在途请求完成；超时后强制关闭
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server graceful shutdown failed", logger.Err(err))
	} else {
		logger.Info("server stopped gracefully")
	}
	// main 返回后按 LIFO 顺序执行 defer：关闭 MQ -> Redis -> DB
}
