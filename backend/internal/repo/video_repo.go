package repo

import (
	"context"
	"feedsystem/internal/model"

	"gorm.io/gorm"
)

type VideoRepo struct {
	db *gorm.DB
}

func NewVideoRepo(db *gorm.DB) *VideoRepo {
	return &VideoRepo{db: db}
}

// Create 保存视频信息到数据库
func (r *VideoRepo) Create(ctx context.Context, video *model.Video) error {
	return r.db.WithContext(ctx).Create(video).Error
}

// FindWithAuthor 根据视频ID查询视频及其作者信息
func (r *VideoRepo) FindWithAuthor(ctx context.Context, videoID uint) (*model.Video, *model.User, error) {
	var video model.Video
	var author model.User

	type VideoAuthorResult struct {
		model.Video
		AuthorID  uint   `gorm:"column:author_id"`
		UserName  string `gorm:"column:user_name"`
		AvatarURL string `gorm:"column:avatar_url"`
	}

	var result VideoAuthorResult

	err := r.db.WithContext(ctx).
		Table("videos").
		Joins("LEFT JOIN users ON videos.author_id = users.id").
		Select(`
            videos.id,
            videos.author_id,
            videos.title,
            videos.description,
            videos.video_url,
            videos.cover_url,
            videos.likes_count,
            videos.created_at,
            users.id as author_id,
            users.user_name,
            users.avatar_url
        `).
		Where("videos.id = ?", videoID).
		Scan(&result).Error

	if err != nil {
		return nil, nil, err
	}

	video = result.Video
	author = model.User{
		ID:        result.AuthorID,
		UserName:  result.UserName,
		AvatarURL: result.AvatarURL,
	}

	return &video, &author, nil
}
