package main

import (
	"feedsystem/internal/config"
	"feedsystem/internal/infra/database"
	"feedsystem/internal/infra/mq"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"log"

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

	// 使用配置连接数据库服务
	db, err := database.ConnectDB(config.AppConfig.DB_dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	database.AutoMigrate(db, &model.User{}, &model.Video{}, &model.Like{}) // 自动迁移数据库表结构

	defer database.CloseDB() // 注册关闭数据库连接的延迟调用

	// 初始化MQ
	rmq, err := mq.NewRabbitMQ()
	if err != nil {
		log.Fatalf("Failed to initialize RabbitMQ: %v", err)
	}
	likeMQ, err := mq.NewLikeMQ(rmq)
	if err != nil {
		log.Fatalf("Failed to initialize LikeMQ: %v", err)
	}

	likeRepo := repo.NewLikeRepo(db)

	// 启动点赞消费
	err = mq.StartLikeConsumer(likeMQ, likeRepo)
	if err != nil {
		log.Fatalf("Failed to start like consumer: %v", err)
	}

	log.Println("Worker is running...")
	select {} // 阻塞主线程，保持worker运行
}
