package model

import "time"

type Social struct {
	ID uint `gorm:"primaryKey"`
	// 唯一约束，确保同一个用户不能重复关注同一个博主
	FollowerID uint `gorm:"not null;index:idx_social_follower;uniqueIndex:idx_social_follower_blogger"`
	BloggerID  uint `gorm:"not null;index:idx_social_blogger;uniqueIndex:idx_social_follower_blogger"`

	CreatedAt time.Time `gorm:"autoCreateTime"`
}
