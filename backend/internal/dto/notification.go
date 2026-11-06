package dto

import (
	"feedsystem/internal/model"
	"time"
)

// 获取通知列表请求
type ListNotificationsRequest struct {
	PageSize int       `json:"page_size" form:"page_size"`
	LastTime time.Time `json:"last_time" form:"last_time"`
	LastID   uint      `json:"last_id" form:"last_id"`
}

// 获取通知列表响应
type ListNotificationsResponse struct {
	Notifications []model.Notification `json:"notifications"`

	// 供前端请求下一页：
	NextLastTime *time.Time `json:"next_last_time,omitempty"`
	NextLastID   *uint      `json:"next_last_id,omitempty"`
}

// 标记单条通知为已读请求
type MarkAsReadRequest struct {
	NotificationID uint `json:"notification_id" binding:"required"`
}
