package handler

import (
	"feedsystem/internal/dto"
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
