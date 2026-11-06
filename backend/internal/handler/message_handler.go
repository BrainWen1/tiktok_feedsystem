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
		response.FailResponse(c, "用户未登录")
		return
	}

	// 解析请求参数
	var req dto.SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Error("bind request failed", logger.Err(err))
		response.FailResponse(c, "参数错误")
		return
	}

	// 调用 Service 层发送消息
	if err := h.messageService.SendMessage(c.Request.Context(), senderID, senderName, req.ReceiverID, req.Content); err != nil {
		logger.Ctx(c.Request.Context()).Error("send message failed", logger.Err(err))
		response.FailResponse(c, "发送消息失败")
		return
	}

	response.SuccessResponse(c, "发送成功")
}

// GetHistory 获取聊天历史
func (h *MessageHandler) GetHistory(c *gin.Context) {
	// 获取目标用户的 userID
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	// 解析请求参数
	var req dto.GetHistoryRequest
	if err := c.ShouldBindQuery(&req); err != nil { // Query 绑定
		logger.Ctx(c.Request.Context()).Error("bind request failed", logger.Err(err))
		response.FailResponse(c, "参数错误")
		return
	}

	// 调用 Service 层获取聊天历史
	resp, err := h.messageService.GetHistory(c.Request.Context(), userID, req)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get history failed", logger.Err(err))
		response.FailResponse(c, "获取聊天历史失败")
		return
	}

	response.SuccessResponse(c, resp)
}
