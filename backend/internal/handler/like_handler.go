package handler

import (
	"feedsystem/internal/dto"
	"feedsystem/internal/handler/middleware"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/logger"
	"feedsystem/internal/utils/response"
	"fmt"

	"github.com/gin-gonic/gin"
)

type LikeHandler struct {
	LikeService *service.LikeService
}

func NewLikeHandler(likeService *service.LikeService) *LikeHandler {
	return &LikeHandler{LikeService: likeService}
}

// LikeVideo 点赞视频
func (h *LikeHandler) LikeVideo(c *gin.Context) {
	// 获取当前登录用户ID
	userID, _, err := getUserFromCtx(c)
	if err != nil {
		logger.Error("get user from context failed", logger.Err(err))
		response.FailResponse(c, "Failed to get user from context")
		return
	}

	var req dto.LikeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("bind like request failed", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(c, "Invalid request data")
		return
	}

	// 调用服务层的LikeVideo方法
	if err := h.LikeService.LikeVideo(c.Request.Context(), userID, req.VideoID); err != nil {
		logger.Error("like video failed", logger.Uint("user_id", userID), logger.Uint("video_id", req.VideoID), logger.Err(err))
		response.FailResponse(c, fmt.Sprintf("Failed to like video: %v", err))
		return
	}

	response.SuccessResponse(c, "Video liked successfully")
}

// UnlikeVideo 取消点赞视频
func (h *LikeHandler) UnlikeVideo(c *gin.Context) {
	// 获取当前登录用户ID
	userID, _, err := getUserFromCtx(c)
	if err != nil {
		logger.Error("get user from context failed", logger.Err(err))
		response.FailResponse(c, "Failed to get user from context")
		return
	}

	var req dto.LikeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("bind like request failed", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(c, "Invalid request data")
		return
	}

	// 调用服务层的UnlikeVideo方法
	if err := h.LikeService.UnlikeVideo(c.Request.Context(), userID, req.VideoID); err != nil {
		logger.Error("unlike video failed", logger.Uint("user_id", userID), logger.Uint("video_id", req.VideoID), logger.Err(err))
		response.FailResponse(c, fmt.Sprintf("Failed to unlike video: %v", err))
		return
	}

	response.SuccessResponse(c, "Video unliked successfully")
}

// IsLiked 检查用户是否点赞了视频
func (h *LikeHandler) IsLiked(c *gin.Context) {
	// 获取当前登录用户ID
	userID, _, err := getUserFromCtx(c)
	if err != nil {
		logger.Error("get user from context failed", logger.Err(err))
		response.FailResponse(c, "Failed to get user from context")
		return
	}

	var req dto.LikeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("bind like request failed", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(c, "Invalid request data")
		return
	}

	// 调用服务层的IsLiked方法
	isLiked, err := h.LikeService.IsLiked(c.Request.Context(), userID, req.VideoID)
	if err != nil {
		logger.Error("check video liked failed", logger.Uint("user_id", userID), logger.Uint("video_id", req.VideoID), logger.Err(err))
		response.FailResponse(c, "Failed to check if video is liked")
		return
	}

	response.SuccessResponse(c, isLiked)
}

// ListLikedVideos 列出用户点赞过的视频
func (h *LikeHandler) ListLikedVideos(c *gin.Context) {
	// 解析请求参数
	var req dto.ListLikedVideosRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("bind list liked videos request failed", logger.Err(err))
		response.FailResponse(c, fmt.Sprintf("Invalid request parameters: %v", err))
		return
	}

	// 获取当前用户的 userID，如果未登录则为0
	userID := middleware.TryGetUserID(c)
	logger.Debug("list liked videos called", logger.Uint("login_user_id", userID), logger.Uint("target_user_id", req.UserID))

	// 调用服务层的ListLikedVideos方法
	resp, err := h.LikeService.ListLikedVideos(c.Request.Context(), req.UserID, userID, req.Cursor, uint(req.PageSize))
	if err != nil {
		logger.Error("list liked videos failed", logger.Uint("target_user_id", req.UserID), logger.Err(err))
		response.FailResponse(c, fmt.Sprintf("Failed to list liked videos: %v", err))
		return
	}

	response.SuccessResponse(c, resp)
}
