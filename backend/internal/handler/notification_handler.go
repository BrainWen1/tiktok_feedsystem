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
// @Summary      获取通知列表
// @Description  按 (last_time, last_id) 游标分页获取当前用户的通知列表。data 为对象，其中 data.data 为 dto.ListNotificationsResponse（notifications 通知列表、next_last_time / next_last_id 下一页游标）
// @Tags         通知模块
// @Produce      json
// @Security     Bearer
// @Param        page_size query int    false "每页数量"
// @Param        last_time query string false "上一页最后一条通知时间（RFC3339）"
// @Param        last_id   query int    false "上一页最后一条通知 ID，与 last_time 成对使用"
// @Success      200 {object} response.Response{data=map[string]interface{}} "获取成功，data 包含 data（通知列表结构）"
// @Failure      400 {object} response.Response "请求参数错误或获取通知列表失败"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Router       /notification/list [get]
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
// @Summary      获取未读通知数量
// @Description  获取当前用户的未读通知总数，data 包含 count 字段
// @Tags         通知模块
// @Produce      json
// @Security     Bearer
// @Success      200 {object} response.Response{data=map[string]interface{}} "获取成功，data 包含 count 字段"
// @Failure      400 {object} response.Response "获取未读通知总数失败"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Router       /notification/unread_count [get]
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
// @Summary      标记单条通知已读
// @Description  将当前用户的一条通知标记为已读
// @Tags         通知模块
// @Accept       json
// @Produce      json
// @Security     Bearer
// @Param        request body dto.MarkAsReadRequest true "标记已读信息"
// @Success      200 {object} response.Response{data=string} "标记成功"
// @Failure      400 {object} response.Response "请求参数错误或标记失败"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Router       /notification/read [post]
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
// @Summary      标记全部通知已读
// @Description  将当前用户的所有未读通知标记为已读
// @Tags         通知模块
// @Produce      json
// @Security     Bearer
// @Success      200 {object} response.Response{data=string} "标记成功"
// @Failure      400 {object} response.Response "标记全部通知为已读失败"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Router       /notification/read_all [post]
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
