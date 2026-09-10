package dto

type PublishCommentRequest struct {
	VideoID  uint   `json:"video_id"`
	ParentID uint   `json:"parent_id"`
	Content  string `json:"content"`
}

type DeleteCommentRequest struct {
	CommentID uint `json:"comment_id"`
}
