package handler

import (
	"feedsystem/internal/infra/cache"
	infra "feedsystem/internal/infra/sse"
	"feedsystem/internal/utils/logger"
	"feedsystem/internal/utils/response"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type SSEHandler struct {
	redisCache *cache.RedisCache
	hub        *infra.Hub
}

func NewSSEHandler(redisCache *cache.RedisCache, hub *infra.Hub) *SSEHandler {
	return &SSEHandler{
		redisCache: redisCache,
		hub:        hub,
	}
}

// SSE 处理函数，前端通过 /sse?ticket=xxx 连接，用于创建一个 SSE 连接，接收实时消息
// @Summary      建立 SSE 实时消息连接
// @Description  使用 POST /sse/ticket 获取的一次性 ticket 建立 SSE 长连接，持续接收实时私信。服务端返回 text/event-stream：event 为 message 时 data 为消息内容，event 为 heartbeat 时 data 为 ping（每 15 秒一次）；连接建立后不返回 JSON，客户端断开即结束。ticket 30 秒内有效且仅可使用一次
// @Tags         私信模块
// @Produce      text/event-stream
// @Param        ticket query string true "一次性连接票据，由 POST /sse/ticket 获取"
// @Success      200 {string} string "SSE 事件流（event: message / event: heartbeat）"
// @Failure      400 {object} response.Response "缺少 ticket、ticket 无效或已过期"
// @Router       /sse [get]
func (h *SSEHandler) SSE(c *gin.Context) {
	// 从 query 参数获取 ticket
	ticket := c.Query("ticket")
	if ticket == "" {
		logger.Ctx(c.Request.Context()).Warn("missing ticket in query")
		response.FailResponse(c, "缺少 ticket 参数")
		return

	}

	// 去 Redis 里查 ticket
	key := cache.SSETicketKey(ticket)
	// 使用现成的GetDel操作，保证ticket只能使用一次
	val, err := h.redisCache.GetDel(c.Request.Context(), key)

	if err != nil || val == "" {
		logger.Ctx(c.Request.Context()).Warn("invalid or expired ticket", logger.Err(err))
		response.FailResponse(c, "无效或过期的 ticket")
		return
	}

	// 解析 userID
	userID64, _ := strconv.ParseUint(val, 10, 64)
	userID := uint(userID64)

	// 设置 SSE 响应头
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no") // Nginx 反向代理时，禁用缓冲，确保消息实时推送给前端

	// 针对当前 SSE 请求，把写超时时间设为无限（或者设置一个极长的值）
	rc := http.NewResponseController(c.Writer)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil { // time.Time{} 表示无限期
		logger.Ctx(c.Request.Context()).Error("failed to set write deadline for SSE", logger.Err(err))
		// 视情况决定是否 return，如果这里报错说明底层不支持，但一般都会支持
	}

	// 生成一个唯一连接 ID
	clientID := uuid.New().String()

	// 注册 SSE 客户端连接，获取对应的消息通道
	messageChan := h.hub.RegisterSSEClient(c.Request.Context(), userID, clientID)

	// 断开时清理
	defer func() {
		h.hub.UnregisterSSEClient(c.Request.Context(), userID, clientID)
		logger.Ctx(c.Request.Context()).Info("SSE client disconnected", logger.Uint("userID", userID), logger.String("clientID", clientID))
	}()

	// 创建一个 ticker，避免 time.After 每次发送心跳都要创建和销毁定时器，高并发下开销大
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	// 把通道里的数据，源源不断写给前端
	c.Stream(func(w io.Writer) bool {
		select {
		case msg := <-messageChan:
			// 格式必须是 "data: xxx\n\n"
			c.SSEvent("message", msg)
			return true // 继续监听
		case <-ticker.C:
			// 15秒没消息，发送一个心跳，防止前端断开
			c.SSEvent("heartbeat", "ping")
			return true
		case <-c.Request.Context().Done(): // 前端断开连接
			return false
		}
	})
}

// GetSSETicket 处理函数，前端调用 /sse/ticket 获取一个临时 ticket，用于 SSE 连接鉴权
// @Summary      获取 SSE 连接票据
// @Description  为当前登录用户签发一次性 SSE 连接票据，data 包含 ticket 字段；ticket 30 秒内有效且仅可使用一次，用于浏览器 EventSource 无法携带 Authorization 头时的鉴权
// @Tags         私信模块
// @Produce      json
// @Security     Bearer
// @Success      200 {object} response.Response{data=map[string]interface{}} "获取成功，data 包含 ticket 字段"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /sse/ticket [post]
func (h *SSEHandler) GetSSETicket(c *gin.Context) {
	// 从中间件上下文拿到 userId
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	// 生成一个随机的 UUID 作为 ticket
	ticket := uuid.New().String()

	// 存入 Redis，Key 为 ticket，Value 为 userID，设置 30 秒过期
	key := cache.SSETicketKey(ticket)
	err := h.redisCache.Set(c.Request.Context(), key, fmt.Sprintf("%d", userID), cache.SSETicketTTL)
	if err != nil {
		response.FailResponse(c, "internal error")
		return
	}

	// 返回给前端
	response.SuccessResponse(c, gin.H{"ticket": ticket})
}
