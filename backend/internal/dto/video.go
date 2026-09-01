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
