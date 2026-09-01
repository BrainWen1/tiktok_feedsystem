package dto

// 发布视频请求
type PublishVideoRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	VideoURL    string `json:"video_url"`
	CoverURL    string `json:"cover_url"`
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
