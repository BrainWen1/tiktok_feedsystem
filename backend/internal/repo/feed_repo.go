package repo

import (
	"context"
	"feedsystem/internal/model"

	"gorm.io/gorm"
)

type FeedRepo struct {
	db *gorm.DB
}

func NewFeedRepo(db *gorm.DB) *FeedRepo {
	return &FeedRepo{db: db}
}

// ListLatest 获取最新视频流
func (r *FeedRepo) ListLatest(ctx context.Context, lastCreatedAt int64, lastVid string, limit int) ([]model.Video, error) {
	var videos []model.Video
	db := r.db.WithContext(ctx)
	q := db.Order("created_at DESC, id DESC")

	// 非首页，带上复合游标过滤
	if lastVid != "" {
		q = q.Where("(created_at, id) < (?, ?)", lastCreatedAt, lastVid)
	}

	err := q.Limit(limit).Find(&videos).Error
	return videos, err
}

// ListByFollowing 获取关注用户发布视频流
func (r *FeedRepo) ListByFollowing(ctx context.Context, followUids []uint, lastCreatedAt int64, lastVid string, limit int) ([]model.Video, error) {
	var videos []model.Video
	db := r.db.WithContext(ctx)
	q := db.Where("author_id IN ?", followUids).Order("created_at DESC, id DESC")

	if lastVid != "" {
		q = q.Where("(created_at, id) < (?, ?)", lastCreatedAt, lastVid)
	}

	err := q.Limit(limit).Find(&videos).Error
	return videos, err
}

// ListByPopularity 根据热度分页获取热门视频
func (r *FeedRepo) ListByPopularity(ctx context.Context, lastPopularity int64, lastVid string, limit int) ([]model.Video, error) {
	var videos []model.Video
	db := r.db.WithContext(ctx)
	q := db.Order("popularity DESC, id DESC")

	if lastVid != "" {
		q = q.Where("(popularity, id) < (?, ?)", lastPopularity, lastVid)
	}

	err := q.Limit(limit).Find(&videos).Error
	return videos, err
}

// ListLikesCount 按点赞数量降序分页
func (r *FeedRepo) ListByLikesCount(ctx context.Context, lastLikeCount int64, lastVid string, limit int) ([]model.Video, error) {
	var videos []model.Video
	db := r.db.WithContext(ctx)
	q := db.Order("likes_count DESC, id DESC")

	if lastVid != "" {
		q = q.Where("(likes_count, id) < (?, ?)", lastLikeCount, lastVid)
	}

	err := q.Limit(limit).Find(&videos).Error
	return videos, err
}

// ListByTag 根据标签筛选视频
func (r *FeedRepo) ListByTag(ctx context.Context, tag string, lastCreatedAt int64, lastVid string, limit int) ([]model.Video, error) {
	var videos []model.Video
	db := r.db.WithContext(ctx)
	q := db.Where("FIND_IN_SET(?, tags)", tag).Order("created_at DESC, id DESC")

	if lastVid != "" {
		q = q.Where("(created_at, id) < (?, ?)", lastCreatedAt, lastVid)
	}

	err := q.Limit(limit).Find(&videos).Error
	return videos, err
}
