package middleware

import (
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/utils/jwt"
	"feedsystem/internal/utils/response"
	"log"
	"strings"

	"github.com/gin-gonic/gin"
)

// 避免auth中间件和service层绑定，AuthMiddleware不直接依赖业务层UserService，而是依赖基础设施层的RedisCache
type AuthMiddleware struct {
	cache *cache.RedisCache
}

func NewAuthMiddleware(cache *cache.RedisCache) *AuthMiddleware {
	return &AuthMiddleware{cache: cache}
}

// Auth 中间件用于验证请求中的JWT令牌
func (am *AuthMiddleware) Auth() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// 拿到请求头中的Authorization字段
		authHeader := ctx.GetHeader("Authorization")
		if authHeader == "" {
			log.Printf("Authorization header is missing")
			response.FailAuthResponse(ctx, "缺少Authorization头")
			ctx.Abort()
			return
		}

		// 拿到Authorization里的token字符串，通常格式是 "Bearer <token>"
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			log.Printf("Authorization header format is invalid: %s", authHeader)
			response.FailAuthResponse(ctx, "Authorization头格式错误")
			ctx.Abort()
			return
		}

		// 解析和验证token
		token := parts[1]
		claims, err := jwt.ParseToken(token)
		if err != nil {
			log.Printf("Failed to parse token: %v", err)
			response.FailAuthResponse(ctx, "无效的token")
			ctx.Abort()
			return
		}

		// 检查token是否在黑名单中
		isBlacklisted, err := am.cache.IsTokenInBlacklist(ctx, token)
		if err != nil {
			log.Printf("Error checking token blacklist: %v", err)
			response.FailAuthResponse(ctx, "服务器错误")
			ctx.Abort()
			return
		}
		if isBlacklisted { // token在黑名单中，说明用户已经登出或token已被注销
			log.Printf("Token is blacklisted: %s", token)
			response.FailAuthResponse(ctx, "token已被注销")
			ctx.Abort()
			return
		}

		// 将用户信息存储在上下文中，供后续处理函数使用
		ctx.Set("userID", claims.UserID)
		ctx.Set("username", claims.UserName)
		log.Printf("Authenticated user: ID=%d, Username=%s", claims.UserID, claims.UserName)

		ctx.Next()
	}
}

// TryGetUID 尝试从token中获取用户ID，如果解析失败则返回0，表示游客
func TryGetUID(ctx *gin.Context) uint {
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
	return claims.UserID
}
