package infra

import (
	"context"
	"encoding/json"
	"errors"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/utils/logger"
	"sync"
)

type Hub struct {
	// 外层 sync.Map: map[uint(用户ID)] -> *sync.Map(内层)
	// 内层 sync.Map: map[string(连接ID)] -> chan string
	sseClients sync.Map // map[uint]chan string，存 userID 对应的通道
}

func NewHub() *Hub {
	return &Hub{
		sseClients: sync.Map{},
	}
}

// RegisterSSEClient 注册一个新的 SSE 客户端连接
func (h *Hub) RegisterSSEClient(ctx context.Context, userID uint, clientID string) chan string {
	// 初始化该用户的 channel
	messageChan := make(chan string, 10)

	// LoadOrStore：如果外层有这个 userID，返回已存在的值（loaded=true）；
	// 如果没有，存入一个新的 &sync.Map{} 并返回传入的值（loaded=false）。
	actual, loaded := h.sseClients.LoadOrStore(userID, &sync.Map{})
	if !loaded {
		// 如果是新创建的内层 map，打印日志
		logger.Ctx(ctx).Info("created new SSE client map for user", logger.Uint("userID", userID))
	}
	userMap := actual.(*sync.Map) // 指向内层的 map，key=clientID，value=messageChan

	// 本连接的 channel 存入内层 map
	userMap.Store(clientID, messageChan)

	return messageChan
}

// UnregisterSSEClient 注销一个 SSE 客户端连接
func (h *Hub) UnregisterSSEClient(ctx context.Context, userID uint, clientID string) {
	if userMapObj, ok := h.sseClients.Load(userID); ok {
		userMap := userMapObj.(*sync.Map)
		userMap.Delete(clientID)

		// 判断一下这个内层 map 是不是空了，如果空了，在外层 map 里把整个内层 map 也删掉，释放内存。
		// 因为 sync.Map 没有 Len()，我们通过 Range 试探：
		isEmpty := true
		userMap.Range(func(k, v interface{}) bool {
			isEmpty = false
			return false // 只要找到一个，立刻停止遍历
		})
		if isEmpty {
			logger.Ctx(ctx).Info("no more SSE clients for user, removing user map", logger.Uint("userID", userID))
			h.sseClients.Delete(userID)
		}
	}
}

// StartSSESubscriber 启动一个协程订阅 Redis Pub/Sub 的 SSE 推送频道。
// 这个函数在 main() 中调用，确保在服务启动时就开始监听通知事件。
// 当 Worker 发布通知事件到 Redis 时，这个协程会接收消息并触发 SSE 推送。
func (h *Hub) StartSSESubscriber(ctx context.Context, redisCache *cache.RedisCache) {
	pubsub, err := redisCache.Subscribe(ctx, cache.SSEPushChannel())
	if err != nil {
		logger.Ctx(ctx).Error("subscribe sse channel failed", logger.Err(err))
		return
	}
	defer pubsub.Close()

	// 启动一个协程死循环接收 Redis 消息
	go func() {
		for {
			msg, err := pubsub.ReceiveMessage(ctx)
			if err != nil {
				logger.Ctx(ctx).Error("receive redis pubsub msg failed", logger.Err(err))
				// 如果是上下文取消或连接关闭，退出循环，避免热循环
				if errors.Is(err, context.Canceled) || ctx.Err() != nil {
					return
				}
				continue
			}
			// msg.Payload 就是 Worker 发过来的 JSON
			// 只需要解析出 user_id 用给内层 map 路由到对应的连接的 channel
			// 然后把 worker 发到 redis 的 json string 直接传给 SendToUser()，再让 SSEHandler 直接写给前端
			var data struct {
				UserID uint `json:"user_id"`
			}
			if err := json.Unmarshal([]byte(msg.Payload), &data); err != nil {
				logger.Ctx(ctx).Error("unmarshal redis pubsub msg failed", logger.Err(err))
				continue
			}
			h.SendToUser(ctx, data.UserID, msg.Payload)
		}
	}()
}

// SendToUser 发送 SSE 消息给指定用户的所有连接
func (h *Hub) SendToUser(ctx context.Context, userID uint, messageJSON string) {
	if userMapObj, ok := h.sseClients.Load(userID); ok {
		// 遍历该用户的所有连接，依次发送消息
		userMap := userMapObj.(*sync.Map)

		userMap.Range(func(key, val interface{}) bool {
			// key 是 clientID，val 是 messageChan
			messageChan := val.(chan string)
			// 非阻塞发送消息，如果通道满了就丢弃，避免阻塞
			select {
			case messageChan <- messageJSON: // 直接把 JSON 字符串发给 SSEHandler，让它写给前端
				// 发送成功
			default:
				// 通道满了，说明前端卡顿或断开，直接丢弃该消息。
				// 为什么敢丢弃？因为数据库里已经落库了通知，前端只要重新连上，
				// 或者刷新页面拉取列表调用/notification/list，依然能看到这条通知。
				clientID := key.(string)
				logger.Ctx(ctx).Warn("SSE channel full, dropping message", logger.Uint("userID", userID), logger.String("clientID", clientID))
			}
			return true // 继续遍历
		})
	}
}

// PublishSSEMessage 是一个工具函数，用于向 Redis Pub/Sub 发布 SSE 消息。
func PublishSSEMessage(ctx context.Context, redisCache *cache.RedisCache, payload any) error {
	// 使用同一个 redis 频道 "sse:push"，让 Hub 的订阅协程接收消息并推送给前端。
	channel := cache.SSEPushChannel()
	// 将传入的不同结构的结构体序列化为 JSON 字符串
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return redisCache.Publish(ctx, channel, string(data))
}
