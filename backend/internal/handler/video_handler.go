package handler

import (
	"feedsystem/internal/dto"
	"feedsystem/internal/handler/middleware"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/response"
	"fmt"
	"log"
	"strconv"

	"github.com/gin-gonic/gin"
)

type VideoHandler struct {
	VideoService *service.VideoService
}

func NewVideoHandler(videoService *service.VideoService) *VideoHandler {
	return &VideoHandler{VideoService: videoService}
}

// UploadVideo 视频上传接口
func (h *VideoHandler) UploadVideo(ctx *gin.Context) {
	// 获取用户ID
	userID, _, err := getUserFromCtx(ctx)
	if err != nil {
		log.Printf("Failed to get user from context: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to get user from context: %v", err))
		return
	}

	// 从表单中获取视频文件
	fh, err := ctx.FormFile("file")
	if err != nil {
		log.Printf("Failed to get file from form: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to get file from form: %v", err))
		return
	}

	// 调用 VideoService 的 Upload 方法处理视频上传
	videoUrl, err := h.VideoService.UploadVideo(ctx.Request.Context(), userID, fh)
	if err != nil {
		log.Printf("Failed to upload video: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to upload video: %v", err))
		return
	}

	response.SuccessResponse(ctx, gin.H{"video_url": videoUrl})
}

// UploadCover 视频封面上传接口
func (h *VideoHandler) UploadCover(ctx *gin.Context) {
	// 获取用户ID
	userID, _, err := getUserFromCtx(ctx)
	if err != nil {
		log.Printf("Failed to get user from context: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to get user from context: %v", err))
		return
	}

	// 从表单中获取封面文件
	fh, err := ctx.FormFile("file")
	if err != nil {
		log.Printf("Failed to get file from form: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to get file from form: %v", err))
		return
	}

	// 调用 VideoService 的 UploadCover 方法处理封面上传
	coverUrl, err := h.VideoService.UploadCover(ctx.Request.Context(), userID, fh)
	if err != nil {
		log.Printf("Failed to upload cover: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to upload cover: %v", err))
		return
	}

	response.SuccessResponse(ctx, gin.H{"cover_url": coverUrl})
}

// PublishVideo 发布视频接口
func (h *VideoHandler) PublishVideo(ctx *gin.Context) {
	// 获取用户ID
	userID, _, err := getUserFromCtx(ctx)
	if err != nil {
		log.Printf("Failed to get user from context: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to get user from context: %v", err))
		return
	}

	// 解析请求参数
	var req dto.PublishVideoRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		log.Printf("Failed to bind request JSON: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to bind request JSON: %v", err))
		return
	}

	// 调用服务层实现
	videoID, err := h.VideoService.PublishVideo(ctx.Request.Context(), userID, req.Title, req.Description, req.VideoURL, req.CoverURL)
	if err != nil {
		log.Printf("Failed to publish video: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to publish video: %v", err))
		return
	}

	response.SuccessResponse(ctx, videoID)
}

// VideoDetail 获取单条视频详情，软鉴权
func (h *VideoHandler) VideoDetail(ctx *gin.Context) {
	// 获取video id
	videoIDStr := ctx.Param("id")
	videoID, err := strconv.ParseUint(videoIDStr, 10, 64)
	if err != nil || videoID == 0 {
		response.FailResponse(ctx, "video id is invalid")
		return
	}

	// 尝试获取用户id，解析失败不报错，游客继续访问
	uid, err := middleware.TryGetUID(ctx), nil

	// 调用service处理
	detail, err := h.VideoService.VideoDetail(ctx, uint(videoID), uid)
	if err != nil {
		response.FailResponse(ctx, "视频不存在")
		return
	}
	response.SuccessResponse(ctx, detail)
}
