package infra

import (
	"context"
	"encoding/json"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/utils/logger"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
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
	// 整个订阅器放入一个无限循环，只要 ctx 没取消，就永远重试
	go func() {
		for {
			// 1. 检查主程序是否退出了
			if ctx.Err() != nil {
				logger.Ctx(ctx).Info("context canceled, stop sse subscriber completely")
				return
			}

			// 2. 检查 Redis 是否可用（如果启动时降级禁用了，就先睡一会）
			if !redisCache.IsEnabled() {
				time.Sleep(3 * time.Second)
				continue
			}

			// 3. 发起订阅
			pubsub, err := redisCache.Subscribe(ctx, cache.SSEPushChannel())
			if err != nil || pubsub == nil {
				logger.Ctx(ctx).Error("subscribe sse channel failed, retry in 3s", logger.Err(err))
				time.Sleep(3 * time.Second)
				continue
			}

			logger.Ctx(ctx).Info("successfully subscribed to redis sse channel")

			// 4. 开始接收消息
			err = h.receiveMessages(ctx, pubsub)
			// 关闭当前的 pubsub（不管是正常退出还是报错）
			_ = pubsub.Close()

			// 5. 如果报错了，说明连接断了，等一小会儿重新进大循环重连
			if err != nil && ctx.Err() == nil {
				logger.Ctx(ctx).Error("receive messages loop exited, retrying in 3s", logger.Err(err))
				time.Sleep(3 * time.Second)
			}
		}
	}()
}

// receiveMessages 把处理消息的循环抽离出来，让 StartSSESubscriber 逻辑更干净
func (h *Hub) receiveMessages(ctx context.Context, pubsub *redis.PubSub) error {
	for {
		msg, err := pubsub.ReceiveMessage(ctx)
		if err != nil {
			// 如果主程序没退出，报错就是异常断开（如 client is closed, i/o timeout）
			if ctx.Err() == nil {
				return err // 把错误抛给外层去重连
			}
			return nil // 主程序退出，正常结束
		}

		// 解析 UserID
		var data struct {
			UserID uint `json:"user_id"`
		}
		if err := json.Unmarshal([]byte(msg.Payload), &data); err != nil {
			logger.Ctx(ctx).Error("unmarshal redis pubsub msg failed", logger.Err(err))
			continue
		}

		// 推送给前端
		h.SendToUser(ctx, data.UserID, msg.Payload)
	}
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
