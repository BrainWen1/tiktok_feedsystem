package handler

import (
	"errors"
	"feedsystem/internal/dto"
	"feedsystem/internal/handler/middleware"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/logger"
	"feedsystem/internal/utils/response"
	"strconv"
	"time"

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
		logger.Error("get user from context failed", logger.Err(err))
		response.FailAuthResponse(ctx, "未登录或登录已过期")
		return
	}

	// 从表单中获取视频文件
	fh, err := ctx.FormFile("file")
	if err != nil {
		logger.Warn("get upload file from form failed", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(ctx, "文件上传参数错误")
		return
	}

	// 调用 VideoService 的 Upload 方法处理视频上传
	videoURL, err := h.VideoService.UploadVideo(ctx.Request.Context(), userID, fh)
	if err != nil {
		logger.Error("upload video failed", logger.Uint("user_id", userID), logger.Err(err))
		switch {
		case errors.Is(err, service.ErrInvalidFileSize):
			response.FailResponse(ctx, "文件大小不符合要求")
		case errors.Is(err, service.ErrInvalidFileType):
			response.FailResponse(ctx, "视频格式不合法，仅支持 mp4/avi/mov/mkv")
		default:
			response.ServerErrorResponse(ctx, "视频上传失败，请稍后重试")
		}
		return
	}

	response.SuccessResponse(ctx, gin.H{"video_url": videoURL})
}

// UploadCover 视频封面上传接口
func (h *VideoHandler) UploadCover(ctx *gin.Context) {
	// 获取用户ID
	userID, _, err := getUserFromCtx(ctx)
	if err != nil {
		logger.Error("get user from context failed", logger.Err(err))
		response.FailAuthResponse(ctx, "未登录或登录已过期")
		return
	}

	// 从表单中获取封面文件
	fh, err := ctx.FormFile("file")
	if err != nil {
		logger.Warn("get upload file from form failed", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(ctx, "文件上传参数错误")
		return
	}

	// 调用 VideoService 的 UploadCover 方法处理封面上传
	coverURL, err := h.VideoService.UploadCover(ctx.Request.Context(), userID, fh)
	if err != nil {
		logger.Error("upload cover failed", logger.Uint("user_id", userID), logger.Err(err))
		switch {
		case errors.Is(err, service.ErrInvalidFileSize):
			response.FailResponse(ctx, "文件大小不符合要求")
		case errors.Is(err, service.ErrInvalidFileType):
			response.FailResponse(ctx, "封面格式不合法，仅支持 jpg/jpeg/png/webp")
		default:
			response.ServerErrorResponse(ctx, "封面上传失败，请稍后重试")
		}
		return
	}

	response.SuccessResponse(ctx, gin.H{"cover_url": coverURL})
}

// PublishVideo 发布视频接口
func (h *VideoHandler) PublishVideo(ctx *gin.Context) {
	// 获取用户ID
	userID, _, err := getUserFromCtx(ctx)
	if err != nil {
		logger.Error("get user from context failed", logger.Err(err))
		response.FailAuthResponse(ctx, "未登录或登录已过期")
		return
	}

	// 解析请求参数
	var req dto.PublishVideoRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		logger.Warn("bind publish video request failed", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(ctx, "请求参数错误")
		return
	}

	// 调用服务层实现
	videoID, err := h.VideoService.PublishVideo(ctx.Request.Context(), userID, req.Title, req.Description, req.VideoURL, req.CoverURL, req.Tags)
	if err != nil {
		logger.Error("publish video failed", logger.Uint("user_id", userID), logger.Err(err))
		response.ServerErrorResponse(ctx, "视频发布失败，请稍后重试")
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
		logger.Warn("invalid video id", logger.String("video_id", videoIDStr), logger.Err(err))
		response.FailResponse(ctx, "视频ID不合法")
		return
	}

	// 尝试获取用户ID，解析失败不报错，游客继续访问
	userID := middleware.TryGetUserID(ctx)

	// 调用service处理
	detail, err := h.VideoService.VideoDetail(ctx, uint(videoID), userID)
	if err != nil {
		logger.Error("get video detail failed", logger.Uint("video_id", uint(videoID)), logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(ctx, "视频不存在")
		return
	}
	response.SuccessResponse(ctx, detail)
}

// VideoList 获取视频列表，软鉴权
func (h *VideoHandler) VideoList(ctx *gin.Context) {
	// 软鉴权
	userID := middleware.TryGetUserID(ctx)

	//从url query拿参数
	// 分页参数：page_num 仅为兼容旧客户端保留，实际使用 cursor 复合游标
	var req struct {
		AuthorID            uint      `form:"author_id" binding:"required"`
		PageNum             int       `form:"page_num" binding:"omitempty,min=1"`
		PageSize            int       `form:"page_size" binding:"omitempty,min=1"`
		CursorLastVid       string    `form:"cursor.last_vid"`
		CursorLastCreatedAt time.Time `form:"cursor.last_created_at"`
	}
	if err := ctx.ShouldBindQuery(&req); err != nil {
		logger.Warn("bind video list query failed", logger.Uint("author_id", req.AuthorID), logger.Err(err))
		response.FailResponse(ctx, "请求参数错误")
		return
	}

	cursor := dto.FeedCursor{
		LastVid:       req.CursorLastVid,
		LastCreatedAt: req.CursorLastCreatedAt,
	}

	// 调用service处理
	resp, err := h.VideoService.VideoList(ctx.Request.Context(), req.AuthorID, userID, cursor, req.PageSize)
	if err != nil {
		logger.Error("list videos failed", logger.Uint("author_id", req.AuthorID), logger.Err(err))
		response.ServerErrorResponse(ctx, "获取视频列表失败，请稍后重试")
		return
	}

	response.SuccessResponse(ctx, resp)
}
