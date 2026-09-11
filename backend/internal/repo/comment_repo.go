package repo

import (
	"context"
	"feedsystem/internal/model"
	"time"

	"gorm.io/gorm"
)

type CommentRepo struct {
	db *gorm.DB
}

func NewCommentRepo(db *gorm.DB) *CommentRepo {
	return &CommentRepo{db: db}
}

func (r *CommentRepo) CreateComment(ctx context.Context, comment *model.Comment) error {
	return r.db.WithContext(ctx).Create(comment).Error
}

func (r *CommentRepo) DeleteComment(ctx context.Context, commentID uint) error {
	return r.db.WithContext(ctx).Delete(&model.Comment{}, commentID).Error
}

func (r *CommentRepo) FindByID(ctx context.Context, commentID uint) (*model.Comment, error) {
	var comment model.Comment
	if err := r.db.WithContext(ctx).First(&comment, commentID).Error; err != nil {
		return nil, err
	}
	return &comment, nil
}

// ListByVideoCursor 复合游标分页查询
func (r *CommentRepo) ListByVideoCursor(ctx context.Context, vid uint, lastTime *int64, lastID *uint, pageSize int) ([]*model.Comment, error) {
	db := r.db.WithContext(ctx).
		Where("video_id = ?", vid).
		Order("created_at desc, id desc").
		Limit(pageSize)

	// 如果传入游标，追加复合条件，解决同一时间戳多条评论丢数据
	if lastTime != nil && lastID != nil && *lastTime > 0 && *lastID > 0 { // 第一页可以不传游标或者传入0
		t := time.Unix(*lastTime, 0)
		db = db.Where("created_at < ? OR (created_at = ? AND id < ?)", t, t, *lastID)
	}

	var list []*model.Comment
	if err := db.Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// CountByVideo 获取视频评论总数
func (r *CommentRepo) CountByVideo(ctx context.Context, vid uint) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.Comment{}).Where("video_id = ?", vid).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
