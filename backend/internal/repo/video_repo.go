package repo

import (
	"context"
	"feedsystem/internal/model"
	"fmt"
	"time"

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

// CreateWithTags 在同一事务内保存视频及其标签关联，避免视频已发布但标签写入失败。
// 标签去重、长度限制等业务规则由 service 层处理，repo 只做原始写入。
func (r *VideoRepo) CreateWithTags(ctx context.Context, video *model.Video, tags []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(video).Error; err != nil {
			return err
		}
		if len(tags) == 0 {
			return nil
		}
		videoTags := make([]model.VideoTag, 0, len(tags))
		for _, tag := range tags {
			videoTags = append(videoTags, model.VideoTag{VideoID: video.ID, Tag: tag})
		}
		return tx.Create(&videoTags).Error
	})
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

// VideoList 根据作者ID和复合游标分页查询视频列表。
// 排序键为 created_at，主键 id 作为并列时的决胜键，游标条件使用行值比较，避免 OFFSET 深翻页。
func (r *VideoRepo) VideoList(ctx context.Context, authorID uint, lastCreatedAt time.Time, lastVid uint, pageSize int) ([]model.Video, int64, error) {
	var videos []model.Video
	var total int64

	if authorID == 0 {
		return nil, 0, fmt.Errorf("video list: empty author id: %w", ErrInvalidArgument)
	}

	// 获取总数
	if err := r.db.WithContext(ctx).Model(&model.Video{}).Where("author_id = ?", authorID).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 复合游标分页查询
	q := r.db.WithContext(ctx).Model(&model.Video{}).Where("author_id = ?", authorID)
	if lastVid != 0 {
		q = q.Where("(created_at, id) < (?, ?)", lastCreatedAt, lastVid)
	}
	if err := q.Order("created_at DESC, id DESC"). // 按创建时间降序，id 兜底保证稳定顺序
							Limit(pageSize).
							Find(&videos).Error; err != nil {
		return nil, 0, err
	}

	return videos, total, nil
}
