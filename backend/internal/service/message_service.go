package service

import (
	"context"
	"errors"
	"feedsystem/internal/dto"
	"feedsystem/internal/infra/cache"
	infra "feedsystem/internal/infra/sse"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"feedsystem/internal/utils/logger"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// Redis Hash 字段名
	lastContentField = "last_content"
	lastTimeField    = "last_time"
	unreadCountField = "unread_count"
)

type MessageService struct {
	messageRepo *repo.MessageRepo
	redisCache  *cache.RedisCache
	userService *UserService
}

func NewMessageService(messageRepo *repo.MessageRepo, redisCache *cache.RedisCache, userService *UserService) *MessageService {
	return &MessageService{messageRepo: messageRepo, redisCache: redisCache, userService: userService}
}

// generateSessionID 生成双向唯一的会话ID
// 规则：小的 ID 在前，大的 ID 在后，用下划线拼接。例如 A=10, B=20，结果为 "10_20"。
// 这样不管谁发给谁，计算出的 SessionID 永远是一致的。
func generateSessionID(userA, userB uint) string {
	if userA > userB {
		userA, userB = userB, userA
	}
	return fmt.Sprintf("%d_%d", userA, userB)
}

// SendMessage 发送消息
func (s *MessageService) SendMessage(ctx context.Context, senderID uint, senderName string, receiverID uint, content string) error {
	// 生成会话ID
	sessionID := generateSessionID(senderID, receiverID)

	message := &model.Message{
		SessionID:  sessionID,
		SenderID:   senderID,
		SenderName: senderName,
		ReceiverID: receiverID,
		Content:    content,
		IsRead:     false,
	}

	// 调用 Repo 层创建消息
	err := s.messageRepo.CreateMessage(ctx, message)
	if err != nil {
		logger.Ctx(ctx).Error("create message failed", logger.Err(err))
		return err
	}

	// 落库后通过向 redis 发布一条 pub/sub 消息，通知 SSEHandler 推送给接收者
	// 私信系统的 SSE 消息结构体
	type IMSseMessage struct {
		Type       string `json:"type"`        // "message" 区别于此前的 "notification"
		UserID     uint   `json:"user_id"`     // SendToUser 需要用这个 user_id 找到对应的连接 channel
		SenderID   uint   `json:"sender_id"`   // 用于前端根据 sender_id 调用接口跳转到私信会话页面
		SenderName string `json:"sender_name"` // 用于前端显示发送者昵称，以及个性化渲染
		Content    string `json:"content"`
		CreatedAt  int64  `json:"created_at"`
	}
	// 构造 SSE 消息
	payload := IMSseMessage{
		Type:       "message",
		UserID:     receiverID,
		SenderID:   senderID,
		SenderName: senderName,
		Content:    fmt.Sprintf("%s: %s", senderName, content), // 前端直接显示这个字段即可
		CreatedAt:  message.CreatedAt.Unix(),
	}

	// 调用 infra 层的公共方法 PublishSSEMessage，向 Redis 发布消息
	err = infra.PublishSSEMessage(ctx, s.redisCache, payload)
	if err != nil {
		logger.Ctx(ctx).Error("publish SSE message to redis failed", logger.Err(err))
	}

	// 异步更新双方的会话列表
	go func() {
		// WithoutCancel 是为了避免在 context 被取消时，导致 Redis 操作被中断，从而保证会话列表的更新能够完成
		// WithTimeout 是为了避免 Redis 操作无限阻塞，设置一个合理的超时时间（例如 10 秒）
		nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()

		score := float64(message.CreatedAt.Unix())

		// 对于接收者
		s.redisCache.ZAddGT(nctx, cache.IMSessionKey(receiverID), []redis.Z{redis.Z{Score: score, Member: senderID}}, cache.IMSessionTTL) // ZAddGT 是为了避免并发时覆盖掉更晚的消息
		// 同时存 Hash: session_detail:B:A (保存最新消息)
		rkey := cache.IMSessionDetailKey(receiverID, senderID)
		s.redisCache.UpdateSessionDetailIncUnread(nctx, rkey, content, message.CreatedAt.Unix(), message.ID, cache.IMSessionDetailTTL) // 利用 lua 脚本原子性地更新未读数和最新消息内容

		// 对于发送者
		s.redisCache.ZAddGT(nctx, cache.IMSessionKey(senderID), []redis.Z{redis.Z{Score: score, Member: receiverID}}, cache.IMSessionTTL)
		// 同时存 Hash: session_detail:A:B
		skey := cache.IMSessionDetailKey(senderID, receiverID)
		s.redisCache.UpdateSessionDetail(nctx, skey, content, message.CreatedAt.Unix(), message.ID, cache.IMSessionDetailTTL) // 同样利用 lua 脚本原子性地更新最新消息内容，不更新未读数
	}()

	return nil
}

// GetHistory 获取聊天历史
func (s *MessageService) GetHistory(ctx context.Context, userID uint, req dto.GetHistoryRequest) (*dto.GetHistoryResponse, error) {
	// 过滤目标用户非法的情况
	if req.TargetID == 0 || req.TargetID == userID {
		return nil, errors.New("invalid target user id")
	}
	// 限制每页条数，防止前端传入过大值导致数据库压力过大
	limit := req.PageSize
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	// 生成会话ID
	sessionID := generateSessionID(userID, req.TargetID)

	// 查库 (limit + 1 用于快速判断是否还有更多消息)
	messages, err := s.messageRepo.GetHistory(ctx, sessionID, req.LastID, limit+1)
	if err != nil {
		logger.Ctx(ctx).Error("get message history failed", logger.Err(err))
		return nil, err
	}

	// 判断 has_more 并截断多余的数据
	hasMore := false
	if len(messages) > limit { // 如果 limit=20，传入limit+1获得了21条（即len(message)=21 > limit=20），则说明还有更多消息
		hasMore = true              // 直接把 hasMore 赋值为 true
		messages = messages[:limit] // 只要前 limit 条 [0, limit)
	}

	// 组装返回游标 (取当前列表的最后一条消息的 ID)
	var nextLastID uint
	if len(messages) > 0 {
		nextLastID = messages[len(messages)-1].ID // repo 里是倒序拉取的，所以最后一条就是最老的那条
	}

	return &dto.GetHistoryResponse{
		Messages:   messages,
		HasMore:    hasMore,
		NextLastID: nextLastID,
	}, nil
}

// ListSessions 获取会话列表
func (s *MessageService) ListSessions(ctx context.Context, userID uint, limit int64) ([]dto.SessionDetail, error) {
	key := cache.IMSessionKey(userID)

	// 限制每页条数，防止前端传入过大值导致 Redis 压力过大
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	// 从 Redis ZSet 获取 targetIDs
	targetIDsStr, err := s.redisCache.ZRange(ctx, key, 0, limit-1, true) // 按照 score 倒序，最新的在前面
	if err != nil && !errors.Is(err, redis.Nil) {
		logger.Ctx(ctx).Error("zrange failed", logger.Err(err))
		return nil, err
	}

	// ZSet 未命中兜底：一旦没拿到，说明整体过期或丢失，直接回源数据库
	var dbMsgs []model.Message
	if len(targetIDsStr) == 0 {
		logger.Ctx(ctx).Info("no target ids found in redis, fallback to db")

		// 从数据库获取最近的聊天消息列表
		dbMsgs, err = s.messageRepo.GetRecentMessagesByUserID(ctx, userID, limit)
		if err != nil {
			logger.Ctx(ctx).Error("fallback to db failed", logger.Err(err))
			return nil, err
		}
		if len(dbMsgs) == 0 {
			// 该用户真的没聊过天，直接返回空数组，别给前端 null
			return []dto.SessionDetail{}, nil
		}

		var zlist []redis.Z
		for _, msg := range dbMsgs {
			// 确定 targetID
			var targetID uint
			if msg.SenderID == userID {
				targetID = msg.ReceiverID
			} else {
				targetID = msg.SenderID
			}

			targetIDsStr = append(targetIDsStr, strconv.FormatUint(uint64(targetID), 10))
			zlist = append(zlist, redis.Z{Score: float64(msg.CreatedAt.Unix()), Member: targetID})
		}

		// 异步回写 ZSet 和 Hash 进行预热，避免主流程再去查库
		go func() {
			nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()

			// 回写 ZSet
			s.redisCache.ZAddGT(nctx, cache.IMSessionKey(userID), zlist, cache.IMSessionTTL)

			// 回写 Hash
			for _, msg := range dbMsgs {
				var targetID uint
				if msg.SenderID == userID {
					targetID = msg.ReceiverID
				} else {
					targetID = msg.SenderID
				}

				detailKey := cache.IMSessionDetailKey(userID, targetID)
				s.redisCache.UpdateSessionDetail(nctx, detailKey, msg.Content, msg.CreatedAt.Unix(), msg.ID, cache.IMSessionDetailTTL)
			}
		}()
	}

	// 统一将 targetIDsStr 转换为 targetIDs 和 keys，并保持顺序绝对一致
	targetIDs := make([]uint, 0, len(targetIDsStr))
	keys := make([]string, 0, len(targetIDsStr))
	for _, tidStr := range targetIDsStr {
		tid, err := strconv.ParseUint(tidStr, 10, 64)
		if err != nil {
			// 容错处理：如果 Redis 里有脏数据，直接跳过
			continue
		}
		// targetIDs 和 keys 的顺序必须和 targetIDsStr 一致，保证后续处理时下标对齐
		targetIDs = append(targetIDs, uint(tid))
		keys = append(keys, cache.IMSessionDetailKey(userID, uint(tid)))
	}

	// 批量拉取 Redis Hash 里的消息详情
	results, missedIndices, err := s.redisCache.BatchHGetAll(ctx, keys) // results[i] 对应 keys[i] 对应 targetIDs[i]
	if err != nil {
		logger.Ctx(ctx).Error("batch hgetall failed", logger.Err(err))
		return nil, err
	}

	// 处理 Redis Hash 未命中的情况
	if len(missedIndices) > 0 {
		// 统一构建 dbMsgMap，彻底告别下标对齐的问题，避免出现越界或错位的情况
		// 无论全量兜底还是部分兜底，都把数据放进 map 里，按 targetID 取，绝对安全
		dbMsgMap := make(map[uint]model.Message)

		if len(dbMsgs) > 0 {
			// 情况 A：刚才 ZSet 未命中，触发全量兜底，手里已经有 dbMsgs 了
			for _, msg := range dbMsgs {
				if msg.SenderID == userID {
					dbMsgMap[msg.ReceiverID] = msg
				} else {
					dbMsgMap[msg.SenderID] = msg
				}
			}
		} else {
			// 情况 B：ZSet 命中了，但是部分 Hash 过期了，只查缺失的那部分数据
			var missedSessionIDs []string
			for _, idx := range missedIndices {
				tid := targetIDs[idx] // targetIDs 和 keys 是严格对齐的，idx 是从 keys 里来的，所以可以直接用 idx 去 targetIDs 里取
				missedSessionIDs = append(missedSessionIDs, generateSessionID(userID, tid))
			}

			// 单独查库
			dbMessages, err := s.messageRepo.GetLastMessagesBySessionIDs(ctx, missedSessionIDs)
			if err == nil {
				for _, msg := range dbMessages {
					if msg.SenderID == userID {
						dbMsgMap[msg.ReceiverID] = msg
					} else {
						dbMsgMap[msg.SenderID] = msg
					}
				}
			} else {
				logger.Ctx(ctx).Error("fallback to db failed", logger.Err(err))
			}
		}

		// 遍历所有 Miss 的索引，用 targetID 从 dbMsgMap 里取，安全可靠
		for _, idx := range missedIndices {
			tid := targetIDs[idx]
			msg, ok := dbMsgMap[tid]
			if !ok {
				continue // 数据库里也没有（可能被删除了），跳过
			}

			// 回写 Redis，防止下次继续穿透
			detailKey := cache.IMSessionDetailKey(userID, tid)
			detailData := map[string]interface{}{
				lastContentField: msg.Content,
				lastTimeField:    msg.CreatedAt.Unix(),
				unreadCountField: 0,
			}
			s.redisCache.HMSet(ctx, detailKey, detailData)
			s.redisCache.Expire(ctx, detailKey, cache.IMSessionDetailTTL)

			// 覆盖到 results 中，让它和命中的一样被处理
			results[idx] = map[string]string{
				lastContentField: msg.Content,
				lastTimeField:    strconv.FormatInt(msg.CreatedAt.Unix(), 10),
				unreadCountField: "0",
			}
		}
	}

	// 统一获取所有 targetID 的最新昵称
	targetNames, err := s.userService.BatchGetUserNames(ctx, targetIDs)
	if err != nil {
		logger.Ctx(ctx).Error("batch get user names failed", logger.Err(err))
		targetNames = make(map[uint]string) // 容错：防 panic
	}

	// 最终组装 DTO
	sessions := make([]dto.SessionDetail, 0, len(targetIDs))
	for i, tid := range targetIDs {
		detail := results[i]
		if len(detail) == 0 {
			continue // 依然为空，说明真的没数据
		}

		createdAt, err := strconv.ParseInt(detail[lastTimeField], 10, 64)
		if err != nil {
			logger.Ctx(ctx).Error("parse created at failed", logger.Err(err))
			continue
		}

		// 查不到名字给默认值
		name := targetNames[tid]
		if name == "" {
			name = "未知用户"
		}

		// 这里把 UnreadCount 也组装进去了，配合前端红点
		unreadCount, _ := strconv.ParseInt(detail[unreadCountField], 10, 64)

		sessions = append(sessions, dto.SessionDetail{
			TargetID:    tid,
			TargetName:  name,
			Content:     detail[lastContentField],
			CreatedAt:   createdAt,
			UnreadCount: unreadCount,
		})
	}

	return sessions, nil
}
