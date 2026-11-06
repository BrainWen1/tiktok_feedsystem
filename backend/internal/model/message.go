package model

import "time"

type Message struct {
	ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	SenderID   uint      `gorm:"not null" json:"sender_id"`
	SenderName string    `gorm:"not null" json:"sender_name"` // 冗余字段，用于接入通知系统时拼接 notification content
	ReceiverID uint      `gorm:"not null" json:"receiver_id"`
	Content    string    `gorm:"type:text;not null" json:"content"`
	IsRead     bool      `gorm:"default:false" json:"is_read"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
}
