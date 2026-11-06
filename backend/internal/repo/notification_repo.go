package repo

import (
	"context"
	"feedsystem/internal/model"
	"time"

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

// ListByCursor 获取分页列表
func (r *NotificationRepo) ListByCursor(ctx context.Context, userID uint, lastTime time.Time, lastID uint, limit int) ([]model.Notification, error) {
	// 复合游标为 (created_at, id)，按创建时间与主键同时降序，保证翻页顺序稳定。
	query := r.db.WithContext(ctx).Where("receiver_id = ?", userID)
	if !lastTime.IsZero() {
		query = query.Where("(created_at < ?) OR (created_at = ? AND id < ?)", lastTime, lastTime, lastID)
	}
	// 查询结果按 created_at DESC, id DESC 排序，保证最新的通知在前面
	var list []model.Notification
	err := query.Order("created_at DESC, id DESC").Limit(limit).Find(&list).Error
	return list, err
}

// MarkRead 标记单条已读
func (r *NotificationRepo) MarkRead(ctx context.Context, userID uint, notificationID uint) (bool, error) {
	// is_read = false确保只更新未读的，返回 RowsAffected 用于判断是否真的改动了
	result := r.db.WithContext(ctx).Model(&model.Notification{}).
		Where("id = ? AND receiver_id = ? AND is_read = false", notificationID, userID). // 不能省略 userID，避免水平越权
		Update("is_read", true)
	return result.RowsAffected > 0, result.Error
}

// MarkAllRead 标记全部已读
func (r *NotificationRepo) MarkAllRead(ctx context.Context, userID uint) error {
	return r.db.WithContext(ctx).Model(&model.Notification{}).
		Where("receiver_id = ? AND is_read = false", userID).
		Update("is_read", true).Error
}
