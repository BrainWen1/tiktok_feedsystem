package model

import "time"

type Comment struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	VideoID   uint      `gorm:"index" json:"video_id"`            // 关联视频ID
	UserID    uint      `gorm:"index" json:"user_id"`             // 关联用户ID
	UserName  string    `gorm:"index" json:"user_name"`           // 冗余关联用户名
	ParentID  uint      `gorm:"index;default:0" json:"parent_id"` // 父评论ID，0表示顶级评论
	Content   string    `gorm:"type:text" json:"content"`         // 评论内容
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}
