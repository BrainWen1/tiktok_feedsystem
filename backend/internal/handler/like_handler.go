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
		response.FailResponse(ctx, fmt.Sprintf("Failed to like video: %v", err))
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
		response.FailResponse(ctx, fmt.Sprintf("Failed to unlike video: %v", err))
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

// ListLikedVideos 列出用户点赞过的视频
func (h *LikeHandler) ListLikedVideos(ctx *gin.Context) {
	// 解析请求参数
	var req dto.ListLikedVideosRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		log.Printf("Failed to bind query parameters: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Invalid request parameters: %v", err))
		return
	}

	// 获取当前用户的uid，如果未登录则为0
	uid := middleware.TryGetUID(ctx)
	log.Printf("ListLikedVideos called by uid=%d for targetUid=%d", uid, req.UserID)

	// 调用服务层的ListLikedVideos方法
	resp, err := h.LikeService.ListLikedVideos(ctx.Request.Context(), req.UserID, uid, uint(req.PageNum), uint(req.PageSize))
	if err != nil {
		log.Printf("Failed to list liked videos: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to list liked videos: %v", err))
		return
	}

	response.SuccessResponse(ctx, resp)
}
