package model

import "time"

// Video 模型
type Video struct {
	ID          uint   `gorm:"primaryKey" json:"id"`                            // 视频ID
	AuthorID    uint   `gorm:"index;not null" json:"author_id"`                 // 作者ID
	Title       string `gorm:"type:varchar(255);not null" json:"title"`         // 视频标题
	Description string `gorm:"type:varchar(255);" json:"description,omitempty"` // 视频简介
	VideoURL    string `gorm:"type:varchar(255);not null" json:"video_url"`     // 视频播放URL
	CoverURL    string `gorm:"type:varchar(255);not null" json:"cover_url"`     // 视频封面URL

	CreateTime time.Time `gorm:"autoCreateTime;index:idx_videos_create_time;index:idx_videos_popularity_time_id,priority:2,sort:desc" json:"create_time"` // 创建时间
	LikesCount int64     `gorm:"column:likes_count;not null;default:0;index:idx_videos_likes_count_id,priority:1,sort:desc" json:"likes_count"`           // 点赞数
	Popularity int64     `gorm:"column:popularity;not null;default:0;index:idx_videos_popularity_time_id,priority:1,sort:desc" json:"popularity"`         // 热度值

	CreatedAt time.Time `gorm:"autoCreateTime"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

// VideoTag 视频-标签关联表。
// 替代 videos.tags 上的 FIND_IN_SET 查询：tag 走联合索引 (tag, video_id)，
// (video_id, tag) 唯一索引保证同一视频不会重复写入同一标签。
type VideoTag struct {
	ID        uint      `gorm:"primaryKey"`
	VideoID   uint      `gorm:"not null;uniqueIndex:uk_video_tag,priority:1;index:idx_video_tag_tag_video,priority:2"`
	Tag       string    `gorm:"type:varchar(64);not null;uniqueIndex:uk_video_tag,priority:2;index:idx_video_tag_tag_video,priority:1"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

// TableName 固定表名为 video_tag，避免 GORM 默认复数化成 video_tags
func (VideoTag) TableName() string {
	return "video_tag"
}

// OutboxMsg 本地消息表 outbox pattern，异步事件投递
type OutboxMsg struct {
	ID         uint      `gorm:"primaryKey"`
	VideoID    uint      `gorm:"index"`
	EventType  string    `gorm:"type:varchar(50)"`
	Payload    string    `gorm:"type:text"` // 完整事件json
	RetryTimes int       `gorm:"default:0"` // 重试次数
	NextExecAt time.Time `gorm:"index"`     // 下次调度时间
	CreateTime time.Time `gorm:"autoCreateTime"`
	Status     string    `gorm:"type:varchar(50);index"` // pending / success / fail
}
