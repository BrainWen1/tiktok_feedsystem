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

// FindByID 根据ID查找视频
func (r *VideoRepo) FindByID(ctx context.Context, id uint) (*model.Video, error) {
	var video model.Video
	if err := r.db.WithContext(ctx).First(&video, id).Error; err != nil {
		return nil, err
	}
	return &video, nil
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

// VideoList 根据作者ID分页查询视频列表
func (r *VideoRepo) VideoList(ctx context.Context, authorID uint, pageNum, pageSize int) ([]model.Video, int64, error) {
	var videos []model.Video
	var total int64

	// 获取总数
	if err := r.db.WithContext(ctx).Model(&model.Video{}).Where("author_id = ?", authorID).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 分页查询
	if err := r.db.WithContext(ctx).Model(&model.Video{}).Where("author_id = ?", authorID).
		Order("created_at DESC").         // 按创建时间降序排列
		Offset((pageNum - 1) * pageSize). // 偏移量
		Limit(pageSize).                  // 限制每页数量
		Find(&videos).Error; err != nil {
		return nil, 0, err
	}

	return videos, total, nil
}
