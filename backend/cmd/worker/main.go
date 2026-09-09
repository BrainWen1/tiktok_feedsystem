package main

import (
	"feedsystem/internal/config"
	"feedsystem/internal/infra/cache"
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

	database.AutoMigrate(db, &model.User{}, &model.Video{}, &model.Like{}, &model.Comment{}) // 自动迁移数据库表结构

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
	commentMQ, err := mq.NewCommentMQ(rmq)
	if err != nil {
		log.Fatalf("Failed to initialize CommentMQ: %v", err)
	}

	// 初始化仓库
	likeRepo := repo.NewLikeRepo(db)
	commentRepo := repo.NewCommentRepo(db)

	// 初始化Redis缓存
	cache := cache.NewRedisCache(config.AppConfig.Redis_addr, config.AppConfig.Redis_password, config.AppConfig.Redis_db)
	if cache == nil {
		log.Fatalf("Failed to initialize Redis cache")
	}

	// 启动点赞消费
	err = mq.StartLikeConsumer(likeMQ, likeRepo, cache)
	if err != nil {
		log.Fatalf("Failed to start like consumer: %v", err)
	}

	// 启动评论消费
	err = mq.StartCommentConsumer(commentMQ, commentRepo, cache)
	if err != nil {
		log.Fatalf("Failed to start comment consumer: %v", err)
	}

	log.Println("Worker is running for like and comment events...")
	select {} // 阻塞主线程，保持worker运行
}
