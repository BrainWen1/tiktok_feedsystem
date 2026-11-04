package handler

import (
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/utils/logger"
	"feedsystem/internal/utils/response"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type SSEHandler struct {
	redisCache *cache.RedisCache
}

func NewSSEHandler(redisCache *cache.RedisCache) *SSEHandler {
	return &SSEHandler{
		redisCache: redisCache,
	}
}

// 外层 sync.Map: map[uint(用户ID)] -> *sync.Map(内层)
// 内层 sync.Map: map[string(连接ID)] -> chan string
var sseClients sync.Map // map[uint]chan string，存 userID 对应的通道

// SSE 处理函数，前端通过 /sse?ticket=xxx 连接，用于创建一个 SSE 连接，接收实时消息
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
	val, err := h.redisCache.Get(c.Request.Context(), key)

	// 无论查没查到，立刻删除这个 Ticket，保证只能使用一次
	h.redisCache.Delete(c.Request.Context(), key)

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

	// 生成一个唯一连接 ID
	clientID := uuid.New().String()

	// 初始化该用户的 channel
	messageChan := make(chan string, 10)

	// LoadOrStore：如果外层有这个 userID，返回已存在的值（loaded=true）；
	// 如果没有，存入一个新的 &sync.Map{} 并返回传入的值（loaded=false）。
	actual, _ := sseClients.LoadOrStore(userID, &sync.Map{})
	userMap := actual.(*sync.Map) // 指向内层的 map，key=clientID，value=messageChan

	// 本连接的 channel 存入内层 map
	userMap.Store(clientID, messageChan)

	// 断开时清理
	defer func() {
		// 只删除自己的连接
		userMap.Delete(clientID)

		// 判断一下这个内层 map 是不是空了，如果空了，在外层 map 里把整个内层 map 也删掉，释放内存。
		// 因为 sync.Map 没有 Len()，我们通过 Range 试探：
		isEmpty := true
		userMap.Range(func(k, v interface{}) bool {
			isEmpty = false
			return false // 只要找到一个，立刻停止遍历
		})
		if isEmpty {
			sseClients.Delete(userID)
		}
	}()

	// 把通道里的数据，源源不断写给前端
	c.Stream(func(w io.Writer) bool {
		select {
		case msg := <-messageChan:
			// 格式必须是 "data: xxx\n\n"
			c.SSEvent("message", msg)
			return true // 继续监听
		case <-time.After(15 * time.Second):
			// 15秒没消息，发送一个心跳，防止前端断开
			c.SSEvent("heartbeat", "ping")
			return true
		case <-c.Request.Context().Done(): // 前端断开连接
			return false
		}
	})
}

// API 进程收到 Redis 消息时，调用这个函数推给用户
func SendToUser(userID uint, content string) {
	if userMapObj, ok := sseClients.Load(userID); ok {
		// 遍历该用户的所有连接，依次发送消息
		userMap := userMapObj.(*sync.Map)

		userMap.Range(func(key, val interface{}) bool {
			// key 是 clientID，val 是 messageChan
			messageChan := val.(chan string)
			// 非阻塞发送消息，如果通道满了就丢弃，避免阻塞
			select {
			case messageChan <- content:
				// 发送成功
			default:
				// 通道满了，说明前端卡顿或断开，直接丢弃该消息。
				// 为什么敢丢弃？因为数据库里已经落库了通知，前端只要重新连上，
				// 或者刷新页面拉取列表调用/notification/list，依然能看到这条通知。
				clientID := key.(string)
				logger.Ctx(nil).Warn("SSE channel full, dropping message", logger.Uint("userID", userID), logger.String("clientID", clientID))
			}
			return true // 继续遍历
		})
	}
}

// GetSSETicket 处理函数，前端调用 /sse/ticket 获取一个临时 ticket，用于 SSE 连接鉴权
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
