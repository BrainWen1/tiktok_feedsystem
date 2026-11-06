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
