package middleware

import (
	"feedsystem/internal/utils/jwt"
	"feedsystem/internal/utils/response"
	"strings"

	"github.com/gin-gonic/gin"
)

// Auth 中间件用于验证请求中的JWT令牌
func Auth() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// 拿到请求头中的Authorization字段
		authHeader := ctx.GetHeader("Authorization")
		if authHeader == "" {
			response.FailAuthResponse(ctx, "缺少Authorization头")
			ctx.Abort()
			return
		}

		// 拿到Authorization里的token字符串，通常格式是 "Bearer <token>"
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			response.FailAuthResponse(ctx, "Authorization头格式错误")
			ctx.Abort()
			return
		}

		// 解析和验证token
		token := parts[1]
		claims, err := jwt.ParseToken(token)
		if err != nil {
			response.FailAuthResponse(ctx, "无效的token")
			ctx.Abort()
			return
		}

		// 将用户信息存储在上下文中，供后续处理函数使用
		ctx.Set("userID", claims.UserID)
		ctx.Set("username", claims.UserName)

		ctx.Next()
	}
}
