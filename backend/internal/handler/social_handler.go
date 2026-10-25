package handler

import (
	"feedsystem/internal/dto"
	"feedsystem/internal/handler/middleware"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/response"
	"log"

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
		log.Printf("Failed to bind JSON: %v", err)
		response.FailResponse(ctx, "请求参数错误")
		return
	}

	// 获取uid
	uid, _, err := getUserFromCtx(ctx)
	if err != nil {
		log.Printf("Failed to get user from context: %v", err)
		response.FailAuthResponse(ctx, "未登录或登录已过期")
		return
	}

	// 调用服务层的Follow方法
	if err := h.SocialService.Follow(ctx.Request.Context(), uid, req.BloggerID); err != nil {
		log.Printf("Failed to follow blogger: %v", err)
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
		log.Printf("Failed to bind JSON: %v", err)
		response.FailResponse(ctx, "请求参数错误")
		return
	}

	// 获取uid
	uid, _, err := getUserFromCtx(ctx)
	if err != nil {
		log.Printf("Failed to get user from context: %v", err)
		response.FailAuthResponse(ctx, "未登录或登录已过期")
		return
	}

	// 调用服务层的Unfollow方法
	if err := h.SocialService.Unfollow(ctx.Request.Context(), uid, req.BloggerID); err != nil {
		log.Printf("Failed to unfollow blogger: %v", err)
		response.FailResponse(ctx, "取消关注失败")
		return
	}

	response.SuccessResponse(ctx, "Unfollowed successfully")
}

// GetBloggers 获取用户的关注列表
func (h *SocialHandler) GetBloggers(ctx *gin.Context) {
	// 获取查询参数
	var GetBloggersReq dto.GetBloggersRequest
	if err := ctx.ShouldBindQuery(&GetBloggersReq); err != nil {
		log.Printf("Failed to bind query parameters: %v", err)
		response.FailResponse(ctx, "请求参数错误")
		return
	}

	// 软鉴权
	uid := middleware.TryGetUID(ctx)

	cursor := dto.FeedCursor{
		LastUid:       GetBloggersReq.CursorLastUid,
		LastCreatedAt: GetBloggersReq.CursorLastCreatedAt,
	}

	// 调用服务层的GetBloggers方法
	bloggers, total, nextCursor, err := h.SocialService.GetBloggers(ctx.Request.Context(), GetBloggersReq.Uid, uid, cursor, GetBloggersReq.PageSize)
	if err != nil {
		log.Printf("Failed to get bloggers: %v", err)
		response.FailResponse(ctx, "获取关注列表失败")
		return
	}

	payload := gin.H{
		"bloggers": bloggers,
		"total":    total,
	}
	if nextCursor != nil {
		payload["next_cursor"] = nextCursor
	}
	response.SuccessResponse(ctx, payload)
}

// GetFollowers 获取用户的粉丝列表
func (h *SocialHandler) GetFollowers(ctx *gin.Context) {
	// 获取查询参数
	var GetFollowersReq dto.GetFollowersRequest
	if err := ctx.ShouldBindQuery(&GetFollowersReq); err != nil {
		log.Printf("Failed to bind query parameters: %v", err)
		response.FailResponse(ctx, "请求参数错误")
		return
	}

	// 软鉴权
	uid := middleware.TryGetUID(ctx)

	cursor := dto.FeedCursor{
		LastUid:       GetFollowersReq.CursorLastUid,
		LastCreatedAt: GetFollowersReq.CursorLastCreatedAt,
	}

	// 调用服务层的GetFollowers方法
	followers, total, nextCursor, err := h.SocialService.GetFollowers(ctx.Request.Context(), GetFollowersReq.Uid, uid, cursor, GetFollowersReq.PageSize)
	if err != nil {
		log.Printf("Failed to get followers: %v", err)
		response.FailResponse(ctx, "获取粉丝列表失败")
		return
	}

	payload := gin.H{
		"followers": followers,
		"total":     total,
	}
	if nextCursor != nil {
		payload["next_cursor"] = nextCursor
	}
	response.SuccessResponse(ctx, payload)
}

// IsFollow 获取两者关注关系
func (h *SocialHandler) IsFollow(ctx *gin.Context) {
	// 获取查询参数
	var req dto.IsFollowRequest
	if err := ctx.ShouldBindQuery(&req); err != nil {
		log.Printf("Failed to bind query parameters: %v", err)
		response.FailResponse(ctx, "请求参数错误")
		return
	}

	// 获取uid
	uid, _, err := getUserFromCtx(ctx)
	if err != nil {
		log.Printf("Failed to get user from context: %v", err)
		response.FailAuthResponse(ctx, "未登录或登录已过期")
		return
	}

	// 调用服务层的IsFollow方法
	isFollow, isFan, err := h.SocialService.IsFollow(ctx.Request.Context(), uid, uint(req.TargetID))
	if err != nil {
		log.Printf("Failed to check follow status: %v", err)
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
	var req dto.GetCountsRequest
	if err := ctx.ShouldBindQuery(&req); err != nil {
		log.Printf("Failed to bind query parameters: %v", err)
		response.FailResponse(ctx, "请求参数错误")
		return
	}

	// 调用服务层的GetCounts方法
	followCount, fanCount, err := h.SocialService.GetCounts(ctx.Request.Context(), req.TargetID)
	if err != nil {
		log.Printf("Failed to get counts: %v", err)
		response.FailResponse(ctx, "获取关注数量失败")
		return
	}

	response.SuccessResponse(ctx, gin.H{
		"follow_count": followCount,
		"fan_count":    fanCount,
	})
}
