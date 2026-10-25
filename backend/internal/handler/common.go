package handler

import (
	"feedsystem/internal/dto"
	"feedsystem/internal/handler/middleware"
	"feedsystem/internal/utils/logger"
	"feedsystem/internal/utils/response"

	"github.com/gin-gonic/gin"
)

// bindQuery 绑定 query 参数；失败时统一记录日志并返回参数错误响应。
func bindQuery[T any](c *gin.Context, logMsg string) (T, bool) {
	var req T
	if err := c.ShouldBindQuery(&req); err != nil {
		logger.Warn(logMsg, logger.Err(err))
		response.FailResponse(c, "请求参数错误")
		return req, false
	}
	return req, true
}

// bindFeedListQuery 绑定 feed 列表公共查询参数，并取出软鉴权用户ID（游客为0）。
func bindFeedListQuery(c *gin.Context) (dto.FeedListReq, uint, bool) {
	req, ok := bindQuery[dto.FeedListReq](c, "bind feed query failed")
	if !ok {
		return dto.FeedListReq{}, 0, false
	}
	return req, middleware.TryGetUserID(c), true
}

// requireUserID 从上下文读取登录用户ID；缺失或类型错误统一返回401。
func requireUserID(c *gin.Context) (uint, bool) {
	userID, _, err := getUserFromCtx(c)
	if err != nil {
		logger.Error("get user from context failed", logger.Err(err))
		response.FailAuthResponse(c, "未登录或登录已过期")
		return 0, false
	}
	return userID, true
}

// buildSocialListPayload 组装关注/粉丝列表响应体，next_cursor 为空时不返回。
func buildSocialListPayload(field string, list interface{}, total int64, nextCursor *dto.FeedCursor) gin.H {
	payload := gin.H{
		field:   list,
		"total": total,
	}
	if nextCursor != nil {
		payload["next_cursor"] = nextCursor
	}
	return payload
}
