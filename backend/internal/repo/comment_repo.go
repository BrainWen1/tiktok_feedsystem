package repo

import (
	"context"
	"feedsystem/internal/model"
	"time"

	"gorm.io/gorm"
)

// CommentRepo 评论仓库，只负责评论表的原始 SQL 读写。
type CommentRepo struct {
	db *gorm.DB
}

// NewCommentRepo 创建评论仓库
func NewCommentRepo(db *gorm.DB) *CommentRepo {
	return &CommentRepo{db: db}
}

// CreateComment 写入一条评论记录
func (r *CommentRepo) CreateComment(ctx context.Context, comment *model.Comment) error {
	return r.db.WithContext(ctx).Create(comment).Error
}

// DeleteComment 按评论ID删除评论记录
func (r *CommentRepo) DeleteComment(ctx context.Context, commentID uint) error {
	return r.db.WithContext(ctx).Delete(&model.Comment{}, commentID).Error
}

// FindByID 根据评论ID查询单条评论，记录不存在时返回 gorm.ErrRecordNotFound
func (r *CommentRepo) FindByID(ctx context.Context, commentID uint) (*model.Comment, error) {
	var comment model.Comment
	if err := r.db.WithContext(ctx).First(&comment, commentID).Error; err != nil {
		return nil, err
	}
	return &comment, nil
}

// ListByVideoCursor 按视频ID复合游标分页查询评论。
// 排序键为 (created_at DESC, id DESC)；lastTime 与 lastID 均有效时追加游标条件，
// 保证同一时间戳下的多条评论不会因单键游标而漏读。
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
