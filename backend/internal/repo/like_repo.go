package repo

import (
	"context"
	"feedsystem/internal/model"

	"gorm.io/gorm"
)

type LikeRepo struct {
	db *gorm.DB
}

func NewLikeRepo(db *gorm.DB) *LikeRepo {
	return &LikeRepo{db: db}
}

// CreateLike 创建点赞记录
func (r *LikeRepo) CreateLike(ctx context.Context, uid, vid uint) error {
	like := &model.Like{
		UserID:  uid,
		VideoID: vid,
	}
	return r.db.WithContext(ctx).Create(like).Error
}

// DeleteLike 删除点赞记录
func (r *LikeRepo) DeleteLike(ctx context.Context, uid, vid uint) error {
	return r.db.WithContext(ctx).Where("user_id = ? AND video_id = ?", uid, vid).Delete(&model.Like{}).Error
}

// IsLiked 检查用户是否点赞了视频
func (r *LikeRepo) IsLiked(ctx context.Context, uid, vid uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Like{}).Where("user_id = ? AND video_id = ?", uid, vid).Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// FindVidsByUserID 根据用户id和分页信息查询用户点赞过的视频id列表
func (r *LikeRepo) FindVidsByUserID(ctx context.Context, uid, page_num, page_size uint) ([]uint, int64, error) {
	var videoIDs []uint
	var total int64
	var offset = (page_num - 1) * page_size

	err := r.db.WithContext(ctx).Model(&model.Like{}).
		Where(model.Like{UserID: uid}).
		Count(&total).
		Order("created_at DESC"). // 按点赞时间倒序排列
		Offset(int(offset)).
		Limit(int(page_size)).
		Pluck("video_id", &videoIDs).Error

	if err != nil {
		return nil, 0, err
	}
	return videoIDs, total, nil
}
