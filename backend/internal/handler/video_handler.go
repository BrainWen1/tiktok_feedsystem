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
// @Summary      上传视频文件
// @Description  上传视频原始文件（multipart/form-data，字段名 file），成功后返回可直接用于发布接口的访问地址；仅支持 mp4/avi/mov/mkv，单文件约 100MB 以内
// @Tags         视频模块
// @Accept       multipart/form-data
// @Produce      json
// @Security     Bearer
// @Param        file formData file true "视频文件（mp4/avi/mov/mkv）"
// @Success      200 {object} response.Response{data=map[string]interface{}} "上传成功，data 包含 video_url 字段"
// @Failure      400 {object} response.Response "文件参数错误、大小不符合要求或格式不合法"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /video/upload_video [post]
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
// @Summary      上传视频封面
// @Description  上传视频封面图片（multipart/form-data，字段名 file），成功后返回可直接用于发布接口的封面地址；仅支持 jpg/jpeg/png/webp
// @Tags         视频模块
// @Accept       multipart/form-data
// @Produce      json
// @Security     Bearer
// @Param        file formData file true "封面图片（jpg/jpeg/png/webp）"
// @Success      200 {object} response.Response{data=map[string]interface{}} "上传成功，data 包含 cover_url 字段"
// @Failure      400 {object} response.Response "文件参数错误、大小不符合要求或格式不合法"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /video/upload_cover [post]
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
// @Summary      发布视频
// @Description  使用已上传的视频地址与封面地址创建视频记录，可同时提交标签列表
// @Tags         视频模块
// @Accept       json
// @Produce      json
// @Security     Bearer
// @Param        request body dto.PublishVideoRequest true "发布视频信息"
// @Success      200 {object} response.Response{data=dto.PublishVideoResponse} "发布成功"
// @Failure      400 {object} response.Response "请求参数错误"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /video/publish [post]
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
// @Summary      获取视频详情
// @Description  根据视频 ID 获取详情；软鉴权接口，可选携带 Authorization 头，携带时返回当前用户的真实点赞状态，游客 is_liked 恒为 false
// @Tags         视频模块
// @Produce      json
// @Param        id path int true "视频 ID"
// @Success      200 {object} response.Response{data=dto.VideoDetailResponse} "获取成功"
// @Failure      400 {object} response.Response "视频ID不合法或视频不存在"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /video/{id} [get]
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
// @Summary      获取指定作者的视频列表
// @Description  按作者 ID 分页获取视频列表，使用 (cursor.last_created_at, cursor.last_vid) 复合游标翻页；软鉴权接口，可选携带 Authorization 头以返回点赞状态
// @Tags         视频模块
// @Produce      json
// @Param        author_id             query int    true  "作者用户 ID"
// @Param        page_size             query int    false "每页数量"
// @Param        cursor.last_vid       query string false "上一页最后一条视频 ID（游标）"
// @Param        cursor.last_created_at query string false "上一页最后一条视频创建时间（RFC3339，游标）"
// @Success      200 {object} response.Response{data=dto.VideoListResponse} "获取成功"
// @Failure      400 {object} response.Response "请求参数错误"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /video/list [get]
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
