package dto

import "feedsystem/internal/model"

type PublishCommentRequest struct {
	VideoID  uint   `json:"video_id"`
	ParentID uint   `json:"parent_id"`
	Content  string `json:"content"`
}

type DeleteCommentRequest struct {
	CommentID uint `json:"comment_id"`
}

// ListCommentRequest 游标分页查询评论
type ListCommentsRequest struct {
	VideoID  uint   `form:"video_id" binding:"required"`
	LastTime *int64 `form:"last_time"` // 上一页最后一条评论的Unix时间戳，第一页不传
	LastID   *uint  `form:"last_id"`   // 上一页最后一条评论ID，和last_time成对
	PageSize int    `form:"page_size" binding:"min=5,max=50"`
}

// ListCommentResponse 列表接口返回
type ListCommentsResponse struct {
	Comments []model.Comment `json:"comments"`
	HasMore  bool            `json:"has_more"`            // 是否还有更多评论
	LastTime *int64          `json:"last_time,omitempty"` // 给前端下一轮请求的游标
	LastID   *uint           `json:"last_id,omitempty"`
	Total    int64           `json:"total"` // 视频评论总数量，来自Redis计数器
}
