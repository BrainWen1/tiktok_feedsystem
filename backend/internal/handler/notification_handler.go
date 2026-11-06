package handler

import (
	"feedsystem/internal/dto"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/logger"
	"feedsystem/internal/utils/response"

	"github.com/gin-gonic/gin"
)

type NotificationHandler struct {
	notificationService *service.NotificationService
}

func NewNotificationHandler(notificationService *service.NotificationService) *NotificationHandler {
	return &NotificationHandler{
		notificationService: notificationService,
	}
}

// ListNotifications 获取用户通知列表
func (h *NotificationHandler) ListNotifications(c *gin.Context) {
	// 获取 userID
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	// 解析请求参数
	var req dto.ListNotificationsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		logger.Ctx(c.Request.Context()).Error("failed to bind ListNotificationsRequest", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(c, "请求参数错误")
		return
	}

	// 调用服务层获取通知列表
	resp, err := h.notificationService.ListNotifications(c.Request.Context(), userID, req.LastTime, req.LastID, req.PageSize)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("failed to list notifications for user ", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(c, "获取通知列表失败")
		return
	}

	response.SuccessResponse(c, gin.H{"data": resp})
}

// GetUnreadCount 获取用户未读通知总数
func (h *NotificationHandler) GetUnreadCount(c *gin.Context) {
	// 获取 userID
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	// 调用服务层获取未读通知总数
	count, err := h.notificationService.GetUnreadCount(c.Request.Context(), userID)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("failed to get unread count for user ", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(c, "获取未读通知总数失败")
		return
	}

	response.SuccessResponse(c, gin.H{"count": count})
}

// MarkAsRead 标记单条通知为已读
func (h *NotificationHandler) MarkAsRead(c *gin.Context) {
	// 获取 userID
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	// 解析请求参数
	var req dto.MarkAsReadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Error("failed to bind MarkAsReadRequest", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(c, "请求参数错误")
		return
	}

	// 调用服务层标记通知为已读
	if err := h.notificationService.MarkAsRead(c.Request.Context(), userID, req.NotificationID); err != nil {
		logger.Ctx(c.Request.Context()).Error("failed to mark notification as read", logger.Uint("user_id", userID), logger.Uint("notification_id", req.NotificationID), logger.Err(err))
		response.FailResponse(c, "标记通知为已读失败")
		return
	}

	response.SuccessResponse(c, "标记通知为已读成功")
}

// MarkAllAsRead 标记全部通知为已读
func (h *NotificationHandler) MarkAllAsRead(c *gin.Context) {
	// 获取 userID
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	// 调用服务层标记全部通知为已读
	if err := h.notificationService.MarkAllAsRead(c.Request.Context(), userID); err != nil {
		logger.Ctx(c.Request.Context()).Error("failed to mark all notifications as read", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(c, "标记全部通知为已读失败")
		return
	}

	response.SuccessResponse(c, "标记全部通知为已读成功")
}
