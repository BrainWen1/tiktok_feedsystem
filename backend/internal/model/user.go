package model

import "time"

// User 用户数据库模型
type User struct {
	ID        uint      `gorm:"primaryKey" json:"id"`                          // 用户ID
	Username  string    `gorm:"unique;type:varchar(128)" json:"username"`      // 用户名
	Password  string    `json:"-"`                                             // bcrypt哈希后的密码，不返回json
	AvatarURL string    `gorm:"type:varchar(512)" json:"avatar_url,omitempty"` // 用户头像URL
	Bio       string    `gorm:"type:text" json:"bio,omitempty"`                // 用户简介，改为text支持长文本
	CreatedAt time.Time `gorm:"autoCreateTime"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}
