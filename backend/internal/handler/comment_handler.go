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

func (h *CommentHandler) PublishComment(ctx *gin.Context) {
	// 获取 userID
	userID, username, err := getUserFromCtx(ctx)
	if err != nil {
		logger.Error("get user from context failed", logger.Err(err))
		response.FailAuthResponse(ctx, "未登录或登录已过期")
		return
	}

	// 获取请求参数
	var req dto.PublishCommentRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		logger.Warn("bind comment request failed", logger.Err(err))
		response.FailResponse(ctx, "请求参数错误")
		return
	}

	// 调用服务层创建评论
	err = h.CommentService.PublishComment(ctx.Request.Context(), req.VideoID, userID, req.ParentID, username, req.Content)
	if err != nil {
		logger.Error("publish comment failed", logger.Uint("user_id", userID), logger.Uint("video_id", req.VideoID), logger.Err(err))
		response.ServerErrorResponse(ctx, "评论发布失败，请稍后重试")
		return
	}

	response.SuccessResponse(ctx, "Comment created successfully")
}

func (h *CommentHandler) DeleteComment(ctx *gin.Context) {
	// 获取 userID
	userID, _, err := getUserFromCtx(ctx)
	if err != nil {
		logger.Error("get user from context failed", logger.Err(err))
		response.FailAuthResponse(ctx, "未登录或登录已过期")
		return
	}

	// 获取请求参数
	var req dto.DeleteCommentRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		logger.Warn("bind comment request failed", logger.Err(err))
		response.FailResponse(ctx, "请求参数错误")
		return
	}

	// 调用服务层删除评论
	err = h.CommentService.DeleteComment(ctx.Request.Context(), userID, req.CommentID)
	if err != nil {
		logger.Error("delete comment failed", logger.Uint("user_id", userID), logger.Uint("comment_id", req.CommentID), logger.Err(err))
		response.ServerErrorResponse(ctx, "评论删除失败，请稍后重试")
		return
	}

	response.SuccessResponse(ctx, "Comment deleted successfully")
}

// ListComment 获取视频评论列表，支持分页
func (h *CommentHandler) ListComments(ctx *gin.Context) {
	// 获取请求参数
	var req dto.ListCommentsRequest
	if err := ctx.ShouldBindQuery(&req); err != nil {
		logger.Warn("bind comment request failed", logger.Err(err))
		response.FailResponse(ctx, "请求参数错误")
		return
	}

	// 软鉴权：尝试从上下文中获取用户ID，游客返回0
	userID := middleware.TryGetUserID(ctx)

	// 调用服务层获取评论列表
	resp, err := h.CommentService.ListComments(ctx.Request.Context(), userID, req)
	if err != nil {
		logger.Error("list comments failed", logger.Uint("user_id", userID), logger.Err(err))
		response.ServerErrorResponse(ctx, "评论列表获取失败，请稍后重试")
		return
	}

	response.SuccessResponse(ctx, resp)
}
