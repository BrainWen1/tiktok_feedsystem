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
// @Summary      获取最新视频流
// @Description  按创建时间倒序返回视频流，使用 (cursor.last_created_at, cursor.last_vid) 复合游标翻页；软鉴权接口，可选携带 Authorization 头以返回点赞/关注状态
// @Tags         动态流模块
// @Produce      json
// @Param        limit                  query int    false "每页数量"
// @Param        cursor.last_vid        query string false "上一页最后一条视频 ID（游标）"
// @Param        cursor.last_created_at query string false "上一页最后一条视频创建时间（RFC3339，游标）"
// @Success      200 {object} response.Response{data=dto.FeedListResp} "获取成功"
// @Failure      400 {object} response.Response "请求参数错误"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /feed/list [get]
func (h *FeedHandler) FeedListLatest(c *gin.Context) {
	// 解析请求参数并获取登录用户ID（游客为0）
	req, loginUserID, ok := bindFeedListQuery(c)
	if !ok {
		return
	}

	// 调用服务层获取视频列表
	list, err := h.feedSvc.ListLatest(c.Request.Context(), loginUserID, req.Cursor, req.Limit)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("fetch latest feeds failed", logger.Uint("login_user_id", loginUserID), logger.Err(err))
		response.ServerErrorResponse(c, "获取视频流失败，请稍后重试")
		return
	}
	response.SuccessResponse(c, list)
}

// FeedListByFollowing 获取我关注用户发布的视频
// @Summary      获取关注博主的视频流
// @Description  返回当前登录用户所关注博主发布的视频，使用 (cursor.last_created_at, cursor.last_vid) 复合游标翻页
// @Tags         动态流模块
// @Produce      json
// @Security     Bearer
// @Param        limit                  query int    false "每页数量"
// @Param        cursor.last_vid        query string false "上一页最后一条视频 ID（游标）"
// @Param        cursor.last_created_at query string false "上一页最后一条视频创建时间（RFC3339，游标）"
// @Success      200 {object} response.Response{data=dto.FeedListResp} "获取成功"
// @Failure      400 {object} response.Response "请求参数错误"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /feed/listByFollowing [get]
func (h *FeedHandler) FeedListByFollowing(c *gin.Context) {
	// 解析请求参数并获取登录用户ID（游客为0）
	req, loginUserID, ok := bindFeedListQuery(c)
	if !ok {
		return
	}

	// 调用服务层获取视频列表
	list, err := h.feedSvc.ListByFollowing(c.Request.Context(), loginUserID, req.Cursor, req.Limit)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("fetch following feeds failed", logger.Uint("login_user_id", loginUserID), logger.Err(err))
		response.ServerErrorResponse(c, "获取关注视频流失败，请稍后重试")
		return
	}
	response.SuccessResponse(c, list)
}

// FeedListByPopularity 获取热门视频列表
// @Summary      获取热门视频流
// @Description  按热度值（popularity）倒序返回视频流，使用 (cursor.last_popularity, cursor.last_vid) 复合游标翻页；软鉴权接口，可选携带 Authorization 头以返回点赞/关注状态
// @Tags         动态流模块
// @Produce      json
// @Param        limit                   query int    false "每页数量"
// @Param        cursor.last_vid         query string false "上一页最后一条视频 ID（游标）"
// @Param        cursor.last_popularity  query int    false "上一页最后一条视频的热度值（游标）"
// @Success      200 {object} response.Response{data=dto.FeedListResp} "获取成功"
// @Failure      400 {object} response.Response "请求参数错误"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /feed/listByPopularity [get]
func (h *FeedHandler) FeedListByPopularity(c *gin.Context) {
	// 解析请求参数并获取登录用户ID（游客为0）
	req, loginUserID, ok := bindFeedListQuery(c)
	if !ok {
		return
	}

	// 调用服务层获取视频列表
	list, err := h.feedSvc.ListByPopularity(c.Request.Context(), loginUserID, req.Cursor, req.Limit)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("fetch popular feeds failed", logger.Uint("login_user_id", loginUserID), logger.Err(err))
		response.ServerErrorResponse(c, "获取热门视频流失败，请稍后重试")
		return
	}
	response.SuccessResponse(c, list)
}

// FeedListLikesCount 按视频点赞总数降序榜单
// @Summary      获取点赞数榜单视频流
// @Description  按视频点赞总数降序返回视频流，使用 (cursor.last_likes_count, cursor.last_vid) 复合游标翻页；软鉴权接口，可选携带 Authorization 头以返回点赞/关注状态
// @Tags         动态流模块
// @Produce      json
// @Param        limit                    query int    false "每页数量"
// @Param        cursor.last_vid          query string false "上一页最后一条视频 ID（游标）"
// @Param        cursor.last_likes_count  query int    false "上一页最后一条视频的点赞总数（游标）"
// @Success      200 {object} response.Response{data=dto.FeedListResp} "获取成功"
// @Failure      400 {object} response.Response "请求参数错误"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /feed/listByLikesCount [get]
func (h *FeedHandler) FeedListLikesCount(c *gin.Context) {
	// 解析请求参数并获取登录用户ID（游客为0）
	req, loginUserID, ok := bindFeedListQuery(c)
	if !ok {
		return
	}

	// 调用服务层获取视频列表
	list, err := h.feedSvc.ListByLikesCount(c.Request.Context(), loginUserID, req.Cursor, req.Limit)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("fetch likes count feeds failed", logger.Uint("login_user_id", loginUserID), logger.Err(err))
		response.ServerErrorResponse(c, "获取点赞榜单失败，请稍后重试")
		return
	}
	response.SuccessResponse(c, list)
}

// FeedListByTag 根据标签筛选视频
// @Summary      按标签获取视频流
// @Description  根据标签筛选视频，按创建时间倒序返回，使用 (cursor.last_created_at, cursor.last_vid) 复合游标翻页；软鉴权接口，可选携带 Authorization 头以返回点赞/关注状态
// @Tags         动态流模块
// @Produce      json
// @Param        tag                    query string true  "标签名称"
// @Param        limit                  query int    false "每页数量"
// @Param        cursor.last_vid        query string false "上一页最后一条视频 ID（游标）"
// @Param        cursor.last_created_at query string false "上一页最后一条视频创建时间（RFC3339，游标）"
// @Success      200 {object} response.Response{data=dto.FeedListResp} "获取成功"
// @Failure      400 {object} response.Response "请求参数错误"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /feed/listByTag [get]
func (h *FeedHandler) FeedListByTag(c *gin.Context) {
	// 解析请求参数并获取登录用户ID（游客为0）
	req, loginUserID, ok := bindFeedListQuery(c)
	if !ok {
		return
	}

	// 调用服务层获取视频列表
	list, err := h.feedSvc.ListByTag(c.Request.Context(), loginUserID, req.Tag, req.Cursor, req.Limit)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("fetch feeds by tag failed", logger.Uint("login_user_id", loginUserID), logger.String("tag", req.Tag), logger.Err(err))
		response.ServerErrorResponse(c, "获取标签视频流失败，请稍后重试")
		return
	}
	response.SuccessResponse(c, list)
}
