package repo

import (
	"context"
	"feedsystem/internal/model"

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
