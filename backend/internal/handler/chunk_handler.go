package handler

import (
	"errors"
	"net/http"
	"strconv"

	"feedsystem/internal/model"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/logger"
	"feedsystem/internal/utils/response"

	"github.com/gin-gonic/gin"
)

// maxChunkRequestBytes 单次分片请求上限：标准分片大小 + 1，
// 留 1 字节用于区分“刚好一片”和“超出上限”，实际大小由 service 精确校验。
const maxChunkRequestBytes = model.ChunkSize + 1

type ChunkHandler struct {
	svc *service.ChunkService
}

func NewChunkHandler(svc *service.ChunkService) *ChunkHandler {
	return &ChunkHandler{svc: svc}
}

// CreateSessionReq 创建分片会话请求
type CreateSessionReq struct {
	Filename string `json:"filename" binding:"required"`
	FileSize int64  `json:"file_size" binding:"required,gt=0"`
	FileHash string `json:"file_hash"` // 可选，提供时合并后强制校验
}

// CreateUploadSession 初始化分片上传会话
// @Summary      创建分片上传会话
// @Description  根据文件名与文件大小创建（或恢复）分片上传会话，返回 upload_id、分片大小与总分片数，用于后续分片上传与断点续传
// @Tags         视频模块
// @Accept       json
// @Produce      json
// @Security     Bearer
// @Param        request body CreateSessionReq true "创建会话参数"
// @Success      200 {object} response.Response{data=map[string]interface{}} "创建成功，data 包含 upload_id、chunk_size、total_chunks、expire_at 字段"
// @Failure      400 {object} response.Response "请求参数错误或上传参数不合法"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /video/chunk/create [post]
func (h *ChunkHandler) CreateUploadSession(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	var req CreateSessionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("bind create chunk session request failed", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(c, "请求参数错误")
		return
	}

	sess, err := h.svc.CreateUploadSession(c.Request.Context(), userID, req.Filename, req.FileSize, req.FileHash)
	if err != nil {
		h.fail(c, err, "创建上传会话失败，请稍后重试")
		return
	}

	response.SuccessResponse(c, gin.H{
		"upload_id":    sess.UploadID,
		"chunk_size":   sess.ChunkSize,
		"total_chunks": sess.TotalChunks,
		"expire_at":    sess.ExpireAt,
	})
}

// UploadChunk 上传单个分片。请求体为分片二进制内容，query 携带 upload_id 与 index。
// @Summary      上传单个分片
// @Description  请求体直接为分片二进制内容（application/octet-stream），upload_id 与分片序号通过 query 传入；单个分片大小受服务端限制
// @Tags         视频模块
// @Accept       octet-stream
// @Produce      json
// @Security     Bearer
// @Param        upload_id query string true  "分片上传会话 ID"
// @Param        index     query int    true  "分片序号，从 0 开始"
// @Param        body      body  string true  "分片二进制内容"
// @Success      200 {object} response.Response{data=map[string]interface{}} "上传成功，data 包含 uploaded（已上传分片序号列表）与 received（本次分片序号）"
// @Failure      400 {object} response.Response "分片参数不合法、会话不存在、分片缺失或校验失败"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /video/chunk/upload [post]
func (h *ChunkHandler) UploadChunk(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	uploadID := c.Query("upload_id")
	chunkIndex, err := strconv.Atoi(c.Query("index"))
	if err != nil || uploadID == "" || chunkIndex < 0 {
		response.FailResponse(c, "分片参数不合法")
		return
	}

	// 在读取阶段就限制请求体大小，避免异常客户端把整块内存/磁盘打满。
	// 直接把请求体流式交给 service 落盘，不再 io.ReadAll 到堆内存，单片 5MB 不再整块驻留 RAM。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxChunkRequestBytes)
	sess, err := h.svc.SaveChunk(c.Request.Context(), userID, uploadID, chunkIndex, c.Request.Body)
	if err != nil {
		h.fail(c, err, "分片保存失败，请稍后重试")
		return
	}

	response.SuccessResponse(c, gin.H{
		"uploaded": sess.UploadedChunks(),
		"received": chunkIndex,
	})
}

// Merge 合并全部分片，返回 Nginx 可直接托管的视频访问路径。
// @Summary      合并分片
// @Description  校验并合并全部分片生成成品视频，返回可直接用于发布接口的视频访问路径；分片未全部上传完成时返回 400
// @Tags         视频模块
// @Produce      json
// @Security     Bearer
// @Param        upload_id query string true "分片上传会话 ID"
// @Success      200 {object} response.Response{data=map[string]interface{}} "合并成功，data 包含 video_url 字段"
// @Failure      400 {object} response.Response "upload_id 为空、会话不存在/过期、分片未传完或文件校验失败"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /video/chunk/merge [post]
func (h *ChunkHandler) Merge(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	uploadID := c.Query("upload_id")
	if uploadID == "" {
		response.FailResponse(c, "upload_id 不能为空")
		return
	}

	videoURL, err := h.svc.MergeChunks(c.Request.Context(), userID, uploadID)
	if err != nil {
		h.fail(c, err, "分片合并失败，请稍后重试")
		return
	}

	response.SuccessResponse(c, gin.H{"video_url": videoURL})
}

// GetSessionStatus 查询上传状态，前端据此只补传缺失分片。
// @Summary      查询分片上传状态
// @Description  查询会话已上传的分片列表，前端据此只补传缺失分片，实现断点续传
// @Tags         视频模块
// @Produce      json
// @Security     Bearer
// @Param        upload_id query string true "分片上传会话 ID"
// @Success      200 {object} response.Response{data=map[string]interface{}} "查询成功，data 包含 upload_id、total_chunks、chunk_size、uploaded、expire_at 字段"
// @Failure      400 {object} response.Response "upload_id 为空或会话不存在/过期"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /video/chunk/status [get]
func (h *ChunkHandler) GetSessionStatus(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	uploadID := c.Query("upload_id")
	if uploadID == "" {
		response.FailResponse(c, "upload_id 不能为空")
		return
	}

	sess, err := h.svc.GetSession(c.Request.Context(), userID, uploadID)
	if err != nil {
		h.fail(c, err, "查询上传状态失败，请稍后重试")
		return
	}

	response.SuccessResponse(c, gin.H{
		"upload_id":    sess.UploadID,
		"total_chunks": sess.TotalChunks,
		"chunk_size":   sess.ChunkSize,
		"uploaded":     sess.UploadedChunks(),
		"expire_at":    sess.ExpireAt,
	})
}

// CancelUpload 取消上传，立即清理分片目录与 Redis 会话。
// @Summary      取消分片上传
// @Description  取消上传并立即清理已上传分片目录与 Redis 会话，data 固定为 null
// @Tags         视频模块
// @Produce      json
// @Security     Bearer
// @Param        upload_id query string true "分片上传会话 ID"
// @Success      200 {object} response.Response "取消成功，data 为 null"
// @Failure      400 {object} response.Response "upload_id 为空或会话不存在/过期"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /video/chunk/cancel [post]
func (h *ChunkHandler) CancelUpload(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	uploadID := c.Query("upload_id")
	if uploadID == "" {
		response.FailResponse(c, "upload_id 不能为空")
		return
	}

	if err := h.svc.CancelUpload(c.Request.Context(), userID, uploadID); err != nil {
		h.fail(c, err, "取消上传失败，请稍后重试")
		return
	}

	response.SuccessResponse(c, nil)
}

// fail 将 service 层错误映射为稳定的客户端提示，内部细节只写日志。
func (h *ChunkHandler) fail(c *gin.Context, err error, fallback string) {
	logger.Ctx(c.Request.Context()).Error("chunk upload operation failed", logger.Err(err))
	switch {
	case errors.Is(err, service.ErrSessionNotFound), errors.Is(err, service.ErrUploadForbidden):
		// 不区分“不存在”和“无权访问”，避免泄露他人 upload_id 是否存在
		response.FailResponse(c, "上传会话不存在或已过期")
	case errors.Is(err, service.ErrInvalidUploadRequest),
		errors.Is(err, service.ErrInvalidChunkIndex),
		errors.Is(err, service.ErrInvalidChunkSize):
		response.FailResponse(c, "上传参数不合法")
	case errors.Is(err, service.ErrChunksIncomplete):
		response.FailResponse(c, "还有分片未上传完成")
	case errors.Is(err, service.ErrChunkMissing):
		response.FailResponse(c, "分片文件缺失或损坏，请重新上传对应分片")
	case errors.Is(err, service.ErrFileHashMismatch):
		response.FailResponse(c, "文件校验失败，请重新上传")
	case errors.Is(err, service.ErrSessionBusy):
		response.FailResponse(c, "上传会话处理中，请稍后重试")
	case errors.Is(err, service.ErrStorageUnavailable):
		response.ServerErrorResponse(c, "上传服务暂不可用，请稍后重试")
	default:
		response.ServerErrorResponse(c, fallback)
	}
}
