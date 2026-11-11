package handler

import (
	"feedsystem/internal/dto"
	"feedsystem/internal/handler/middleware"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/logger"
	"feedsystem/internal/utils/response"

	"github.com/gin-gonic/gin"
)

type CommentHandler struct {
	CommentService *service.CommentService
}

func NewCommentHandler(commentService *service.CommentService) *CommentHandler {
	return &CommentHandler{
		CommentService: commentService,
	}
}

// PublishComment 发布评论
// @Summary      发布评论
// @Description  当前登录用户对指定视频发布评论，parent_id 非 0 时表示回复该条评论
// @Tags         评论模块
// @Accept       json
// @Produce      json
// @Security     Bearer
// @Param        request body dto.PublishCommentRequest true "评论信息"
// @Success      200 {object} response.Response{data=string} "评论发布成功"
// @Failure      400 {object} response.Response "请求参数错误"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /comment/publish [post]
func (h *CommentHandler) PublishComment(c *gin.Context) {
	// 获取 userID
	userID, username, err := getUserFromCtx(c)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get user from context failed", logger.Err(err))
		response.FailAuthResponse(c, "未登录或登录已过期")
		return
	}

	// 获取请求参数
	var req dto.PublishCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("bind comment request failed", logger.Err(err))
		response.FailResponse(c, "请求参数错误")
		return
	}

	// 调用服务层创建评论
	err = h.CommentService.PublishComment(c.Request.Context(), req.VideoID, userID, req.ParentID, username, req.Content)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("publish comment failed", logger.Uint("user_id", userID), logger.Uint("video_id", req.VideoID), logger.Err(err))
		response.ServerErrorResponse(c, "评论发布失败，请稍后重试")
		return
	}

	response.SuccessResponse(c, "Comment created successfully")
}

// DeleteComment 删除评论
// @Summary      删除评论
// @Description  当前登录用户删除自己发布的评论
// @Tags         评论模块
// @Accept       json
// @Produce      json
// @Security     Bearer
// @Param        request body dto.DeleteCommentRequest true "删除评论信息"
// @Success      200 {object} response.Response{data=string} "评论删除成功"
// @Failure      400 {object} response.Response "请求参数错误"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /comment/delete [post]
func (h *CommentHandler) DeleteComment(c *gin.Context) {
	// 获取 userID
	userID, _, err := getUserFromCtx(c)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get user from context failed", logger.Err(err))
		response.FailAuthResponse(c, "未登录或登录已过期")
		return
	}

	// 获取请求参数
	var req dto.DeleteCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("bind comment request failed", logger.Err(err))
		response.FailResponse(c, "请求参数错误")
		return
	}

	// 调用服务层删除评论
	err = h.CommentService.DeleteComment(c.Request.Context(), userID, req.CommentID)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("delete comment failed", logger.Uint("user_id", userID), logger.Uint("comment_id", req.CommentID), logger.Err(err))
		response.ServerErrorResponse(c, "评论删除失败，请稍后重试")
		return
	}

	response.SuccessResponse(c, "Comment deleted successfully")
}

// ListComments 获取视频评论列表，支持分页
// @Summary      获取视频评论列表
// @Description  按 (last_time, last_id) 游标分页获取指定视频的评论列表；软鉴权接口，可选携带 Authorization 头
// @Tags         评论模块
// @Produce      json
// @Param        video_id  query int    true  "视频 ID"
// @Param        last_time query int    false "上一页最后一条评论的 Unix 毫秒时间戳（第一页不传）"
// @Param        last_id   query int    false "上一页最后一条评论 ID，与 last_time 成对使用"
// @Param        page_size query int    false "每页数量，范围 5-50"
// @Success      200 {object} response.Response{data=dto.ListCommentsResponse} "获取成功"
// @Failure      400 {object} response.Response "请求参数错误"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /comment/list [get]
func (h *CommentHandler) ListComments(c *gin.Context) {
	// 获取请求参数
	var req dto.ListCommentsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("bind comment request failed", logger.Err(err))
		response.FailResponse(c, "请求参数错误")
		return
	}

	// 软鉴权：尝试从上下文中获取用户ID，游客返回0
	userID := middleware.TryGetUserID(c)

	// 调用服务层获取评论列表
	resp, err := h.CommentService.ListComments(c.Request.Context(), userID, req)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("list comments failed", logger.Uint("user_id", userID), logger.Err(err))
		response.ServerErrorResponse(c, "评论列表获取失败，请稍后重试")
		return
	}

	response.SuccessResponse(c, resp)
}
