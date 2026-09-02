package handler

import (
	"feedsystem/internal/dto"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/response"
	"log"

	"github.com/gin-gonic/gin"
)

type LikeHandler struct {
	LikeService *service.LikeService
}

func NewLikeHandler(likeService *service.LikeService) *LikeHandler {
	return &LikeHandler{LikeService: likeService}
}

// LikeVideo 点赞视频
func (h *LikeHandler) LikeVideo(ctx *gin.Context) {
	// 获取uid和vid
	uid, _, err := getUserFromCtx(ctx)
	if err != nil {
		log.Printf("Failed to get user from context: %v", err)
		response.FailResponse(ctx, "Failed to get user from context")
		return
	}

	var req dto.LikeRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		log.Printf("Failed to bind JSON: %v", err)
		response.FailResponse(ctx, "Invalid request data")
		return
	}

	// 调用服务层的LikeVideo方法
	if err := h.LikeService.LikeVideo(ctx, uid, req.VideoID); err != nil {
		log.Printf("Failed to like video: %v", err)
		response.FailResponse(ctx, "Failed to like video")
		return
	}

	response.SuccessResponse(ctx, "Video liked successfully")
}

// UnlikeVideo 取消点赞视频
func (h *LikeHandler) UnlikeVideo(ctx *gin.Context) {
	// 获取uid和vid
	uid, _, err := getUserFromCtx(ctx)
	if err != nil {
		log.Printf("Failed to get user from context: %v", err)
		response.FailResponse(ctx, "Failed to get user from context")
		return
	}

	var req dto.LikeRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		log.Printf("Failed to bind JSON: %v", err)
		response.FailResponse(ctx, "Invalid request data")
		return
	}

	// 调用服务层的UnlikeVideo方法
	if err := h.LikeService.UnlikeVideo(ctx, uid, req.VideoID); err != nil {
		log.Printf("Failed to unlike video: %v", err)
		response.FailResponse(ctx, "Failed to unlike video")
		return
	}

	response.SuccessResponse(ctx, "Video unliked successfully")
}

// IsLiked 检查用户是否点赞了视频
func (h *LikeHandler) IsLiked(ctx *gin.Context) {
	// 获取uid和vid
	uid, _, err := getUserFromCtx(ctx)
	if err != nil {
		log.Printf("Failed to get user from context: %v", err)
		response.FailResponse(ctx, "Failed to get user from context")
		return
	}

	var req dto.LikeRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		log.Printf("Failed to bind JSON: %v", err)
		response.FailResponse(ctx, "Invalid request data")
		return
	}

	// 调用服务层的IsLiked方法
	isLiked, err := h.LikeService.IsLiked(ctx, uid, req.VideoID)
	if err != nil {
		log.Printf("Failed to check if video is liked: %v", err)
		response.FailResponse(ctx, "Failed to check if video is liked")
		return
	}

	response.SuccessResponse(ctx, isLiked)
}
