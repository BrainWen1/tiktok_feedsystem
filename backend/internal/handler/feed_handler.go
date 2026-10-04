package handler

import (
	"feedsystem/internal/dto"
	"feedsystem/internal/handler/middleware"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/response"
	"fmt"

	"github.com/gin-gonic/gin"
)

type FeedHandler struct {
	feedSvc *service.FeedService
}

func NewFeedHandler(feedSvc *service.FeedService) *FeedHandler {
	return &FeedHandler{feedSvc: feedSvc}
}

// FeedListLatest 获取最新视频列表，按时间倒序
func (h *FeedHandler) FeedListLatest(c *gin.Context) {
	// 解析请求参数
	var req dto.FeedListReq
	if err := c.ShouldBindQuery(&req); err != nil {
		response.FailResponse(c, fmt.Sprintf("Failed to bind query parameters: %v", err))
		return
	}

	// 获取登录用户ID，如果未登录则为0
	loginUid := middleware.TryGetUID(c)

	// 调用服务层获取视频列表
	list, err := h.feedSvc.ListLatest(loginUid, req.LastVid, req.Limit)
	if err != nil {
		response.FailResponse(c, fmt.Sprintf("Failed to fetch latest feeds: %v", err))
		return
	}
	response.SuccessResponse(c, list)
}

// FeedListByFollowing 获取我关注用户发布的视频
func (h *FeedHandler) FeedListByFollowing(c *gin.Context) {
	// 解析请求参数
	var req dto.FeedListReq
	if err := c.ShouldBindQuery(&req); err != nil {
		response.FailResponse(c, fmt.Sprintf("Failed to bind query parameters: %v", err))
		return
	}

	// 获取登录用户ID，如果未登录则为0
	loginUid := middleware.TryGetUID(c)

	// 调用服务层获取视频列表
	list, err := h.feedSvc.ListByFollowing(loginUid, req.LastVid, req.Limit)
	if err != nil {
		response.FailResponse(c, fmt.Sprintf("Failed to fetch following feeds: %v", err))
		return
	}
	response.SuccessResponse(c, list)
}

// FeedListByPopularity 获取热门视频列表
func (h *FeedHandler) FeedListByPopularity(c *gin.Context) {
	// 解析请求参数
	var req dto.FeedListReq
	if err := c.ShouldBindQuery(&req); err != nil {
		response.FailResponse(c, fmt.Sprintf("Failed to bind query parameters: %v", err))
		return
	}

	// 获取登录用户ID，如果未登录则为0
	loginUid := middleware.TryGetUID(c)

	// 调用服务层获取视频列表
	list, err := h.feedSvc.ListByPopularity(loginUid, req.LastVid, req.Limit)
	if err != nil {
		response.FailResponse(c, fmt.Sprintf("Failed to fetch popular feeds: %v", err))
		return
	}
	response.SuccessResponse(c, list)
}

// FeedListLikesCount 按视频点赞总数降序榜单
func (h *FeedHandler) FeedListLikesCount(c *gin.Context) {
	// 解析请求参数
	var req dto.FeedListReq
	if err := c.ShouldBindQuery(&req); err != nil {
		response.FailResponse(c, fmt.Sprintf("Failed to bind query parameters: %v", err))
		return
	}

	// 获取登录用户ID，如果未登录则为0
	loginUid := middleware.TryGetUID(c)

	// 调用服务层获取视频列表
	list, err := h.feedSvc.ListLikesCount(loginUid, req.LastVid, req.Limit)
	if err != nil {
		response.FailResponse(c, fmt.Sprintf("Failed to fetch likes count feeds: %v", err))
		return
	}
	response.SuccessResponse(c, list)
}

// FeedListByTag 根据标签筛选视频
func (h *FeedHandler) FeedListByTag(c *gin.Context) {
	// 解析请求参数
	var req dto.FeedListReq
	if err := c.ShouldBindQuery(&req); err != nil {
		response.FailResponse(c, fmt.Sprintf("Failed to bind query parameters: %v", err))
		return
	}

	// 获取登录用户ID，如果未登录则为0
	loginUid := middleware.TryGetUID(c)

	// 调用服务层获取视频列表
	list, err := h.feedSvc.ListByTag(loginUid, req.Tag, req.LastVid, req.Limit)
	if err != nil {
		response.FailResponse(c, fmt.Sprintf("Failed to fetch feeds by tag: %v", err))
		return
	}
	response.SuccessResponse(c, list)
}
