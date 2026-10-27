package middleware

import (
	"net/http"
	"strings"

	"feedsystem/internal/utils/requestid"

	"github.com/gin-gonic/gin"
)

// allowedMethods 预检请求允许的 HTTP 方法
const allowedMethods = "GET, POST, PUT, PATCH, DELETE, OPTIONS"

// allowedHeaders 预检请求允许携带的请求头。
// 显式放行 X-Request-Id，允许跨域请求复用上游追踪ID。
const allowedHeaders = "Origin, Content-Type, Authorization, " + requestid.Header

// exposeHeaders 允许浏览器脚本读取的响应头。
// 暴露 X-Request-Id，前端可直接拿到追踪ID用于问题反馈。
const exposeHeaders = "Content-Length, " + requestid.Header

// corsMaxAge 预检结果缓存时间（秒），12 小时
const corsMaxAge = "43200"

// Cors 跨域中间件：仅放行配置白名单中的 Origin。
// 命中白名单时才回写 Allow-Credentials，未命中时不返回任何 CORS 头，
// 避免 AllowOrigins=* 与 AllowCredentials=true 同时出现的非法组合。
func Cors(allowOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowOrigins))
	for _, origin := range allowOrigins {
		if origin = strings.TrimSpace(origin); origin != "" {
			allowed[origin] = struct{}{}
		}
	}

	return func(ctx *gin.Context) {
		origin := ctx.GetHeader("Origin")
		_, matched := allowed[origin]

		// 响应随 Origin 变化，必须声明 Vary，防止代理/缓存串用不同来源的响应
		ctx.Header("Vary", "Origin")

		if matched && origin != "" {
			ctx.Header("Access-Control-Allow-Origin", origin)
			ctx.Header("Access-Control-Allow-Credentials", "true")
			ctx.Header("Access-Control-Expose-Headers", exposeHeaders)
		}

		// 预检请求直接结束；未命中白名单时只返回 204，不携带 CORS 头，由浏览器拒绝
		if ctx.Request.Method == http.MethodOptions {
			if matched && origin != "" {
				ctx.Header("Access-Control-Allow-Methods", allowedMethods)
				ctx.Header("Access-Control-Allow-Headers", allowedHeaders)
				ctx.Header("Access-Control-Max-Age", corsMaxAge)
			}
			ctx.AbortWithStatus(http.StatusNoContent)
			return
		}

		ctx.Next()
	}
}
