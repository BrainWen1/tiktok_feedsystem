package repo

import (
	"context"
	"feedsystem/internal/model"

	"gorm.io/gorm"
)

type NotificationRepo struct {
	db *gorm.DB
}

func NewNotificationRepo(db *gorm.DB) *NotificationRepo {
	return &NotificationRepo{db: db}
}

// CreateNotification 创建一条新的通知记录
func (r *NotificationRepo) CreateNotification(ctx context.Context, notif *model.Notification) error {
	return r.db.WithContext(ctx).Create(notif).Error
}

// CountUnreadByReceiverID 统计某个用户未读通知的数量
func (r *NotificationRepo) CountUnreadByReceiverID(ctx context.Context, userID uint) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Notification{}).Where("receiver_id = ? AND is_read = false", userID).Count(&count).Error
	return count, err
}
