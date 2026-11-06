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
