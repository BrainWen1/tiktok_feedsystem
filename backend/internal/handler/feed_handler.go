package handler

import (
	"feedsystem/internal/service"
	"feedsystem/internal/utils/logger"
	"feedsystem/internal/utils/response"

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
	// 解析请求参数并获取登录用户ID（游客为0）
	req, loginUserID, ok := bindFeedListQuery(c)
	if !ok {
		return
	}

	// 调用服务层获取视频列表
	list, err := h.feedSvc.ListLatest(c.Request.Context(), loginUserID, req.Cursor, req.Limit)
	if err != nil {
		logger.Error("fetch latest feeds failed", logger.Uint("login_user_id", loginUserID), logger.Err(err))
		response.ServerErrorResponse(c, "获取视频流失败，请稍后重试")
		return
	}
	response.SuccessResponse(c, list)
}

// FeedListByFollowing 获取我关注用户发布的视频
func (h *FeedHandler) FeedListByFollowing(c *gin.Context) {
	// 解析请求参数并获取登录用户ID（游客为0）
	req, loginUserID, ok := bindFeedListQuery(c)
	if !ok {
		return
	}

	// 调用服务层获取视频列表
	list, err := h.feedSvc.ListByFollowing(c.Request.Context(), loginUserID, req.Cursor, req.Limit)
	if err != nil {
		logger.Error("fetch following feeds failed", logger.Uint("login_user_id", loginUserID), logger.Err(err))
		response.ServerErrorResponse(c, "获取关注视频流失败，请稍后重试")
		return
	}
	response.SuccessResponse(c, list)
}

// FeedListByPopularity 获取热门视频列表
func (h *FeedHandler) FeedListByPopularity(c *gin.Context) {
	// 解析请求参数并获取登录用户ID（游客为0）
	req, loginUserID, ok := bindFeedListQuery(c)
	if !ok {
		return
	}

	// 调用服务层获取视频列表
	list, err := h.feedSvc.ListByPopularity(c.Request.Context(), loginUserID, req.Cursor, req.Limit)
	if err != nil {
		logger.Error("fetch popular feeds failed", logger.Uint("login_user_id", loginUserID), logger.Err(err))
		response.ServerErrorResponse(c, "获取热门视频流失败，请稍后重试")
		return
	}
	response.SuccessResponse(c, list)
}

// FeedListLikesCount 按视频点赞总数降序榜单
func (h *FeedHandler) FeedListLikesCount(c *gin.Context) {
	// 解析请求参数并获取登录用户ID（游客为0）
	req, loginUserID, ok := bindFeedListQuery(c)
	if !ok {
		return
	}

	// 调用服务层获取视频列表
	list, err := h.feedSvc.ListByLikesCount(c.Request.Context(), loginUserID, req.Cursor, req.Limit)
	if err != nil {
		logger.Error("fetch likes count feeds failed", logger.Uint("login_user_id", loginUserID), logger.Err(err))
		response.ServerErrorResponse(c, "获取点赞榜单失败，请稍后重试")
		return
	}
	response.SuccessResponse(c, list)
}

// FeedListByTag 根据标签筛选视频
func (h *FeedHandler) FeedListByTag(c *gin.Context) {
	// 解析请求参数并获取登录用户ID（游客为0）
	req, loginUserID, ok := bindFeedListQuery(c)
	if !ok {
		return
	}

	// 调用服务层获取视频列表
	list, err := h.feedSvc.ListByTag(c.Request.Context(), loginUserID, req.Tag, req.Cursor, req.Limit)
	if err != nil {
		logger.Error("fetch feeds by tag failed", logger.Uint("login_user_id", loginUserID), logger.String("tag", req.Tag), logger.Err(err))
		response.ServerErrorResponse(c, "获取标签视频流失败，请稍后重试")
		return
	}
	response.SuccessResponse(c, list)
}
