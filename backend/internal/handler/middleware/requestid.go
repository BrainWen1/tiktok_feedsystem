package middleware

import (
	"strings"

	"feedsystem/internal/utils/requestid"

	"github.com/gin-gonic/gin"
)

// maxRequestIDLen 复用上游 X-Request-Id 时的最大长度。
// 超长头会污染日志并增加存储压力，此时按未携带处理，重新生成。
const maxRequestIDLen = 128

// RequestID 全链路追踪中间件，必须在业务路由/其它业务中间件之前注册。
//
// 处理逻辑：
//  1. 上游请求已带合法 X-Request-Id 时直接复用，保证跨服务链路连续；
//  2. 未携带时生成 UUID v4；
//  3. 写入响应头 X-Request-Id，方便前端/Postman 拿到追踪ID；
//  4. 注入 request.Context()，后续 handler/service/repo 透传 ctx 即可，
//     logger.Ctx(ctx) 会自动输出 request_id。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.GetHeader(requestid.Header))
		if id == "" || len(id) > maxRequestIDLen {
			id = requestid.New()
		}

		// 先写响应头，保证后续 handler 提前返回（4xx/5xx）时仍能拿到追踪ID
		c.Header(requestid.Header, id)

		// 注入请求上下文；service/repo 统一使用 c.Request.Context() 即可透传
		ctx := requestid.WithContext(c.Request.Context(), id)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}
