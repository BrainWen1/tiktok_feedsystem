package dto

type PublishCommentRequest struct {
	VideoID  uint   `json:"video_id"`
	ParentID uint   `json:"parent_id"`
	Content  string `json:"content"`
}
