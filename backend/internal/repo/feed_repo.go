package repo

import (
	"context"
	"feedsystem/internal/model"
	"time"

	"gorm.io/gorm"
)

// FeedRepo 视频流仓库，只负责 feed 相关查询的原始 SQL。
type FeedRepo struct {
	db *gorm.DB
}

// NewFeedRepo 创建视频流仓库
func NewFeedRepo(db *gorm.DB) *FeedRepo {
	return &FeedRepo{db: db}
}

// ListLatest 获取最新视频流。
// lastCreatedAt 直接使用 Go time.Time，由 GORM 绑定成 DATETIME，
// 禁止转 Unix 秒后与 DATETIME 列比较，否则会触发隐式类型转换导致索引失效和翻页丢数据。
func (r *FeedRepo) ListLatest(ctx context.Context, lastCreatedAt time.Time, lastVid uint, limit int) ([]model.Video, error) {
	var videos []model.Video
	db := r.db.WithContext(ctx)
	q := db.Order("created_at DESC, id DESC")

	// 非首页，带上复合游标过滤
	if lastVid != 0 {
		q = q.Where("(created_at, id) < (?, ?)", lastCreatedAt, lastVid)
	}

	err := q.Limit(limit).Find(&videos).Error
	return videos, err
}

// ListByFollowing 获取关注用户发布视频流。
// 直接 JOIN social 表按 follower_id 过滤关注关系，不再接收全量关注 ID 数组，
// 避免关注量大时 SQL 占位符与内存膨胀；social 上的 (follower_id, blogger_id) 唯一索引可加速 JOIN。
// 复合游标为 (created_at, id)，按创建时间与主键同时降序，保证翻页顺序稳定。
func (r *FeedRepo) ListByFollowing(ctx context.Context, followerID uint, lastCreatedAt time.Time, lastVid uint, limit int) ([]model.Video, error) {
	var videos []model.Video
	db := r.db.WithContext(ctx)
	q := db.Model(&model.Video{}).
		Joins("JOIN socials ON socials.blogger_id = videos.author_id AND socials.follower_id = ?", followerID).
		Select("videos.*").
		Order("videos.created_at DESC, videos.id DESC")

	if lastVid != 0 {
		q = q.Where("(videos.created_at, videos.id) < (?, ?)", lastCreatedAt, lastVid)
	}

	err := q.Limit(limit).Find(&videos).Error
	return videos, err
}

// ListByPopularity 根据热度分页获取热门视频。
// 复合游标为 (popularity, id)，热度并列时用主键决胜。
func (r *FeedRepo) ListByPopularity(ctx context.Context, lastPopularity int64, lastVid uint, limit int) ([]model.Video, error) {
	var videos []model.Video
	db := r.db.WithContext(ctx)
	q := db.Order("popularity DESC, id DESC")

	if lastVid != 0 {
		q = q.Where("(popularity, id) < (?, ?)", lastPopularity, lastVid)
	}

	err := q.Limit(limit).Find(&videos).Error
	return videos, err
}

// ListByLikesCount 按点赞数量降序分页查询视频。
// 复合游标为 (likes_count, id)，点赞数并列时用主键决胜。
func (r *FeedRepo) ListByLikesCount(ctx context.Context, lastLikeCount int64, lastVid uint, limit int) ([]model.Video, error) {
	var videos []model.Video
	db := r.db.WithContext(ctx)
	q := db.Order("likes_count DESC, id DESC")

	if lastVid != 0 {
		q = q.Where("(likes_count, id) < (?, ?)", lastLikeCount, lastVid)
	}

	err := q.Limit(limit).Find(&videos).Error
	return videos, err
}

// ListByTag 根据标签筛选视频。
// 通过 video_tag 关联表查询，tag 命中联合索引 (tag, video_id)，避免 FIND_IN_SET 全表扫描。
func (r *FeedRepo) ListByTag(ctx context.Context, tag string, lastCreatedAt time.Time, lastVid uint, limit int) ([]model.Video, error) {
	var videos []model.Video
	db := r.db.WithContext(ctx)
	q := db.Model(&model.Video{}).
		Joins("JOIN video_tag ON video_tag.video_id = videos.id").
		Where("video_tag.tag = ?", tag).
		Select("videos.*").
		Order("videos.created_at DESC, videos.id DESC")

	if lastVid != 0 {
		q = q.Where("(videos.created_at, videos.id) < (?, ?)", lastCreatedAt, lastVid)
	}

	err := q.Limit(limit).Find(&videos).Error
	return videos, err
}
