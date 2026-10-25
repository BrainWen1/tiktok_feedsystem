package repo

import (
	"context"
	"feedsystem/internal/model"
	"fmt"
	"time"

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

// CountByVideo 统计视频的点赞总数，供缓存禁用时降级查询
func (r *LikeRepo) CountByVideo(ctx context.Context, vid uint) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Like{}).Where("video_id = ?", vid).Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

// FindUserIDsByVideo 查询点赞该视频的全部用户ID，用于点赞明细 ZSet 过期后的缓存重建
func (r *LikeRepo) FindUserIDsByVideo(ctx context.Context, vid uint) ([]uint, error) {
	var userIDs []uint
	err := r.db.WithContext(ctx).Model(&model.Like{}).
		Where("video_id = ?", vid).
		Pluck("user_id", &userIDs).Error
	if err != nil {
		return nil, err
	}
	return userIDs, nil
}

// FindLikedVideoPage 按点赞时间 + 视频ID复合游标分页查询用户点赞记录。
// 返回完整 Like 记录，service 层据此同时拿到 video_id 和 created_at 生成下一页游标。
func (r *LikeRepo) FindLikedVideoPage(ctx context.Context, uid uint, lastCreatedAt time.Time, lastVid uint, pageSize int) ([]model.Like, int64, error) {
	var likes []model.Like
	var total int64

	if uid == 0 {
		return nil, 0, fmt.Errorf("find liked videos: empty user id: %w", ErrInvalidArgument)
	}

	if err := r.db.WithContext(ctx).Model(&model.Like{}).
		Where("user_id = ?", uid).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	q := r.db.WithContext(ctx).Model(&model.Like{}).Where("user_id = ?", uid)
	if lastVid != 0 {
		q = q.Where("(created_at, video_id) < (?, ?)", lastCreatedAt, lastVid)
	}
	if err := q.Order("created_at DESC, video_id DESC"). // 按点赞时间倒序，video_id 兜底保证顺序稳定
								Limit(pageSize).
								Find(&likes).Error; err != nil {
		return nil, 0, err
	}

	return likes, total, nil
}
