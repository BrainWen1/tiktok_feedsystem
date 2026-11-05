package model

import "time"

type Notification struct {
	ID uint `gorm:"primaryKey;autoIncrement"`

	// 唯一索引：ActorID + ReceiverID + Type + TargetID，避免重复通知
	ActorID    uint   `gorm:"not null;uniqueIndex:uk_ignore"`
	ReceiverID uint   `gorm:"not null;uniqueIndex:uk_ignore;index:idx_receiver_read_time,priority:1"`
	Type       string `gorm:"type:varchar(20);not null;uniqueIndex:uk_ignore"`
	TargetID   uint   `gorm:"not null;uniqueIndex:uk_ignore"`

	Content string `gorm:"type:text;not null"`

	// 复合索引：ReceiverID + IsRead + CreatedAt，用于快速查询某个用户的未读通知，并按时间排序
	IsRead    bool      `gorm:"not null;default:false;index:idx_receiver_read_time,priority:2"`
	CreatedAt time.Time `gorm:"autoCreateTime;index:idx_receiver_read_time,priority:3"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}
