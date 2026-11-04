package model

import "time"

type Notification struct {
	ID         uint      `gorm:"primaryKey;autoIncrement"`
	ActorID    uint      `gorm:"not null;index"`         // 触发通知的用户ID
	ReceiverID uint      `gorm:"not null;index"`         // 接收通知的用户ID
	Type       string    `gorm:"not null;index"`         // 通知类型，如 "like", "comment", "follow"
	TargetID   uint      `gorm:"not null;index"`         // 相关目标ID，如视频ID、评论ID等
	Content    string    `gorm:"type:text;not null"`     // 通知内容，如“用户A点赞了你的视频”、"用户A评论了你的视频"
	IsRead     bool      `gorm:"not null;default:false"` // 是否已读
	CreatedAt  time.Time `gorm:"autoCreateTime"`         // 创建时间
	UpdatedAt  time.Time `gorm:"autoUpdateTime"`         // 更新时间
}
