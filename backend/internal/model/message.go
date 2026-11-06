package model

import "time"

type Message struct {
	// 在主键 ID 上追加一个联合索引，优先级为 2
	ID uint `gorm:"primaryKey;autoIncrement;index:idx_session_msg,priority:2" json:"id"`

	// SessionID 是联合索引的第一列，优先级为 1
	SessionID string `gorm:"type:varchar(32);not null;index:idx_session_msg,priority:1" json:"-"` // json:"-" 表示不返回给前端，前端不需要知道这个字段

	SenderID   uint      `gorm:"not null" json:"sender_id"`
	SenderName string    `gorm:"type:varchar(128);not null" json:"sender_name"`
	ReceiverID uint      `gorm:"not null" json:"receiver_id"`
	Content    string    `gorm:"type:text;not null" json:"content"`
	IsRead     bool      `gorm:"not null;default:false" json:"is_read"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
}
