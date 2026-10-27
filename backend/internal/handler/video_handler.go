package handler

import (
	"errors"
	"feedsystem/internal/dto"
	"feedsystem/internal/handler/middleware"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/logger"
	"feedsystem/internal/utils/response"
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
func (h *VideoHandler) UploadVideo(c *gin.Context) {
	// 获取用户ID
	userID, _, err := getUserFromCtx(c)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get user from context failed", logger.Err(err))
		response.FailAuthResponse(c, "未登录或登录已过期")
		return
	}

	// 从表单中获取视频文件
	fh, err := c.FormFile("file")
	if err != nil {
		logger.Ctx(c.Request.Context()).Warn("get upload file from form failed", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(c, "文件上传参数错误")
		return
	}

	// 调用 VideoService 的 Upload 方法处理视频上传
	videoURL, err := h.VideoService.UploadVideo(c.Request.Context(), userID, fh)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("upload video failed", logger.Uint("user_id", userID), logger.Err(err))
		switch {
		case errors.Is(err, service.ErrInvalidFileSize):
			response.FailResponse(c, "文件大小不符合要求")
		case errors.Is(err, service.ErrInvalidFileType):
			response.FailResponse(c, "视频格式不合法，仅支持 mp4/avi/mov/mkv")
		default:
			response.ServerErrorResponse(c, "视频上传失败，请稍后重试")
		}
		return
	}

	response.SuccessResponse(c, gin.H{"video_url": videoURL})
}

// UploadCover 视频封面上传接口
func (h *VideoHandler) UploadCover(c *gin.Context) {
	// 获取用户ID
	userID, _, err := getUserFromCtx(c)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get user from context failed", logger.Err(err))
		response.FailAuthResponse(c, "未登录或登录已过期")
		return
	}

	// 从表单中获取封面文件
	fh, err := c.FormFile("file")
	if err != nil {
		logger.Ctx(c.Request.Context()).Warn("get upload file from form failed", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(c, "文件上传参数错误")
		return
	}

	// 调用 VideoService 的 UploadCover 方法处理封面上传
	coverURL, err := h.VideoService.UploadCover(c.Request.Context(), userID, fh)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("upload cover failed", logger.Uint("user_id", userID), logger.Err(err))
		switch {
		case errors.Is(err, service.ErrInvalidFileSize):
			response.FailResponse(c, "文件大小不符合要求")
		case errors.Is(err, service.ErrInvalidFileType):
			response.FailResponse(c, "封面格式不合法，仅支持 jpg/jpeg/png/webp")
		default:
			response.ServerErrorResponse(c, "封面上传失败，请稍后重试")
		}
		return
	}

	response.SuccessResponse(c, gin.H{"cover_url": coverURL})
}

// PublishVideo 发布视频接口
func (h *VideoHandler) PublishVideo(c *gin.Context) {
	// 获取用户ID
	userID, _, err := getUserFromCtx(c)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get user from context failed", logger.Err(err))
		response.FailAuthResponse(c, "未登录或登录已过期")
		return
	}

	// 解析请求参数
	var req dto.PublishVideoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("bind publish video request failed", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(c, "请求参数错误")
		return
	}

	// 调用服务层实现
	videoID, err := h.VideoService.PublishVideo(c.Request.Context(), userID, req.Title, req.Description, req.VideoURL, req.CoverURL, req.Tags)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("publish video failed", logger.Uint("user_id", userID), logger.Err(err))
		response.ServerErrorResponse(c, "视频发布失败，请稍后重试")
		return
	}

	response.SuccessResponse(c, videoID)
}

// VideoDetail 获取单条视频详情，软鉴权
func (h *VideoHandler) VideoDetail(c *gin.Context) {
	// 获取video id
	videoIDStr := c.Param("id")
	videoID, err := strconv.ParseUint(videoIDStr, 10, 64)
	if err != nil || videoID == 0 {
		logger.Ctx(c.Request.Context()).Warn("invalid video id", logger.String("video_id", videoIDStr), logger.Err(err))
		response.FailResponse(c, "视频ID不合法")
		return
	}

	// 尝试获取用户ID，解析失败不报错，游客继续访问
	userID := middleware.TryGetUserID(c)

	// 调用service处理
	detail, err := h.VideoService.VideoDetail(c.Request.Context(), uint(videoID), userID)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get video detail failed", logger.Uint("video_id", uint(videoID)), logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(c, "视频不存在")
		return
	}
	response.SuccessResponse(c, detail)
}

// VideoList 获取视频列表，软鉴权
func (h *VideoHandler) VideoList(c *gin.Context) {
	// 软鉴权
	userID := middleware.TryGetUserID(c)

	// 从 url query 拿参数
	var req dto.VideoListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("bind video list query failed", logger.Uint("author_id", req.AuthorID), logger.Err(err))
		response.FailResponse(c, "请求参数错误")
		return
	}

	cursor := dto.FeedCursor{
		LastVid:       req.CursorLastVid,
		LastCreatedAt: req.CursorLastCreatedAt,
	}

	// 调用service处理
	resp, err := h.VideoService.VideoList(c.Request.Context(), req.AuthorID, userID, cursor, req.PageSize)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("list videos failed", logger.Uint("author_id", req.AuthorID), logger.Err(err))
		response.ServerErrorResponse(c, "获取视频列表失败，请稍后重试")
		return
	}

	response.SuccessResponse(c, resp)
}
