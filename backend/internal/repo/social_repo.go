package repo

import (
	"context"
	"feedsystem/internal/model"

	"gorm.io/gorm"
)

type SocialRepo struct {
	db *gorm.DB
}

func NewSocialRepo(db *gorm.DB) *SocialRepo {
	return &SocialRepo{db: db}
}

// CreateFollow 创建关注关系。
func (r *SocialRepo) CreateFollow(ctx context.Context, followerID, bloggerID uint) error {
	social := &model.Social{
		FollowerID: followerID,
		BloggerID:  bloggerID,
	}
	return r.db.WithContext(ctx).Create(social).Error
}

// DeleteFollow 删除关注关系，并返回是否真的删除到记录。
func (r *SocialRepo) DeleteFollow(ctx context.Context, followerID, bloggerID uint) (bool, error) {
	tx := r.db.WithContext(ctx).
		Where("follower_id = ? AND blogger_id = ?", followerID, bloggerID).
		Delete(&model.Social{})
	if tx.Error != nil {
		return false, tx.Error
	}
	return tx.RowsAffected > 0, nil // 返回是否真的删除到记录
}

// IsFollowing 检查用户是否已经关注了某个博主。
func (r *SocialRepo) IsFollowing(ctx context.Context, followerID, bloggerID uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&model.Social{}).
		Where("follower_id = ? AND blogger_id = ?", followerID, bloggerID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
