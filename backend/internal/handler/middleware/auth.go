package middleware

import (
	"errors"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/utils/jwt"
	"feedsystem/internal/utils/logger"
	"feedsystem/internal/utils/response"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/gin-gonic/gin"
)

// sharedCache 供包级软鉴权函数TryGetUserID复用，保证软鉴权也能校验token黑名单
var sharedCache atomic.Pointer[cache.RedisCache]

// 避免auth中间件和service层绑定，AuthMiddleware不直接依赖业务层UserService，而是依赖基础设施层的RedisCache
type AuthMiddleware struct {
	cache *cache.RedisCache
}

func NewAuthMiddleware(cache *cache.RedisCache) *AuthMiddleware {
	if cache != nil {
		sharedCache.Store(cache)
	}
	return &AuthMiddleware{cache: cache}
}

// Auth 中间件用于验证请求中的JWT令牌
func (am *AuthMiddleware) Auth() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// 拿到请求头中的Authorization字段
		authHeader := ctx.GetHeader("Authorization")
		if authHeader == "" {
			logger.Ctx(ctx.Request.Context()).Debug("authorization header is missing")
			response.FailAuthResponse(ctx, "缺少Authorization头")
			ctx.Abort()
			return
		}

		// 拿到Authorization里的token字符串，通常格式是 "Bearer <token>"
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			// 屏蔽敏感信息，防止日志泄露（Authorization头内含Access Token）
			logger.Ctx(ctx.Request.Context()).Warn("authorization header format is invalid")
			response.FailAuthResponse(ctx, "Authorization头格式错误")
			ctx.Abort()
			return
		}

		// 解析和验证token
		token := parts[1]
		claims, err := jwt.ParseToken(token)
		if err != nil {
			logger.Ctx(ctx.Request.Context()).Warn("parse token failed", logger.Err(err))
			response.FailAuthResponse(ctx, "无效的token")
			ctx.Abort()
			return
		}

		// 检查 token 的颁发时间是否早于用户最近一次改密时间（token epoch）。
		// 缓存禁用时无法读取 epoch，降级为只校验签名与黑名单（黑名单同样为空操作）。
		if am.cache.IsEnabled() {
			valid, epochErr := tokenEpochValid(ctx, am.cache, claims)
			if epochErr != nil {
				// Redis 故障不能返回 401，否则客户端会误以为登录失效并清空 token
				logger.Ctx(ctx.Request.Context()).Error("check user token epoch failed", logger.Err(epochErr))
				ctx.JSON(http.StatusServiceUnavailable, response.Response{
					Code:    http.StatusServiceUnavailable,
					Message: "服务暂不可用，请稍后重试",
				})
				ctx.Abort()
				return
			}
			if !valid {
				response.FailAuthResponse(ctx, "登录状态已失效，请重新登录")
				ctx.Abort()
				return
			}
		}

		// 检查token是否在黑名单中
		isBlacklisted, err := am.cache.IsTokenInBlacklist(ctx.Request.Context(), token)
		if err != nil {
			logger.Ctx(ctx.Request.Context()).Error("check token blacklist failed", logger.Err(err))
			response.FailAuthResponse(ctx, "服务器错误")
			ctx.Abort()
			return
		}
		if isBlacklisted { // token在黑名单中，说明用户已经登出或token已被注销
			// 屏蔽敏感信息，防止日志泄露
			logger.Ctx(ctx.Request.Context()).Warn("token is blacklisted", logger.Uint("user_id", claims.UserID))
			response.FailAuthResponse(ctx, "token已被注销")
			ctx.Abort()
			return
		}

		// 将用户信息存储在上下文中，供后续处理函数使用
		ctx.Set("userID", claims.UserID)
		ctx.Set("username", claims.UserName)
		logger.Ctx(ctx.Request.Context()).Debug("authenticated user", logger.Uint("user_id", claims.UserID), logger.String("username", claims.UserName))

		ctx.Next()
	}
}

// TryGetUserID 尝试从token中获取用户ID，如果解析失败则返回0，表示游客
func TryGetUserID(ctx *gin.Context) uint {
	authHeader := ctx.GetHeader("Authorization")
	if authHeader == "" {
		return 0
	}
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		return 0
	}
	tokenStr := parts[1]

	// 解析token，获取用户ID
	claims, err := jwt.ParseToken(tokenStr)
	if err != nil {
		return 0
	}

	// 软鉴权同样校验黑名单：已注销/拉黑的token只能按游客处理，禁止识别为登录用户
	if c := sharedCache.Load(); c != nil && c.IsEnabled() {
		isBlacklisted, err := c.IsTokenInBlacklist(ctx.Request.Context(), tokenStr)
		if err != nil {
			// 黑名单查询失败时按游客降级，避免把无法确认身份的请求当成已登录
			logger.Ctx(ctx.Request.Context()).Error("check token blacklist in TryGetUserID failed", logger.Err(err))
			return 0
		}
		if isBlacklisted {
			logger.Ctx(ctx.Request.Context()).Warn("soft auth token is blacklisted", logger.Uint("user_id", claims.UserID))
			return 0
		}

		// 改密前签发的旧 token 在软鉴权中也只能按游客处理
		valid, err := tokenEpochValid(ctx, c, claims)
		if err != nil {
			logger.Ctx(ctx.Request.Context()).Error("check token epoch in TryGetUserID failed", logger.Err(err))
			return 0
		}
		if !valid {
			return 0
		}
	}
	return claims.UserID
}

// tokenEpochValid 校验 token 的签发时间不早于用户最近一次改密时间。
// 返回 (false, nil) 表示 token 已因改密失效；(false, err) 表示 Redis 故障、无法确认。
func tokenEpochValid(ctx *gin.Context, c *cache.RedisCache, claims *jwt.Claims) (bool, error) {
	epochStr, err := c.Get(ctx.Request.Context(), cache.UserTokenEpochKey(claims.UserID))
	if err != nil {
		if errors.Is(err, cache.ErrCacheMiss) {
			// 用户从未改过密码，没有 epoch
			return true, nil
		}
		return false, err
	}

	epoch, parseErr := strconv.ParseInt(epochStr, 10, 64)
	if parseErr != nil {
		return false, fmt.Errorf("parse token epoch for user %d: %w", claims.UserID, parseErr)
	}
	if claims.IssuedAt == nil || claims.IssuedAt.Unix() < epoch {
		return false, nil
	}
	return true, nil
}
