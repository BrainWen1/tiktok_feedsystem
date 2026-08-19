package main

import (
	"context"
	"feedsystem/internal/config"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/infra/database"
	"feedsystem/internal/model"
	"feedsystem/internal/router"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
)

func main() {
	// 加载 .env（本地开发）
	if err := godotenv.Load(); err != nil {
		log.Println(".env not found; continuing")
	}

	// 加载配置
	configPath := os.Getenv("CONFIG_PATH") // 从环境变量中获取配置文件路径
	if configPath == "" {
		configPath = "configs/.env.dev" // 如果未设置环境变量，则使用默认路径
	}
	log.Printf("Loading config from %s", configPath)

	err := config.LoadConfig() // 加载配置
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
	} else {
		defer cache.Close() // 注册关闭Redis连接的延迟调用
		log.Printf("Redis connected (cache enabled)")
	}

	// RabbitMQ -- 预留

	// 设置路由
	r := router.SetupRouter(db, cache)

	// 启动服务器
	if err := r.Run(":8080"); err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
	log.Println("Server is running on port 8080")
}
