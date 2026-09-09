package handler

import (
	"feedsystem/internal/dto"
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
	err = h.CommentService.PublishComment(ctx, req.VideoID, uid, req.ParentID, username, req.Content)
	if err != nil {
		log.Printf("Failed to create comment: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to create comment: %v", err))
		return
	}

	response.SuccessResponse(ctx, "Comment created successfully")
}
