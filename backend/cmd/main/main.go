package main

import (
	"context"
	"feedsystem/internal/config"
	"feedsystem/internal/handler/middleware"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/infra/database"
	"feedsystem/internal/infra/mq"
	"feedsystem/internal/model"
	"feedsystem/internal/router"
	"log"
	"time"

	"github.com/joho/godotenv"
)

// StartRefreshCleanTask 启动定时清扫，单独goroutine常驻运行
func StartRefreshCleanTask(ctx context.Context, redisCache *cache.RedisCache) {
	go func() {
		const interval = 4 * time.Hour //每4小时自动清理一次，可调整
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		log.Printf("refresh token stale cleaner started, interval=%v\n", interval)
		for {
			select {
			case <-ctx.Done(): // 监听上下文取消信号，优雅退出
				log.Println("refresh cleaner stopped")
				return
			case <-ticker.C: // 定时触发清理任务
				log.Println("refresh token stale cleaner triggered")
				//捕获panic，防止任务崩溃导致整个goroutine死掉不再执行
				func() {
					defer func() {
						if err := recover(); err != nil {
							log.Printf("clean task panic recovered: %v", err)
						}
					}()
					cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()

					// 调用Redis缓存的清理方法
					err := redisCache.CleanStaleRefreshSetMembers(cleanCtx)
					if err != nil {
						log.Printf("clean stale refresh failed: %v", err)
					} else {
						log.Println("stale refresh token clean success")
					}
				}()
			}
		}
	}()
}

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

	database.AutoMigrate(db, &model.User{}, &model.Video{}) // 自动迁移数据库表结构

	defer database.CloseDB() // 注册关闭数据库连接的延迟调用

	// Redis
	cache := cache.NewRedisCache(config.AppConfig.Redis_addr, config.AppConfig.Redis_password, config.AppConfig.Redis_db)

	pingCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond) // 设置超时时间为300毫秒
	defer cancel()                                                                     // 确保在函数退出时取消上下文，以释放资源

	if err := cache.Ping(pingCtx); err != nil { // 检查Redis连接是否正常
		log.Printf("Redis not available (cache disabled): %v", err)
		_ = cache.Close()
		cache = nil
		return
	} else {
		defer cache.Close() // 注册关闭Redis连接的延迟调用
		log.Printf("Redis connected (cache enabled)")
	}

	// RabbitMQ
	rmq, err := mq.NewRabbitMQ()
	if err != nil {
		log.Printf("RabbitMQ config error (disabled): %v", err)
		rmq = nil
	} else {
		defer rmq.Close()
		log.Printf("RabbitMQ connected")
	}

	// 初始化中间件
	authMiddleware := middleware.NewAuthMiddleware(cache)

	// 设置路由
	r := router.SetupRouter(db, cache, authMiddleware)

	// 设置定时异步的redis清理任务
	globalCtx, stop := context.WithCancel(context.Background()) // 创建一个可取消的上下文，用于控制清理任务的生命周期
	defer stop()
	StartRefreshCleanTask(globalCtx, cache)

	// 启动服务器
	if err := r.Run(":8080"); err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
	log.Println("Server is running on port 8080")
}
