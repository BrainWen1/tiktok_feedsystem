package model

import "time"

// Video 模型
type Video struct {
	ID          uint   `gorm:"primaryKey" json:"id"`                            // 视频ID
	AuthorID    uint   `gorm:"index;not null" json:"author_id"`                 // 作者ID
	Title       string `gorm:"type:varchar(255);not null" json:"title"`         // 视频标题
	Description string `gorm:"type:varchar(255);" json:"description,omitempty"` // 视频简介
	PlayURL     string `gorm:"type:varchar(255);not null" json:"play_url"`      // 视频播放URL
	CoverURL    string `gorm:"type:varchar(255);not null" json:"cover_url"`     // 视频封面URL

	CreateTime time.Time `gorm:"autoCreateTime;index:idx_videos_create_time;index:idx_videos_popularity_time_id,priority:2,sort:desc" json:"create_time"` // 创建时间
	LikesCount int64     `gorm:"column:likes_count;not null;default:0;index:idx_videos_likes_count_id,priority:1,sort:desc" json:"likes_count"`           // 点赞数
	Popularity int64     `gorm:"column:popularity;not null;default:0;index:idx_videos_popularity_time_id,priority:1,sort:desc" json:"popularity"`         // 热度值

	CreatedAt time.Time `gorm:"autoCreateTime"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
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
