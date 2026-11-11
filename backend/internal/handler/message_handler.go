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
// @Summary      发送私信
// @Description  当前登录用户向指定用户发送一条私信，接收方在线时通过 SSE 实时推送
// @Tags         私信模块
// @Accept       json
// @Produce      json
// @Security     Bearer
// @Param        request body dto.SendMessageRequest true "消息内容"
// @Success      200 {object} response.Response{data=string} "发送成功"
// @Failure      400 {object} response.Response "参数错误或发送消息失败"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Router       /message/send [post]
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
// @Summary      获取聊天历史
// @Description  按消息 ID 游标分页获取当前用户与目标用户的聊天记录，结果按时间从新到旧排列
// @Tags         私信模块
// @Produce      json
// @Security     Bearer
// @Param        target_id query int true  "对方用户 ID"
// @Param        last_id   query int false "游标，上一页最老一条消息的 ID（第一页传 0）"
// @Param        page_size query int false "每页条数，默认 20"
// @Success      200 {object} response.Response{data=dto.GetHistoryResponse} "获取成功"
// @Failure      400 {object} response.Response "参数错误或获取聊天历史失败"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Router       /message/history [get]
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

// ListSessions 获取会话列表
// @Summary      获取会话列表
// @Description  获取当前用户的私信会话列表，包含对方信息、最新一条消息内容与未读消息数
// @Tags         私信模块
// @Produce      json
// @Security     Bearer
// @Param        limit query int false "拉取数量，默认 20"
// @Success      200 {object} response.Response{data=[]dto.SessionDetail} "获取成功"
// @Failure      400 {object} response.Response "参数错误或获取会话列表失败"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Router       /message/sessions [get]
func (h *MessageHandler) ListSessions(c *gin.Context) {
	// 获取 userID
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	// 解析请求参数
	var req dto.ListSessionsRequest
	if err := c.ShouldBindQuery(&req); err != nil { // Query 绑定
		logger.Ctx(c.Request.Context()).Error("bind request failed", logger.Err(err))
		response.FailResponse(c, "参数错误")
		return
	}

	// 调用 Service 层获取会话列表
	sessions, err := h.messageService.ListSessions(c.Request.Context(), userID, req.Limit)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("list sessions failed", logger.Err(err))
		response.FailResponse(c, "获取会话列表失败")
		return
	}

	response.SuccessResponse(c, sessions)
}
