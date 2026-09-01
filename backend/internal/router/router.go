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

func SetupRouter(sqlDB *gorm.DB, cache *cache.RedisCache, authMiddleware *middleware.AuthMiddleware) *gin.Engine {
	// 创建一个默认的Gin引擎
	r := gin.Default()

	// 注册全局中间件
	r.Use(middleware.Cors()) // 跨域中间件

	// 注册静态文件路由，用于访问默认头像等静态资源
	r.Static("/static", "./assets/static")
	r.Static("/upload", "./.run/upload")

	// 初始化各层组件
	// User
	userRepo := repo.NewUserRepo(sqlDB)
	userService := service.NewUserService(userRepo, cache)
	userHandler := handler.NewUserHandler(userService)

	// 设置路由
	// 健康检查路由
	r.GET("/healthz", func(c *gin.Context) {
		response.SuccessResponse(c, "health check ok")
	})

	// 用户相关路由
	// 公共路由组
	userGroup := r.Group("/user")
	{
		userGroup.POST("/register", userHandler.Register)               // 用户注册
		userGroup.POST("/login", userHandler.Login)                     // 用户登录
		userGroup.POST("/refresh", userHandler.RefreshToken)            // 刷新Token
		userGroup.POST("/find_by_id", userHandler.FindByID)             // 根据ID查找用户
		userGroup.POST("/find_by_username", userHandler.FindByUsername) // 根据用户名查找用户
	}
	// 受保护的路由组
	protectedUserGroup := userGroup.Group("/").Use(authMiddleware.Auth()) // 使用Auth中间件鉴权
	{
		protectedUserGroup.POST("/logout", userHandler.Logout)                  // 用户登出
		protectedUserGroup.GET("/profile", userHandler.GetProfile)              // 获取用户资料
		protectedUserGroup.PATCH("/profile", userHandler.UpdateProfile)         // 更新用户资料
		protectedUserGroup.POST("/upload_avatar", userHandler.UploadAvatar)     // 上传用户头像
		protectedUserGroup.POST("/change_password", userHandler.ChangePassword) // 修改用户密码
	}

	// 返回配置好的路由引擎
	return r
}
