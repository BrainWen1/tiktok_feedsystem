# TikTok Feed System

A system for managing and displaying TikTok feeds.
```
tiktok_feedsystem
├─ README.md
├─ backend
│  ├─ README.md
│  ├─ assets
│  │  └─ static
│  │     └─ default_avatar.png
│  ├─ cmd
│  │  ├─ main
│  │  │  └─ main.go
│  │  └─ worker
│  │     └─ main.go
│  ├─ configs
│  │  └─ .env.example
│  ├─ docker-compose.yml
│  ├─ docs
│  ├─ go.mod
│  ├─ go.sum
│  └─ internal
│     ├─ config
│     │  └─ config.go
│     ├─ dto
│     │  ├─ comment.go
│     │  ├─ like.go
│     │  ├─ social.go
│     │  ├─ user.go
│     │  └─ video.go
│     ├─ handler
│     │  ├─ comment_handler.go
│     │  ├─ like_handler.go
│     │  ├─ middleware
│     │  │  ├─ auth.go
│     │  │  └─ cors.go
│     │  ├─ social_handler.go
│     │  ├─ user_handler.go
│     │  └─ video_handler.go
│     ├─ infra
│     │  ├─ cache
│     │  │  └─ redis_cache.go
│     │  ├─ database
│     │  │  └─ mysql.go
│     │  └─ mq
│     │     ├─ comment_consumer.go
│     │     ├─ commentmq.go
│     │     ├─ dlx.go
│     │     ├─ like_consumer.go
│     │     ├─ likemq.go
│     │     └─ rabbitmq.go
│     ├─ model
│     │  ├─ comment.go
│     │  ├─ like.go
│     │  ├─ social.go
│     │  ├─ user.go
│     │  ├─ user_refresh_token.go
│     │  └─ video.go
│     ├─ repo
│     │  ├─ comment_repo.go
│     │  ├─ like_repo.go
│     │  ├─ social_repo.go
│     │  ├─ user_repo.go
│     │  └─ video_repo.go
│     ├─ router
│     │  └─ router.go
│     ├─ service
│     │  ├─ comment_service.go
│     │  ├─ like_service.go
│     │  ├─ social_service.go
│     │  ├─ user_service.go
│     │  └─ video_service.go
│     └─ utils
│        ├─ file
│        │  └─ file.go
│        ├─ jwt
│        │  └─ jwt.go
│        └─ response
│           └─ response.go
├─ frontend
├─ notes
│  └─ logs
│     └─ log.md
├─ picture
├─ project_struct.md
├─ test
└─ user.postman_collection.json

```