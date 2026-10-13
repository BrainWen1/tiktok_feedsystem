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

// ListLatest 最新视频，按创建时间倒序，游标分页 lastVid="" 第一页
func (r *FeedRepo) ListLatest(ctx context.Context, lastVid string, limit int) ([]model.Video, error) {
	var list []model.Video
	q := r.db.WithContext(ctx).Model(&model.Video{}).Order("created_at DESC")
	if lastVid != "" {
		q = q.Where("id < ?", lastVid)
	}
	err := q.Limit(limit).Find(&list).Error
	return list, err
}

// ListByAuthorUids 根据一批作者uid，查询这些作者发布的视频（用于关注流 listByFollowing）
func (r *FeedRepo) ListByAuthorUids(ctx context.Context, authorUidList []uint, lastVid string, limit int) ([]model.Video, error) {
	var list []model.Video
	q := r.db.WithContext(ctx).Model(&model.Video{}).Where("author_id IN ?", authorUidList).Order("created_at DESC")
	if lastVid != "" {
		q = q.Where("id < ?", lastVid)
	}
	err := q.Limit(limit).Find(&list).Error
	return list, err
}

// ListByPopularity 综合热门，示例：created_at + 点赞权重，你后续可以自己调整排序表达式
func (r *FeedRepo) ListByPopularity(ctx context.Context, lastVid string, limit int) ([]model.Video, error) {
	var list []model.Video
	// 示例：先按创建时间倒序，后续可以改成结合redis点赞热度的逻辑
	q := r.db.WithContext(ctx).Model(&model.Video{}).Order("created_at DESC")
	if lastVid != "" {
		q = q.Where("id < ?", lastVid)
	}
	err := q.Limit(limit).Find(&list).Error
	return list, err
}

// ListByLikeCount 按视频点赞总数排序榜单（注意：点赞数存在Redis，这里只是演示；真实生产不要直接DB order by，后面可以改造ZSet）
func (r *FeedRepo) ListByLikeCount(ctx context.Context, lastVid string, limit int) ([]model.Video, error) {
	var list []model.Video
	// 提示：demo阶段简单实现；真实项目应该读取redis zset拿到vid有序列表，再in查询vid
	q := r.db.WithContext(ctx).Model(&model.Video{}).Order("created_at DESC")
	if lastVid != "" {
		q = q.Where("id < ?", lastVid)
	}
	err := q.Limit(limit).Find(&list).Error
	return list, err
}

// ListByTag 根据标签筛选视频
func (r *FeedRepo) ListByTag(ctx context.Context, tag string, lastVid string, limit int) ([]model.Video, error) {
	var list []model.Video
	q := r.db.WithContext(ctx).Model(&model.Video{}).Where("tag = ?", tag).Order("created_at DESC")
	if lastVid != "" {
		q = q.Where("id < ?", lastVid)
	}
	err := q.Limit(limit).Find(&list).Error
	return list, err
}
