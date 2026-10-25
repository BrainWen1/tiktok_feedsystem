package ratelimit

import (
	"net/http"
	"strconv"
	"time"

	"feedsystem/internal/infra/cache"
	"feedsystem/internal/utils/logger"
	"feedsystem/internal/utils/response"

	"github.com/gin-gonic/gin"
)

// Limiter 基于 Redis 滑动窗口的接口限流器
type Limiter struct {
	cache *cache.RedisCache
}

// New 创建限流器；cache 可为 nil，此时所有请求放行（降级）
func New(c *cache.RedisCache) *Limiter {
	return &Limiter{cache: c}
}

// Config 单个限流策略
type Config struct {
	Scope       string        // 限流维度，参与 Redis key 组装，如 login/register/upload_video
	Limit       int           // 窗口内允许的最大请求数
	Window      time.Duration // 滑动窗口长度
	KeyByUserID bool          // true：已登录按 userID 限流，未登录回退 ip；false：始终按 ip 限流
}

// Middleware 返回 Gin 限流中间件，超限统一返回 429
func (l *Limiter) Middleware(cfg Config) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		principal, kind := principalOf(ctx, cfg.KeyByUserID)
		key := "ratelimit:" + cfg.Scope + ":" + kind + ":" + principal

		allowed, err := l.cache.AllowSlidingWindow(ctx.Request.Context(), key, cfg.Limit, cfg.Window)
		if err != nil {
			// Redis 故障时放行，避免限流组件拖垮整个服务，仅打印告警供监控发现
			logger.Warn("rate limit check failed, fail open", logger.String("scope", cfg.Scope), logger.String("key", key), logger.Err(err))
			ctx.Next()
			return
		}
		if !allowed {
			logger.Warn("rate limit exceeded", logger.String("scope", cfg.Scope), logger.String("principal", principal), logger.String("path", ctx.Request.URL.Path), logger.String("ip", ctx.ClientIP()))
			ctx.Header("Retry-After", strconv.Itoa(int(cfg.Window.Seconds())))
			ctx.AbortWithStatusJSON(http.StatusTooManyRequests, response.Response{
				Code:    http.StatusTooManyRequests,
				Message: "请求过于频繁，请稍后再试",
				Data:    nil,
			})
			return
		}

		ctx.Next()
	}
}

// principalOf 计算限流主体：优先 userID，未登录时回退客户端 IP
func principalOf(ctx *gin.Context, keyByUserID bool) (string, string) {
	if keyByUserID {
		if v, ok := ctx.Get("userID"); ok {
			if userID, ok := v.(uint); ok && userID > 0 {
				return strconv.FormatUint(uint64(userID), 10), "uid"
			}
		}
	}
	return ctx.ClientIP(), "ip"
}
