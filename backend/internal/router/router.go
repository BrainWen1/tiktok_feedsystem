package router

import (
	"feedsystem/internal/handler"
	"feedsystem/internal/handler/middleware"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/repo"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func SetupRouter(sqlDB *gorm.DB, cache *cache.RedisCache) *gin.Engine {
	// 创建一个默认的Gin引擎
	r := gin.Default()

	// 注册全局中间件
	r.Use(middleware.Cors()) // 跨域中间件

	// 初始化各层组件
	// User
	userRepo := repo.NewUserRepo(sqlDB)
	userService := service.NewUserService(userRepo)
	userHandler := handler.NewUserHandler(userService)

	// 设置路由
	// 健康检查路由
	r.GET("/healthz", func(c *gin.Context) {
		response.SuccessResponse(c, "health check ok")
	})

	// 用户相关路由
	userGroup := r.Group("/user")
	{
		userGroup.POST("/register", userHandler.Register)    // 用户注册
		userGroup.POST("/login", userHandler.Login)          // 用户登录
		userGroup.POST("/refresh", userHandler.RefreshToken) // 刷新Token
	}

	return r
}
