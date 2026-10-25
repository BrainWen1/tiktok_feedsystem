package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"feedsystem/internal/config"
	"feedsystem/internal/handler/middleware"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/infra/database"
	"feedsystem/internal/infra/mq"
	"feedsystem/internal/model"
	"feedsystem/internal/router"

	"github.com/joho/godotenv"
)

const (
	serverAddr      = ":8080"
	shutdownTimeout = 10 * time.Second
)

func main() {
	// 加载 .env（本地开发）
	if err := godotenv.Load(); err != nil {
		log.Println(".env not found; continuing")
	}

	// 加载配置
	err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 使用配置连接数据库、Redis等服务
	db, err := database.ConnectDB(config.AppConfig.DB_dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	database.AutoMigrate(db, &model.User{}, &model.Video{}, &model.Like{}, &model.Comment{}, &model.Social{}, &model.VideoTag{}) // 自动迁移数据库表结构

	defer database.CloseDB() // 注册关闭数据库连接的延迟调用

	// Redis
	redisCache := cache.NewRedisCache(config.AppConfig.Redis_addr, config.AppConfig.Redis_password, config.AppConfig.Redis_db)
	defer redisCache.Close() // 注册关闭Redis连接的延迟调用

	pingCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond) // 设置超时时间为300毫秒
	defer cancel()                                                                     // 确保在函数退出时取消上下文，以释放资源

	if err := redisCache.Ping(pingCtx); err != nil { // 检查Redis连接是否正常
		// Redis不可用时降级运行：禁用缓存，业务走纯数据库，不阻断服务启动
		log.Printf("ERROR: Redis not available, cache disabled, fallback to database: %v", err)
		redisCache.Disable()
	} else {
		log.Printf("Redis connected (cache enabled)")
	}

	// RabbitMQ
	rmq, err := mq.NewRabbitMQ()
	if err != nil {
		// MQ不可用时降级运行：NewRabbitMQ已返回禁用的合法实例，投递由service层回退数据库
		log.Printf("WARN: RabbitMQ unavailable, mq disabled, service continues: %v", err)
	} else {
		log.Printf("RabbitMQ connected")
	}
	defer rmq.Close() // 禁用实例的Close为空操作，统一延迟关闭

	// 初始化中间件
	authMiddleware := middleware.NewAuthMiddleware(redisCache)

	// 设置路由
	r := router.SetupRouter(db, redisCache, rmq, authMiddleware)

	// 显式配置 http.Server 超时，避免慢连接耗尽连接资源
	srv := &http.Server{
		Addr:              serverAddr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,   // 读取请求头上限
		ReadTimeout:       120 * time.Second, // 覆盖大文件上传的请求体读取
		WriteTimeout:      120 * time.Second, // 覆盖大文件上传的响应写入
		IdleTimeout:       60 * time.Second,  // keep-alive 空闲连接上限
	}

	// 信号监听：SIGINT/SIGTERM 触发优雅退出
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("Server is running on %s", serverAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		log.Printf("Server error: %v", err)
	case <-ctx.Done():
		log.Println("Shutdown signal received, shutting down server...")
	}

	// 停止接收新请求，等待在途请求完成；超时后强制关闭
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server graceful shutdown failed: %v", err)
	} else {
		log.Println("Server stopped gracefully")
	}
	// main 返回后按 LIFO 顺序执行 defer：关闭 MQ -> Redis -> DB
}
