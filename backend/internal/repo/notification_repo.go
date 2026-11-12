package repo

import (
	"context"
	"errors"
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

// FindLatestByTuple 查询某个业务键下最新的一条通知（包含 cancelled 的历史记录）。
// 不存在时返回 (nil, nil)，由调用方决定是首次通知还是取消后重新触发。
func (r *NotificationRepo) FindLatestByTuple(ctx context.Context, actorID, receiverID uint, typ string, targetID uint) (*model.Notification, error) {
	var notif model.Notification
	err := r.db.WithContext(ctx).
		Where("actor_id = ? AND receiver_id = ? AND type = ? AND target_id = ?", actorID, receiverID, typ, targetID).
		Order("id DESC").
		Limit(1).
		Take(&notif).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &notif, nil
}

// CancelActiveByTuple 把某个业务键下最新的 active 通知置为 cancelled，
// 同时清空 ActiveKey（释放唯一约束）并将该通知标记为已读。
// 该业务键没有 active 记录时返回 (nil, nil)，调用方按幂等处理。
func (r *NotificationRepo) CancelActiveByTuple(ctx context.Context, actorID, receiverID uint, typ string, targetID uint) (*model.Notification, error) {
	var notif model.Notification
	err := r.db.WithContext(ctx).
		Where("actor_id = ? AND receiver_id = ? AND type = ? AND target_id = ? AND status = ?",
			actorID, receiverID, typ, targetID, model.NotificationStatusActive).
		Order("id DESC").
		Limit(1).
		Take(&notif).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// 带上 status = active 条件，避免并发下重复取消已经变更的记录。
	result := r.db.WithContext(ctx).Model(&model.Notification{}).
		Where("id = ? AND status = ?", notif.ID, model.NotificationStatusActive).
		Updates(map[string]interface{}{
			"status":     model.NotificationStatusCancelled,
			"active_key": nil,
			"is_read":    true,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		// 已被其它并发请求取消，按幂等成功处理
		return nil, nil
	}

	notif.Status = model.NotificationStatusCancelled
	notif.ActiveKey = nil
	notif.IsRead = true
	return &notif, nil
}

// CountUnreadByReceiverID 统计某个用户未读通知的数量。
// 已取消的通知不参与未读统计，避免历史记录把红点数字撑大。
func (r *NotificationRepo) CountUnreadByReceiverID(ctx context.Context, userID uint) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Notification{}).
		Where("receiver_id = ? AND is_read = false AND status = ?", userID, model.NotificationStatusActive).
		Count(&count).Error
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
