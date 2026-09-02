package model

import "time"

type Like struct {
	ID uint `gorm:"primaryKey"`

	UserID  uint `gorm:"not null;index:idx_user_video,unique"`
	VideoID uint `gorm:"not null;index:idx_user_video,unique"`

	CreatedAt time.Time
}
