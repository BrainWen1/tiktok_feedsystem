package handler

import (
	"feedsystem/internal/dto"
	"feedsystem/internal/handler/middleware"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/logger"
	"feedsystem/internal/utils/response"

	"github.com/gin-gonic/gin"
)

type SocialHandler struct {
	SocialService *service.SocialService
}

func NewSocialHandler(socialService *service.SocialService) *SocialHandler {
	return &SocialHandler{SocialService: socialService}
}

// Follow 关注博主
func (h *SocialHandler) Follow(ctx *gin.Context) {
	// 解析请求参数
	var req dto.FollowRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		logger.Warn("bind social request failed", logger.Err(err))
		response.FailResponse(ctx, "请求参数错误")
		return
	}

	// 获取 userID
	userID, ok := requireUserID(ctx)
	if !ok {
		return
	}

	// 调用服务层的Follow方法
	if err := h.SocialService.Follow(ctx.Request.Context(), userID, req.BloggerID); err != nil {
		logger.Error("follow blogger failed", logger.Uint("user_id", userID), logger.Uint("blogger_id", req.BloggerID), logger.Err(err))
		response.FailResponse(ctx, "关注失败")
		return
	}

	response.SuccessResponse(ctx, "Followed successfully")
}

// Unfollow 取消关注博主
func (h *SocialHandler) Unfollow(ctx *gin.Context) {
	// 解析请求参数
	var req dto.UnfollowRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		logger.Warn("bind social request failed", logger.Err(err))
		response.FailResponse(ctx, "请求参数错误")
		return
	}

	// 获取 userID
	userID, ok := requireUserID(ctx)
	if !ok {
		return
	}

	// 调用服务层的Unfollow方法
	if err := h.SocialService.Unfollow(ctx.Request.Context(), userID, req.BloggerID); err != nil {
		logger.Error("unfollow blogger failed", logger.Uint("user_id", userID), logger.Uint("blogger_id", req.BloggerID), logger.Err(err))
		response.FailResponse(ctx, "取消关注失败")
		return
	}

	response.SuccessResponse(ctx, "Unfollowed successfully")
}

// GetBloggers 获取用户的关注列表
func (h *SocialHandler) GetBloggers(ctx *gin.Context) {
	// 获取查询参数
	getBloggersReq, ok := bindQuery[dto.GetBloggersRequest](ctx, "bind social query parameters failed")
	if !ok {
		return
	}

	// 软鉴权
	loginUserID := middleware.TryGetUserID(ctx)

	cursor := dto.FeedCursor{
		LastUserID:    getBloggersReq.CursorLastUserID,
		LastCreatedAt: getBloggersReq.CursorLastCreatedAt,
	}

	// 调用服务层的GetBloggers方法
	bloggers, total, nextCursor, err := h.SocialService.GetBloggers(ctx.Request.Context(), getBloggersReq.UserID, loginUserID, cursor, getBloggersReq.PageSize)
	if err != nil {
		logger.Error("get bloggers failed", logger.Uint("target_user_id", getBloggersReq.UserID), logger.Err(err))
		response.FailResponse(ctx, "获取关注列表失败")
		return
	}

	response.SuccessResponse(ctx, buildSocialListPayload("bloggers", bloggers, total, nextCursor))
}

// GetFollowers 获取用户的粉丝列表
func (h *SocialHandler) GetFollowers(ctx *gin.Context) {
	// 获取查询参数
	getFollowersReq, ok := bindQuery[dto.GetFollowersRequest](ctx, "bind social query parameters failed")
	if !ok {
		return
	}

	// 软鉴权
	loginUserID := middleware.TryGetUserID(ctx)

	cursor := dto.FeedCursor{
		LastUserID:    getFollowersReq.CursorLastUserID,
		LastCreatedAt: getFollowersReq.CursorLastCreatedAt,
	}

	// 调用服务层的GetFollowers方法
	followers, total, nextCursor, err := h.SocialService.GetFollowers(ctx.Request.Context(), getFollowersReq.UserID, loginUserID, cursor, getFollowersReq.PageSize)
	if err != nil {
		logger.Error("get followers failed", logger.Uint("target_user_id", getFollowersReq.UserID), logger.Err(err))
		response.FailResponse(ctx, "获取粉丝列表失败")
		return
	}

	response.SuccessResponse(ctx, buildSocialListPayload("followers", followers, total, nextCursor))
}

// IsFollow 获取两者关注关系
func (h *SocialHandler) IsFollow(ctx *gin.Context) {
	// 获取查询参数
	req, ok := bindQuery[dto.IsFollowRequest](ctx, "bind social query parameters failed")
	if !ok {
		return
	}

	// 获取 userID
	userID, ok := requireUserID(ctx)
	if !ok {
		return
	}

	// 调用服务层的IsFollow方法
	isFollow, isFan, err := h.SocialService.IsFollow(ctx.Request.Context(), userID, uint(req.TargetID))
	if err != nil {
		logger.Error("check follow status failed", logger.Uint("user_id", userID), logger.Uint("target_user_id", req.TargetID), logger.Err(err))
		response.FailResponse(ctx, "获取关注关系失败")
		return
	}

	response.SuccessResponse(ctx, gin.H{
		"is_follow": isFollow,
		"is_fan":    isFan,
	})
}

// GetCounts 获取关注和粉丝数量
func (h *SocialHandler) GetCounts(ctx *gin.Context) {
	// 获取查询参数
	req, ok := bindQuery[dto.GetCountsRequest](ctx, "bind social query parameters failed")
	if !ok {
		return
	}

	// 调用服务层的GetCounts方法
	followCount, fanCount, err := h.SocialService.GetCounts(ctx.Request.Context(), req.TargetID)
	if err != nil {
		logger.Error("get social counts failed", logger.Uint("target_user_id", req.TargetID), logger.Err(err))
		response.FailResponse(ctx, "获取关注数量失败")
		return
	}

	response.SuccessResponse(ctx, gin.H{
		"follow_count": followCount,
		"fan_count":    fanCount,
	})
}
