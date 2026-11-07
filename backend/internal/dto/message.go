package dto

import (
	"feedsystem/internal/model"
)

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

// ListSessionsRequest 获取会话列表请求
type ListSessionsRequest struct {
	Limit int64 `form:"limit,default=20"` // 拉取数量，默认 20
}

// SessionDetail
type SessionDetail struct {
	TargetID    uint   `json:"target_id"`    // 对方的 userID
	TargetName  string `json:"target_name"`  // 对方的用户名
	Content     string `json:"content"`      // 最新消息内容
	CreatedAt   int64  `json:"created_at"`   // 最新消息时间
	UnreadCount int64  `json:"unread_count"` // 未读消息数
}
