package handler

import (
	"feedsystem/internal/dto"
	"feedsystem/internal/handler/middleware"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/response"
	"fmt"
	"log"

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
	// 获取uid
	uid, username, err := getUserFromCtx(ctx)
	if err != nil {
		log.Printf("Failed to get user from context: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to get user from context: %v", err))
		return
	}

	// 获取请求参数
	var req dto.PublishCommentRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		log.Printf("Failed to bind request: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to bind request: %v", err))
		return
	}

	// 调用服务层创建评论
	err = h.CommentService.PublishComment(ctx.Request.Context(), req.VideoID, uid, req.ParentID, username, req.Content)
	if err != nil {
		log.Printf("Failed to create comment: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to create comment: %v", err))
		return
	}

	response.SuccessResponse(ctx, "Comment created successfully")
}

func (h *CommentHandler) DeleteComment(ctx *gin.Context) {
	// 获取uid
	uid, _, err := getUserFromCtx(ctx)
	if err != nil {
		log.Printf("Failed to get user from context: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to get user from context: %v", err))
		return
	}

	// 获取请求参数
	var req dto.DeleteCommentRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		log.Printf("Failed to bind request: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to bind request: %v", err))
		return
	}

	// 调用服务层删除评论
	err = h.CommentService.DeleteComment(ctx.Request.Context(), uid, req.CommentID)
	if err != nil {
		log.Printf("Failed to delete comment: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to delete comment: %v", err))
		return
	}

	response.SuccessResponse(ctx, "Comment deleted successfully")
}

// ListComment 获取视频评论列表，支持分页
func (h *CommentHandler) ListComments(ctx *gin.Context) {
	// 获取请求参数
	var req dto.ListCommentsRequest
	if err := ctx.ShouldBindQuery(&req); err != nil {
		log.Printf("Failed to bind request: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to bind request: %v", err))
		return
	}

	// 软鉴权：尝试从上下文中获取用户ID，游客返回0
	uid := middleware.TryGetUID(ctx)

	// 调用服务层获取评论列表
	resp, err := h.CommentService.ListComments(ctx.Request.Context(), uid, req)
	if err != nil {
		log.Printf("Failed to list comments: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to list comments: %v", err))
		return
	}

	response.SuccessResponse(ctx, resp)
}
