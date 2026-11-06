package dto

// SendMessageRequest 发送消息请求
type SendMessageRequest struct {
	ReceiverID uint   `json:"receiver_id" binding:"required"`
	Content    string `json:"content" binding:"required"`
}
