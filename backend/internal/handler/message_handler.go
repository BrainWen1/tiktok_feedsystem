package handler

import (
	"feedsystem/internal/dto"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/logger"
	"feedsystem/internal/utils/response"

	"github.com/gin-gonic/gin"
)

type MessageHandler struct {
	messageService *service.MessageService
}

func NewMessageHandler(messageService *service.MessageService) *MessageHandler {
	return &MessageHandler{messageService: messageService}
}

// SendMessage 发送消息
func (h *MessageHandler) SendMessage(c *gin.Context) {
	// 获取 senderID 和 senderName
	senderID, senderName, err := getUserFromCtx(c)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get user from context failed", logger.Err(err))
		response.FailResponse(c, "Failed to get user from context")
		return
	}

	// 解析请求参数
	var req dto.SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Error("bind request failed", logger.Err(err))
		response.FailResponse(c, "Invalid request parameters")
		return
	}

	// 调用 Service 层发送消息
	if err := h.messageService.SendMessage(c.Request.Context(), senderID, senderName, req.ReceiverID, req.Content); err != nil {
		logger.Ctx(c.Request.Context()).Error("send message failed", logger.Err(err))
		response.FailResponse(c, "Failed to send message")
		return
	}

	response.SuccessResponse(c, "Message sent successfully")
}
