package dto

import "feedsystem/internal/model"

// SendMessageRequest 发送消息请求
type SendMessageRequest struct {
	ReceiverID uint   `json:"receiver_id" binding:"required"`
	Content    string `json:"content" binding:"required"`
}

// GetHistoryRequest 获取聊天历史请求
type GetHistoryRequest struct {
	TargetID uint `form:"target_id" binding:"required"` // 对方的 userID
	LastID   uint `form:"last_id"`                      // 游标，上一次拉取的最老的那条消息的 ID（第一页传 0）
	PageSize int  `form:"page_size,default=20"`         // 每页条数
}

// GetHistoryResponse 获取聊天历史响应
type GetHistoryResponse struct {
	Messages   []model.Message `json:"messages"`     // 新 -> 旧
	HasMore    bool            `json:"has_more"`     // 是否还有更早的消息
	NextLastID uint            `json:"next_last_id"` // 供前端请求下一页的游标
}
