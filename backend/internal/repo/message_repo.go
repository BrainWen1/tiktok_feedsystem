package repo

import (
	"context"
	"feedsystem/internal/model"

	"gorm.io/gorm"
)

type MessageRepo struct {
	db *gorm.DB
}

func NewMessageRepo(db *gorm.DB) *MessageRepo {
	return &MessageRepo{db: db}
}

// CreateMessage 新建消息
func (r *MessageRepo) CreateMessage(ctx context.Context, message *model.Message) error {
	return r.db.WithContext(ctx).Create(message).Error
}

// GetHistory 获取两人之间的历史消息
func (r *MessageRepo) GetHistory(ctx context.Context, sessionID string, lastID uint, limit int) ([]model.Message, error) {
	var messages []model.Message

	// 直接走 (session_id, id) 的联合索引
	query := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("session_id = ?", sessionID)

	// 如果 lastID > 0，说明是拉取下一页，需要加上 id < lastID 的条件
	if lastID > 0 {
		query = query.Where("id < ?", lastID)
	}

	// 查库
	// 倒序拉取的，所以 messages 的最后一条就是最老的那条
	err := query.Order("id DESC").Limit(limit).Find(&messages).Error
	if err != nil {
		return nil, err
	}

	if len(messages) == 0 {
		// 如果没有消息，直接返回空数组，避免返回 nil 给前端
		return []model.Message{}, nil
	}
	return messages, nil
}

// GetLastMessagesBySessionIDs 批量获取多个会话的最后一条消息
func (r *MessageRepo) GetLastMessagesBySessionIDs(ctx context.Context, sessionIDs []string) ([]model.Message, error) {
	// 如果没有会话 ID，直接返回空数组
	if len(sessionIDs) == 0 {
		return []model.Message{}, nil
	}

	// 子查询：按 session_id 分组，找出每个会话最大的消息 ID (也就是最后一条消息)
	subQuery := r.db.WithContext(ctx).Model(&model.Message{}).
		Select("MAX(id)").
		Where("session_id IN ?", sessionIDs).
		Group("session_id")

	// 主查询：根据找出来的最大 ID，查出完整的消息记录
	var messages []model.Message
	err := r.db.WithContext(ctx).
		Where("id IN (?)", subQuery).
		Find(&messages).Error

	return messages, err
}

// GetRecentMessagesByUserID 获取用户最近的聊天消息列表
func (r *MessageRepo) GetRecentMessagesByUserID(ctx context.Context, userID uint, limit int64) ([]model.Message, error) {
	var messages []model.Message

	// 核心思路：
	// 1. 用 UNION ALL 和 GROUP BY 找出用户最近聊天的 target_id，以及该会话最大的 message id。
	// 2. 把这个结果作为临时表 t，再 JOIN 回 messages 表，拿到这条消息的具体 created_at 和 content。
	// 3. 按 id 降序取 limit 条。
	sql := `
		SELECT m.* 
		FROM messages m
		INNER JOIN (
			SELECT target_id, MAX(id) AS max_id 
			FROM (
				SELECT receiver_id AS target_id, id FROM messages WHERE sender_id = ?
				UNION ALL
				SELECT sender_id AS target_id, id FROM messages WHERE receiver_id = ?
			) AS all_msgs
			GROUP BY target_id
			ORDER BY max_id DESC
			LIMIT ?
		) AS t ON m.id = t.max_id
		ORDER BY m.id DESC
	`

	err := r.db.WithContext(ctx).Raw(sql, userID, userID, limit).Scan(&messages).Error
	return messages, err
}
