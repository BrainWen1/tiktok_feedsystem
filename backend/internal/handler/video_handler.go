package handler

import (
	"feedsystem/internal/service"
	"feedsystem/internal/utils/response"
	"fmt"
	"log"

	"github.com/gin-gonic/gin"
)

type VideoHandler struct {
	VideoService *service.VideoService
}

func NewVideoHandler(videoService *service.VideoService) *VideoHandler {
	return &VideoHandler{VideoService: videoService}
}

// Upload 视频上传接口
func (h *VideoHandler) Upload(ctx *gin.Context) {
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
	videoUrl, err := h.VideoService.Upload(userID, fh)
	if err != nil {
		log.Printf("Failed to upload video: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to upload video: %v", err))
		return
	}

	response.SuccessResponse(ctx, gin.H{"video_url": videoUrl})
}
