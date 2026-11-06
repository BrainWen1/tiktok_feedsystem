package router

import (
	"feedsystem/internal/config"
	"feedsystem/internal/handler"
	"feedsystem/internal/handler/middleware"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/infra/mq"
	"feedsystem/internal/infra/ratelimit"
	infra "feedsystem/internal/infra/sse"
	"feedsystem/internal/repo"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/file"
	"feedsystem/internal/utils/logger"
	"feedsystem/internal/utils/response"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// maxRequestBodyBytes 全局请求体上限，略大于单个视频文件 100MB，预留 multipart 边界开销
const maxRequestBodyBytes = 110 << 20

// 接口限流策略：登录/注册/刷新严格限制，上传单独限流，feed 读取放宽。
// 登录类按 IP 限流，已登录接口优先按 userID 限流。
var (
	registerRateLimit       = ratelimit.Config{Scope: "register", Limit: 5, Window: time.Minute}
	loginRateLimit          = ratelimit.Config{Scope: "login", Limit: 10, Window: time.Minute}
	refreshRateLimit        = ratelimit.Config{Scope: "refresh", Limit: 20, Window: time.Minute}
	uploadVideoLimit        = ratelimit.Config{Scope: "upload_video", Limit: 5, Window: time.Minute, KeyByUserID: true}
	uploadImageLimit        = ratelimit.Config{Scope: "upload_image", Limit: 10, Window: time.Minute, KeyByUserID: true}
	feedReadRateLimitIP     = ratelimit.Config{Scope: "feed_read", Limit: 120, Window: time.Minute}
	feedReadRateLimitUserID = ratelimit.Config{Scope: "feed_read", Limit: 120, Window: time.Minute, KeyByUserID: true}
)

// blockedStaticExt 静态资源目录中禁止直接访问的敏感后缀，防止源码/配置泄露
var blockedStaticExt = map[string]struct{}{
	".go": {}, ".env": {}, ".yaml": {}, ".yml": {}, ".toml": {}, ".ini": {},
	".conf": {}, ".sql": {}, ".mod": {}, ".sum": {}, ".sh": {}, ".bak": {},
	".swp": {}, ".pem": {}, ".key": {},
}

// safeStaticFS 包装 http.FileSystem：
// 1. 拒绝目录请求，关闭目录列举；
// 2. 拒绝隐藏路径（.git/.env 等）与敏感后缀文件。
type safeStaticFS struct {
	fs http.FileSystem
}

// Open 在打开文件前做路径安全检查，并拒绝目录，避免暴露目录结构
func (s safeStaticFS) Open(name string) (http.File, error) {
	if isForbiddenStaticPath(name) {
		return nil, os.ErrNotExist
	}
	f, err := s.fs.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if info.IsDir() {
		_ = f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}

// isForbiddenStaticPath 判断静态资源路径是否命中敏感文件规则
func isForbiddenStaticPath(name string) bool {
	if strings.Contains(name, "..") {
		return true
	}
	cleaned := path.Clean("/" + name)
	// 隐藏文件/目录（.git、.env、.env.dev 等）一律拒绝
	for _, seg := range strings.Split(strings.TrimPrefix(cleaned, "/"), "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	_, blocked := blockedStaticExt[strings.ToLower(path.Ext(cleaned))]
	return blocked
}

func SetupRouter(sqlDB *gorm.DB, cache *cache.RedisCache, rmq *mq.RabbitMQ, authMiddleware *middleware.AuthMiddleware, hub *infra.Hub) *gin.Engine {
	// 创建一个默认的Gin引擎
	r := gin.Default()

	// RequestID 必须紧跟在引擎创建之后、所有业务中间件之前注册：
	// 这样后续 handler/service/repo 的 ctx 都携带 requestId，日志可全链路关联；
	// 响应头 X-Request-Id 也会覆盖包括鉴权失败、限流、413 在内的所有响应。
	// 注意：gin.Default() 自带的访问日志中间件先于本中间件执行，故其访问日志不含 requestId。
	r.Use(middleware.RequestID())

	// 默认不信任代理头，防止客户端伪造 X-Forwarded-For 绕过基于 IP 的登录限流。
	// 若部署在可信反向代理后，需在此处配置实际代理网段。
	if err := r.SetTrustedProxies(nil); err != nil {
		logger.Warn("set trusted proxies failed", logger.Err(err))
	}

	// 全局请求体上限：Content-Length 超限直接拒绝，未知长度由 MaxBytesReader 在读取时截断兜底，
	// 避免超大 multipart 表单在校验文件大小之前就落盘。
	r.Use(func(c *gin.Context) {
		if c.Request.ContentLength > maxRequestBodyBytes {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, response.Response{
				Code:    http.StatusRequestEntityTooLarge,
				Message: "request body too large",
				Data:    nil,
			})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodyBytes)
		c.Next()
	})

	// 注册全局中间件
	r.Use(middleware.Cors(config.AppConfig.CORS_allow_origins)) // 跨域白名单中间件

	// 限流器：Redis 故障时降级放行，不阻断业务
	limiter := ratelimit.New(cache)

	// 注册项目内置静态资源路由（默认头像、占位图），目录来自 StorageRoot；
	// safeStaticFS 关闭目录列举并拦截源码/配置等敏感文件。
	// 用户上传资源 /upload/ 由 Nginx 直接托管，Go 不注册静态路由、不转发文件流。
	r.StaticFS("/static", safeStaticFS{fs: http.Dir(file.StaticRoot())})

	// 初始化各层组件
	// ==========================================
	// 1. 初始化所有底层 Repo (复用实例)
	// ==========================================
	userRepo := repo.NewUserRepo(sqlDB)
	videoRepo := repo.NewVideoRepo(sqlDB)
	likeRepo := repo.NewLikeRepo(sqlDB)
	commentRepo := repo.NewCommentRepo(sqlDB)
	socialRepo := repo.NewSocialRepo(sqlDB)
	feedRepo := repo.NewFeedRepo(sqlDB)
	notificationRepo := repo.NewNotificationRepo(sqlDB)
	chunkRepo := repo.NewChunkRepo(cache)
	messageRepo := repo.NewMessageRepo(sqlDB)

	// ==========================================
	// 2. 初始化 MQ (带降级处理)
	// ==========================================
	likeMQ, err := mq.NewLikeMQ(rmq)
	if err != nil {
		// MQ降级：保留已禁用的合法实例，点赞投递由service层回退数据库
		logger.Warn("LikeMQ disabled, like events fallback to database", logger.Err(err))
	}

	commentMQ, err := mq.NewCommentMQ(rmq)
	if err != nil {
		// MQ降级：保留已禁用的合法实例，评论投递由service层回退数据库
		logger.Warn("CommentMQ disabled, comment events fallback to database", logger.Err(err))
	}

	// ==========================================
	// 3. 初始化 Service (按依赖顺序自底向上组装)
	// ==========================================
	// 基础 Service (无其他 Service 依赖)
	userService := service.NewUserService(userRepo, cache)
	notificationService := service.NewNotificationService(cache, notificationRepo, videoRepo, commentRepo)
	messageService := service.NewMessageService(messageRepo, cache)

	// 业务 Service (依赖基础 Service)
	likeService := service.NewLikeService(likeRepo, likeMQ, userService, notificationService, cache)
	videoService := service.NewVideoService(videoRepo, userService, cache, likeService)
	// 破循环依赖：这里把 VideoService 以接口形式注入回 LikeService
	likeService.SetVideoDetailer(videoService)
	commentService := service.NewCommentService(commentRepo, commentMQ, cache, likeService, videoService, notificationService)
	socialService := service.NewSocialService(socialRepo, cache, userService, notificationService)
	feedService := service.NewFeedService(feedRepo, likeService, socialService, commentService, userService)
	chunkService := service.NewChunkService(chunkRepo)

	// ==========================================
	// 4. 初始化 Handler (最上层，直接对接路由)
	// ==========================================
	userHandler := handler.NewUserHandler(userService)
	likeHandler := handler.NewLikeHandler(likeService)
	videoHandler := handler.NewVideoHandler(videoService)
	commentHandler := handler.NewCommentHandler(commentService)
	socialHandler := handler.NewSocialHandler(socialService)
	feedHandler := handler.NewFeedHandler(feedService)
	chunkHandler := handler.NewChunkHandler(chunkService)
	sseHandler := handler.NewSSEHandler(cache, hub) // 注入 hub，供 SSEHandler 调用 hub.SendToUser() 推送消息
	notificationHandler := handler.NewNotificationHandler(notificationService)
	messageHandler := handler.NewMessageHandler(messageService)

	// 设置路由
	// 健康检查路由
	r.GET("/healthz", func(c *gin.Context) {
		response.SuccessResponse(c, "health check ok")
	})

	// 用户相关路由
	// 公共路由组
	userGroup := r.Group("/user")
	{
		userGroup.POST("/register", limiter.Middleware(registerRateLimit), userHandler.Register)   // 用户注册（按IP严格限流）
		userGroup.POST("/login", limiter.Middleware(loginRateLimit), userHandler.Login)            // 用户登录（按IP严格限流）
		userGroup.POST("/refresh", limiter.Middleware(refreshRateLimit), userHandler.RefreshToken) // 刷新Token（按IP严格限流）
		userGroup.POST("/find_by_id", userHandler.FindByID)                                        // 根据ID查找用户
		userGroup.POST("/find_by_username", userHandler.FindByUsername)                            // 根据用户名查找用户
	}
	// 受保护的路由组
	protectedUserGroup := userGroup.Group("/").Use(authMiddleware.Auth()) // 使用Auth中间件鉴权
	{
		protectedUserGroup.POST("/logout", userHandler.Logout)                                                    // 用户登出
		protectedUserGroup.GET("/profile", userHandler.GetProfile)                                                // 获取用户资料
		protectedUserGroup.PATCH("/profile", userHandler.UpdateProfile)                                           // 更新用户资料
		protectedUserGroup.POST("/upload_avatar", limiter.Middleware(uploadImageLimit), userHandler.UploadAvatar) // 上传用户头像（按userID限流）
		protectedUserGroup.POST("/change_password", userHandler.ChangePassword)                                   // 修改用户密码
	}

	// 视频相关路由
	videoGroup := r.Group("/video")
	{
		// 软鉴权
		videoGroup.GET("/:id", videoHandler.VideoDetail) // 获取视频详情（路径参数统一为 /:id）
		videoGroup.GET("/list", videoHandler.VideoList)  // 获取视频列表
	}
	// 受保护的路由组
	protectedVideoGroup := videoGroup.Group("/").Use(authMiddleware.Auth()) // 使用Auth中间件鉴权
	{
		protectedVideoGroup.POST("/upload_video", limiter.Middleware(uploadVideoLimit), videoHandler.UploadVideo) // 上传视频（按userID限流）
		protectedVideoGroup.POST("/upload_cover", limiter.Middleware(uploadImageLimit), videoHandler.UploadCover) // 上传视频封面（按userID限流）
		protectedVideoGroup.POST("/publish", videoHandler.PublishVideo)                                           // 发布视频
	}

	// 点赞相关路由
	likeGroup := r.Group("/like")
	{
		likeGroup.POST("/list_liked", likeHandler.ListLikedVideos) // 列出用户点赞过的视频
	}
	protectedLikeGroup := likeGroup.Group("/").Use(authMiddleware.Auth())
	{
		protectedLikeGroup.POST("/like", likeHandler.LikeVideo)     // 点赞视频
		protectedLikeGroup.POST("/unlike", likeHandler.UnlikeVideo) // 取消点赞视频
		protectedLikeGroup.POST("/is_liked", likeHandler.IsLiked)   // 检查用户是否点赞了视频
	}

	// 评论相关路由
	commentGroup := r.Group("/comment")
	{
		commentGroup.GET("/list", commentHandler.ListComments) // 获取视频评论列表
	}
	protectedCommentGroup := commentGroup.Group("/").Use(authMiddleware.Auth())
	{
		protectedCommentGroup.POST("/publish", commentHandler.PublishComment) // 发布评论
		protectedCommentGroup.POST("/delete", commentHandler.DeleteComment)   // 删除评论
	}

	// 社交相关路由
	socialGroup := r.Group("/social")
	{
		socialGroup.GET("/bloggers", socialHandler.GetBloggers)   // 获取关注列表
		socialGroup.GET("/followers", socialHandler.GetFollowers) // 获取粉丝列表
		socialGroup.GET("/count", socialHandler.GetCounts)        // 获取关注和粉丝数量
	}
	protectedSocialGroup := socialGroup.Group("/").Use(authMiddleware.Auth())
	{
		protectedSocialGroup.POST("/follow", socialHandler.Follow)     // 关注博主
		protectedSocialGroup.POST("/unfollow", socialHandler.Unfollow) // 取消关注博主
		protectedSocialGroup.GET("/is_follow", socialHandler.IsFollow) // 获取两者关注关系
	}

	// feed流相关路由：读多写少，公开接口按 IP、登录接口按 userID 放宽限流
	feedIPLimiter := limiter.Middleware(feedReadRateLimitIP)
	feedGroup := r.Group("/feed")
	{
		feedGroup.GET("/list", feedIPLimiter, feedHandler.FeedListLatest)                   // 获取最新视频流
		feedGroup.GET("/listByPopularity", feedIPLimiter, feedHandler.FeedListByPopularity) // 获取按热度排序的视频流
		feedGroup.GET("/listByLikesCount", feedIPLimiter, feedHandler.FeedListLikesCount)   // 获取按点赞数排序的视频流
		feedGroup.GET("/listByTag", feedIPLimiter, feedHandler.FeedListByTag)               // 获取按标签过滤的视频流
	}
	// 鉴权先执行，限流器随后才能拿到 userID
	protectedFeedGroup := feedGroup.Group("/").Use(authMiddleware.Auth(), limiter.Middleware(feedReadRateLimitUserID))
	{
		protectedFeedGroup.GET("/listByFollowing", feedHandler.FeedListByFollowing) // 获取关注博主的视频流
	}

	// chunk 分片上传相关路由：必须登录，会话按 userID 隔离
	chunkGroup := videoGroup.Group("/chunk", authMiddleware.Auth())
	{
		chunkGroup.POST("/create", chunkHandler.CreateUploadSession) // 创建/恢复上传会话
		chunkGroup.POST("/upload", chunkHandler.UploadChunk)         // 上传单个分片（body 为分片二进制）
		chunkGroup.POST("/merge", chunkHandler.Merge)                // 合并分片并生成成品视频
		chunkGroup.GET("/status", chunkHandler.GetSessionStatus)     // 查询已上传分片，用于断点续传
		chunkGroup.POST("/cancel", chunkHandler.CancelUpload)        // 取消上传并清理碎片
	}

	// Notification 相关路由
	notificationGroup := r.Group("/notification", authMiddleware.Auth())
	{
		notificationGroup.GET("/list", notificationHandler.ListNotifications)      // 获取通知列表
		notificationGroup.GET("/unread_count", notificationHandler.GetUnreadCount) // 获取未读总数
		notificationGroup.POST("/read", notificationHandler.MarkAsRead)            // 标记单条已读
		notificationGroup.POST("/read_all", notificationHandler.MarkAllAsRead)     // 标记全部已读
	}

	// SSE 订阅路由：必须登录，按 userID 隔离
	// 1. 换取 Ticket 的接口，走常规 JWT 鉴权
	r.POST("/sse/ticket", authMiddleware.Auth(), sseHandler.GetSSETicket)
	// 2. SSE 连接接口，不带常规 JWT 中间件（因为头里塞不进去）
	r.GET("/sse", sseHandler.SSE)

	// Message 相关路由
	messageGroup := r.Group("/message", authMiddleware.Auth())
	{
		messageGroup.POST("/send", messageHandler.SendMessage)  // 发送消息
		messageGroup.GET("/history", messageHandler.GetHistory) // 获取聊天历史
	}

	// 返回配置好的路由引擎
	return r
}
