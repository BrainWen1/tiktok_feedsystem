package dto

// 发布视频请求
type PublishVideoRequest struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	VideoURL    string   `json:"video_url"`
	CoverURL    string   `json:"cover_url"`
	Tags        []string `json:"tags"` // 标签，写入 video_tag 关联表
}

// 发布视频响应
type PublishVideoResponse struct {
	VideoID uint `json:"video_id"`
}

// 删除视频请求
type DeleteVideoRequest struct {
	ID uint `json:"id"`
}

// 按作者ID列出视频请求
type ListByAuthorIDRequest struct {
	AuthorID uint `json:"author_id"`
}

// 获取视频详情请求
type GetDetailRequest struct {
	ID uint `json:"id"`
}

// VideoDetailResponse 视频详情返回
type VideoDetailResponse struct {
	ID          uint        `json:"video_id"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	VideoURL    string      `json:"video_url"`
	CoverURL    string      `json:"cover_url"`
	LikesCount  int64       `json:"like_count"`
	IsLiked     bool        `json:"is_liked"` // 软鉴权：游客永远false，登录用户才会算出真实状态
	AuthorInfo  AuthorBrief `json:"author"`
}

// AuthorBrief 作者简要信息
type AuthorBrief struct {
	UserID    uint   `json:"user_id"`
	UserName  string `json:"user_name"`
	AvatarURL string `json:"avatar_url"`
}

// VideoListResponse 视频列表返回
type VideoListResponse struct {
	Total      int64                 `json:"total"`
	Videos     []VideoDetailResponse `json:"videos"`
	NextCursor *FeedCursor           `json:"next_cursor,omitempty"` // 复合游标，为空表示没有下一页
}

// ListLikedVideosRequest 列出用户点赞过的视频请求。
// page_num 仅为兼容旧客户端保留，实际分页使用 cursor；
// cursor.last_vid 为上一页最后一条点赞记录的 video_id，last_created_at 为对应点赞时间。
type ListLikedVideosRequest struct {
	UserID   uint       `json:"user_id" binding:"required,min=1,max=4294967295"`
	PageNum  int        `json:"page_num" binding:"omitempty,min=1,max=100000"`
	PageSize int        `json:"page_size" binding:"required,min=1,max=30"`
	Cursor   FeedCursor `json:"cursor"`
}
