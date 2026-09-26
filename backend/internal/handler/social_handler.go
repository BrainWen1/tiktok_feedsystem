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
		response.FailResponse(ctx, err.Error())
		return
	}

	// 获取uid
	uid, _, err := getUserFromCtx(ctx)
	if err != nil {
		log.Printf("Failed to get user from context: %v", err)
		response.FailResponse(ctx, err.Error())
		return
	}

	// 调用服务层的Follow方法
	if err := h.SocialService.Follow(ctx.Request.Context(), uid, req.BloggerID); err != nil {
		log.Printf("Failed to follow blogger: %v", err)
		response.FailResponse(ctx, err.Error())
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
		response.FailResponse(ctx, err.Error())
		return
	}

	// 获取uid
	uid, _, err := getUserFromCtx(ctx)
	if err != nil {
		log.Printf("Failed to get user from context: %v", err)
		response.FailResponse(ctx, err.Error())
		return
	}

	// 调用服务层的Unfollow方法
	if err := h.SocialService.Unfollow(ctx.Request.Context(), uid, req.BloggerID); err != nil {
		log.Printf("Failed to unfollow blogger: %v", err)
		response.FailResponse(ctx, err.Error())
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
		response.FailResponse(ctx, err.Error())
		return
	}

	// 软鉴权
	uid := middleware.TryGetUID(ctx)

	// 调用服务层的GetBloggers方法
	bloggers, total, err := h.SocialService.GetBloggers(ctx.Request.Context(), GetBloggersReq.Uid, GetBloggersReq.PageNum, GetBloggersReq.PageSize, uid)
	if err != nil {
		log.Printf("Failed to get bloggers: %v", err)
		response.FailResponse(ctx, err.Error())
		return
	}

	response.SuccessResponse(ctx, gin.H{
		"bloggers": bloggers,
		"total":    total,
	})
}

// GetFollowers 获取用户的粉丝列表
func (h *SocialHandler) GetFollowers(ctx *gin.Context) {
	// 获取查询参数
	var GetFollowersReq dto.GetFollowersRequest
	if err := ctx.ShouldBindQuery(&GetFollowersReq); err != nil {
		log.Printf("Failed to bind query parameters: %v", err)
		response.FailResponse(ctx, err.Error())
		return
	}

	// 软鉴权
	uid := middleware.TryGetUID(ctx)

	// 调用服务层的GetBloggers方法
	followers, total, err := h.SocialService.GetFollowers(ctx.Request.Context(), GetFollowersReq.Uid, GetFollowersReq.PageNum, GetFollowersReq.PageSize, uid)
	if err != nil {
		log.Printf("Failed to get followers: %v", err)
		response.FailResponse(ctx, err.Error())
		return
	}

	response.SuccessResponse(ctx, gin.H{
		"followers": followers,
		"total":     total,
	})
}
