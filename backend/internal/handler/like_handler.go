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
// @Summary      点赞视频
// @Description  当前登录用户对指定视频点赞，重复点赞按幂等处理
// @Tags         点赞模块
// @Accept       json
// @Produce      json
// @Security     Bearer
// @Param        request body dto.LikeRequest true "点赞信息"
// @Success      200 {object} response.Response{data=string} "点赞成功"
// @Failure      400 {object} response.Response "请求参数错误或点赞失败"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Router       /like/like [post]
func (h *LikeHandler) LikeVideo(c *gin.Context) {
	// 获取当前登录用户ID
	userID, userName, err := getUserFromCtx(c)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get user from context failed", logger.Err(err))
		response.FailResponse(c, "Failed to get user from context")
		return
	}

	var req dto.LikeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("bind like request failed", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(c, "Invalid request data")
		return
	}

	// 调用服务层的LikeVideo方法
	if err := h.LikeService.LikeVideo(c.Request.Context(), userID, req.VideoID, userName); err != nil {
		logger.Ctx(c.Request.Context()).Error("like video failed", logger.Uint("user_id", userID), logger.Uint("video_id", req.VideoID), logger.Err(err))
		response.FailResponse(c, fmt.Sprintf("Failed to like video: %v", err))
		return
	}

	response.SuccessResponse(c, "Video liked successfully")
}

// UnlikeVideo 取消点赞视频
// @Summary      取消点赞视频
// @Description  当前登录用户取消对指定视频的点赞，未点赞时按幂等处理
// @Tags         点赞模块
// @Accept       json
// @Produce      json
// @Security     Bearer
// @Param        request body dto.LikeRequest true "取消点赞信息"
// @Success      200 {object} response.Response{data=string} "取消点赞成功"
// @Failure      400 {object} response.Response "请求参数错误或取消点赞失败"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Router       /like/unlike [post]
func (h *LikeHandler) UnlikeVideo(c *gin.Context) {
	// 获取当前登录用户ID
	userID, _, err := getUserFromCtx(c)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get user from context failed", logger.Err(err))
		response.FailResponse(c, "Failed to get user from context")
		return
	}

	var req dto.LikeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("bind like request failed", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(c, "Invalid request data")
		return
	}

	// 调用服务层的UnlikeVideo方法
	if err := h.LikeService.UnlikeVideo(c.Request.Context(), userID, req.VideoID); err != nil {
		logger.Ctx(c.Request.Context()).Error("unlike video failed", logger.Uint("user_id", userID), logger.Uint("video_id", req.VideoID), logger.Err(err))
		response.FailResponse(c, fmt.Sprintf("Failed to unlike video: %v", err))
		return
	}

	response.SuccessResponse(c, "Video unliked successfully")
}

// IsLiked 检查用户是否点赞了视频
// @Summary      查询是否已点赞
// @Description  查询当前登录用户是否点赞了指定视频，data 为布尔值
// @Tags         点赞模块
// @Accept       json
// @Produce      json
// @Security     Bearer
// @Param        request body dto.LikeRequest true "查询信息"
// @Success      200 {object} response.Response{data=bool} "查询成功"
// @Failure      400 {object} response.Response "请求参数错误或查询失败"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Router       /like/is_liked [post]
func (h *LikeHandler) IsLiked(c *gin.Context) {
	// 获取当前登录用户ID
	userID, _, err := getUserFromCtx(c)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get user from context failed", logger.Err(err))
		response.FailResponse(c, "Failed to get user from context")
		return
	}

	var req dto.LikeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("bind like request failed", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(c, "Invalid request data")
		return
	}

	// 调用服务层的IsLiked方法
	isLiked, err := h.LikeService.IsLiked(c.Request.Context(), userID, req.VideoID)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("check video liked failed", logger.Uint("user_id", userID), logger.Uint("video_id", req.VideoID), logger.Err(err))
		response.FailResponse(c, "Failed to check if video is liked")
		return
	}

	response.SuccessResponse(c, isLiked)
}

// ListLikedVideos 列出用户点赞过的视频
// @Summary      获取用户点赞过的视频
// @Description  分页列出指定用户点赞过的视频，使用 (cursor.last_vid, cursor.last_created_at) 复合游标翻页；软鉴权接口，可选携带 Authorization 头以返回当前用户的点赞状态
// @Tags         点赞模块
// @Accept       json
// @Produce      json
// @Param        request body dto.ListLikedVideosRequest true "查询参数，user_id 必填"
// @Success      200 {object} response.Response{data=dto.VideoListResponse} "获取成功"
// @Failure      400 {object} response.Response "请求参数错误或获取失败"
// @Router       /like/list_liked [post]
func (h *LikeHandler) ListLikedVideos(c *gin.Context) {
	// 解析请求参数
	var req dto.ListLikedVideosRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("bind list liked videos request failed", logger.Err(err))
		response.FailResponse(c, fmt.Sprintf("Invalid request parameters: %v", err))
		return
	}

	// 获取当前用户的 userID，如果未登录则为0
	userID := middleware.TryGetUserID(c)
	logger.Ctx(c.Request.Context()).Debug("list liked videos called", logger.Uint("login_user_id", userID), logger.Uint("target_user_id", req.UserID))

	// 调用服务层的ListLikedVideos方法
	resp, err := h.LikeService.ListLikedVideos(c.Request.Context(), req.UserID, userID, req.Cursor, uint(req.PageSize))
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("list liked videos failed", logger.Uint("target_user_id", req.UserID), logger.Err(err))
		response.FailResponse(c, fmt.Sprintf("Failed to list liked videos: %v", err))
		return
	}

	response.SuccessResponse(c, resp)
}
