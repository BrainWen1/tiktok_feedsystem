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

	database.AutoMigrate(db, &model.User{}, &model.Video{}, &model.Like{}, &model.Comment{}, &model.Social{}) // 自动迁移数据库表结构

	defer database.CloseDB() // 注册关闭数据库连接的延迟调用

	// Redis
	cache := cache.NewRedisCache(config.AppConfig.Redis_addr, config.AppConfig.Redis_password, config.AppConfig.Redis_db)
	defer cache.Close() // 注册关闭Redis连接的延迟调用

	pingCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond) // 设置超时时间为300毫秒
	defer cancel()                                                                     // 确保在函数退出时取消上下文，以释放资源

	if err := cache.Ping(pingCtx); err != nil { // 检查Redis连接是否正常
		// Redis不可用时降级运行：禁用缓存，业务走纯数据库，不阻断服务启动
		log.Printf("ERROR: Redis not available, cache disabled, fallback to database: %v", err)
		cache.Disable()
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
	authMiddleware := middleware.NewAuthMiddleware(cache)

	// 设置路由
	r := router.SetupRouter(db, cache, rmq, authMiddleware)

	// 启动服务器
	if err := r.Run(":8080"); err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
	log.Println("Server is running on port 8080")
}
