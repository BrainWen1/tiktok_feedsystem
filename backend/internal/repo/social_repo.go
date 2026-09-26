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

// ListBloggers 获取用户的关注列表，支持分页和按关注时间排序。
func (r *SocialRepo) ListBloggers(ctx context.Context, followerID, pageNum, pageSize uint, orderByFollowTime bool) ([]*model.Social, int64, error) {
	var socials []*model.Social
	query := r.db.WithContext(ctx).Model(&model.Social{}).Where(&model.Social{FollowerID: followerID})

	// 获取总数
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 排序
	if orderByFollowTime {
		// 按关注时间降序排序
		query = query.Order("created_at DESC")
	} else {
		// 按博主ID升序排序
		query = query.Order("blogger_id ASC")
	}

	// 分页
	offset := (pageNum - 1) * pageSize
	if err := query.Offset(int(offset)).Limit(int(pageSize)).Find(&socials).Error; err != nil {
		return nil, 0, err
	}

	return socials, total, nil
}

// ListAllBloggers 获取用户的全部关注列表，按关注时间排序。
func (r *SocialRepo) ListAllBloggers(ctx context.Context, followerID uint, orderByFollowTime bool) ([]*model.Social, error) {
	var socials []*model.Social
	query := r.db.WithContext(ctx).Model(&model.Social{}).Where(&model.Social{FollowerID: followerID})

	if orderByFollowTime {
		query = query.Order("created_at DESC")
	} else {
		query = query.Order("blogger_id ASC")
	}

	if err := query.Find(&socials).Error; err != nil {
		return nil, err
	}

	return socials, nil
}

// FindByFollowerAndBlogger 查找特定关注关系。
func (r *SocialRepo) FindByFollowerAndBlogger(ctx context.Context, followerID, bloggerID uint) (*model.Social, error) {
	var social model.Social
	err := r.db.WithContext(ctx).
		Where(&model.Social{FollowerID: followerID, BloggerID: bloggerID}).
		First(&social).Error
	if err != nil {
		return nil, err
	}
	return &social, nil
}

// ListFollowers 获取用户粉丝列表
func (r *SocialRepo) ListFollowers(ctx context.Context, bloggerID, pageNum, pageSize uint, orderByFollowTime bool) ([]*model.Social, int64, error) {
	var socials []*model.Social
	var total int64
	query := r.db.WithContext(ctx).Model(&model.Social{}).Where(&model.Social{BloggerID: bloggerID})

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if orderByFollowTime {
		query = query.Order("created_at DESC")
	} else {
		query = query.Order("follower_id ASC")
	}
	offset := (pageNum - 1) * pageSize
	if err := query.Offset(int(offset)).Limit(int(pageSize)).Find(&socials).Error; err != nil {
		return nil, 0, err
	}
	return socials, total, nil
}

// ListAllFollowers 获取用户的全部粉丝列表，按关注时间排序。
func (r *SocialRepo) ListAllFollowers(ctx context.Context, bloggerID uint, orderByFollowTime bool) ([]*model.Social, error) {
	var socials []*model.Social
	query := r.db.WithContext(ctx).Model(&model.Social{}).Where(&model.Social{BloggerID: bloggerID})

	if orderByFollowTime {
		query = query.Order("created_at DESC")
	} else {
		query = query.Order("follower_id ASC")
	}

	if err := query.Find(&socials).Error; err != nil {
		return nil, err
	}

	return socials, nil
}
