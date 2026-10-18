package dto

import "time"

// FeedListReq 通用游标分页入参，5个list接口复用
type FeedListReq struct {
	Limit  int        `form:"limit"`
	Tag    string     `form:"tag"`     // listByTag 专用，其他接口忽略
	Cursor FeedCursor `form:"cursor."` // 复合游标，5个list接口复用，cursor.会让gin自动解析成嵌套结构体
}

// FeedItemResp feed对外输出VO，聚合完成后的视频条目
type FeedItemResp struct {
	Vid        string `json:"vid"`
	Title      string `json:"title"`
	Desc       string `json:"desc"`
	VideoUrl   string `json:"video_url"`
	CoverUrl   string `json:"cover_url"`
	AuthorUid  uint   `json:"author_uid"`
	AuthorName string `json:"author_name"`
	AvatarUrl  string `json:"avatar_url"`

	LikesCnt    int64 `json:"likes_cnt"`    // 点赞总数
	CommentsCnt int64 `json:"comments_cnt"` // 评论总数
	Popularity  int64 `json:"popularity"`   // 热度值，越大越热

	IsLike   bool `json:"is_like"`   // 当前登录用户是否点赞该视频
	IsFollow bool `json:"is_follow"` // 当前登录用户是否关注作者

	CreatedAt time.Time `json:"created_at"` // 视频创建时间戳
}

// FeedListResp feed列表接口返回VO，聚合完成后的视频列表以及分页游标
type FeedListResp struct {
	List       []FeedItemResp `json:"list"`
	NextCursor *FeedCursor    `json:"next_cursor,omitempty"`
}

// FeedCursor 复合游标，用于分页，包含多维度的游标信息，每个接口各取所需
type FeedCursor struct {
	LastVid        string    `json:"last_vid"`
	LastPopularity int64     `json:"last_popularity"`
	LastCreatedAt  time.Time `json:"last_created_at"`
	LastLikesCount int64     `json:"last_likes_count"`
}
