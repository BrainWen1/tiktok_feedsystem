package handler

import (
	"feedsystem/internal/dto"
	"feedsystem/internal/handler/middleware"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/logger"
	"feedsystem/internal/utils/response"

	"github.com/gin-gonic/gin"
)

type SocialHandler struct {
	SocialService *service.SocialService
}

func NewSocialHandler(socialService *service.SocialService) *SocialHandler {
	return &SocialHandler{SocialService: socialService}
}

// Follow 关注博主
// @Summary      关注博主
// @Description  当前登录用户关注指定博主，重复关注按幂等处理
// @Tags         社交模块
// @Accept       json
// @Produce      json
// @Security     Bearer
// @Param        request body dto.FollowRequest true "关注信息"
// @Success      200 {object} response.Response{data=string} "关注成功"
// @Failure      400 {object} response.Response "请求参数错误或关注失败"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Router       /social/follow [post]
func (h *SocialHandler) Follow(c *gin.Context) {
	// 解析请求参数
	var req dto.FollowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("bind social request failed", logger.Err(err))
		response.FailResponse(c, "请求参数错误")
		return
	}

	// 获取 userID 和 userName
	userID, userName, err := getUserFromCtx(c)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get user from context failed", logger.Err(err))
		response.FailAuthResponse(c, "未登录或登录已过期")
		return
	}

	// 调用服务层的Follow方法
	if err := h.SocialService.Follow(c.Request.Context(), userID, req.BloggerID, userName); err != nil {
		logger.Ctx(c.Request.Context()).Error("follow blogger failed", logger.Uint("user_id", userID), logger.Uint("blogger_id", req.BloggerID), logger.Err(err))
		response.FailResponse(c, "关注失败")
		return
	}

	response.SuccessResponse(c, "Followed successfully")
}

// Unfollow 取消关注博主
// @Summary      取消关注博主
// @Description  当前登录用户取消关注指定博主，未关注时按幂等处理
// @Tags         社交模块
// @Accept       json
// @Produce      json
// @Security     Bearer
// @Param        request body dto.UnfollowRequest true "取消关注信息"
// @Success      200 {object} response.Response{data=string} "取消关注成功"
// @Failure      400 {object} response.Response "请求参数错误或取消关注失败"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Router       /social/unfollow [post]
func (h *SocialHandler) Unfollow(c *gin.Context) {
	// 解析请求参数
	var req dto.UnfollowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("bind social request failed", logger.Err(err))
		response.FailResponse(c, "请求参数错误")
		return
	}

	// 获取 userID
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	// 调用服务层的Unfollow方法
	if err := h.SocialService.Unfollow(c.Request.Context(), userID, req.BloggerID); err != nil {
		logger.Ctx(c.Request.Context()).Error("unfollow blogger failed", logger.Uint("user_id", userID), logger.Uint("blogger_id", req.BloggerID), logger.Err(err))
		response.FailResponse(c, "取消关注失败")
		return
	}

	response.SuccessResponse(c, "Unfollowed successfully")
}

// GetBloggers 获取用户的关注列表
// @Summary      获取用户关注列表
// @Description  分页获取指定用户关注的博主列表，使用 (cursor.last_uid, cursor.last_created_at) 复合游标翻页；软鉴权接口，可选携带 Authorization 头以返回关注关系
// @Tags         社交模块
// @Produce      json
// @Param        uid                    query int    true  "目标用户 ID"
// @Param        page_size              query int    true  "每页数量，范围 10-50"
// @Param        cursor.last_uid        query string false "上一页最后一个用户 ID（游标）"
// @Param        cursor.last_created_at query string false "上一页最后一条关注记录创建时间（RFC3339，游标）"
// @Success      200 {object} response.Response{data=map[string]interface{}} "获取成功，data 包含 bloggers（用户列表）、total、next_cursor（有下一页时返回）"
// @Failure      400 {object} response.Response "请求参数错误或获取关注列表失败"
// @Router       /social/bloggers [get]
func (h *SocialHandler) GetBloggers(c *gin.Context) {
	// 获取查询参数
	getBloggersReq, ok := bindQuery[dto.GetBloggersRequest](c, "bind social query parameters failed")
	if !ok {
		return
	}

	// 软鉴权
	loginUserID := middleware.TryGetUserID(c)

	cursor := dto.FeedCursor{
		LastUserID:    getBloggersReq.CursorLastUserID,
		LastCreatedAt: getBloggersReq.CursorLastCreatedAt,
	}

	// 调用服务层的GetBloggers方法
	bloggers, total, nextCursor, err := h.SocialService.GetBloggers(c.Request.Context(), getBloggersReq.UserID, loginUserID, cursor, getBloggersReq.PageSize)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get bloggers failed", logger.Uint("target_user_id", getBloggersReq.UserID), logger.Err(err))
		response.FailResponse(c, "获取关注列表失败")
		return
	}

	response.SuccessResponse(c, buildSocialListPayload("bloggers", bloggers, total, nextCursor))
}

// GetFollowers 获取用户的粉丝列表
// @Summary      获取用户粉丝列表
// @Description  分页获取指定用户的粉丝列表，使用 (cursor.last_uid, cursor.last_created_at) 复合游标翻页；软鉴权接口，可选携带 Authorization 头以返回关注关系
// @Tags         社交模块
// @Produce      json
// @Param        uid                    query int    true  "目标用户 ID"
// @Param        page_size              query int    true  "每页数量，范围 10-50"
// @Param        cursor.last_uid        query string false "上一页最后一个用户 ID（游标）"
// @Param        cursor.last_created_at query string false "上一页最后一条关注记录创建时间（RFC3339，游标）"
// @Success      200 {object} response.Response{data=map[string]interface{}} "获取成功，data 包含 followers（用户列表）、total、next_cursor（有下一页时返回）"
// @Failure      400 {object} response.Response "请求参数错误或获取粉丝列表失败"
// @Router       /social/followers [get]
func (h *SocialHandler) GetFollowers(c *gin.Context) {
	// 获取查询参数
	getFollowersReq, ok := bindQuery[dto.GetFollowersRequest](c, "bind social query parameters failed")
	if !ok {
		return
	}

	// 软鉴权
	loginUserID := middleware.TryGetUserID(c)

	cursor := dto.FeedCursor{
		LastUserID:    getFollowersReq.CursorLastUserID,
		LastCreatedAt: getFollowersReq.CursorLastCreatedAt,
	}

	// 调用服务层的GetFollowers方法
	followers, total, nextCursor, err := h.SocialService.GetFollowers(c.Request.Context(), getFollowersReq.UserID, loginUserID, cursor, getFollowersReq.PageSize)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get followers failed", logger.Uint("target_user_id", getFollowersReq.UserID), logger.Err(err))
		response.FailResponse(c, "获取粉丝列表失败")
		return
	}

	response.SuccessResponse(c, buildSocialListPayload("followers", followers, total, nextCursor))
}

// IsFollow 获取两者关注关系
// @Summary      查询关注关系
// @Description  查询当前登录用户与目标用户的关注关系，data 包含 is_follow（我是否关注对方）与 is_fan（对方是否是我的粉丝）
// @Tags         社交模块
// @Produce      json
// @Security     Bearer
// @Param        target_id query int true "目标用户 ID"
// @Success      200 {object} response.Response{data=map[string]interface{}} "查询成功，data 包含 is_follow、is_fan 字段"
// @Failure      400 {object} response.Response "请求参数错误或获取关注关系失败"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Router       /social/is_follow [get]
func (h *SocialHandler) IsFollow(c *gin.Context) {
	// 获取查询参数
	req, ok := bindQuery[dto.IsFollowRequest](c, "bind social query parameters failed")
	if !ok {
		return
	}

	// 获取 userID
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	// 调用服务层的IsFollow方法
	isFollow, isFan, err := h.SocialService.IsFollow(c.Request.Context(), userID, uint(req.TargetID))
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("check follow status failed", logger.Uint("user_id", userID), logger.Uint("target_user_id", req.TargetID), logger.Err(err))
		response.FailResponse(c, "获取关注关系失败")
		return
	}

	response.SuccessResponse(c, gin.H{
		"is_follow": isFollow,
		"is_fan":    isFan,
	})
}

// GetCounts 获取关注和粉丝数量
// @Summary      获取关注与粉丝数量
// @Description  获取指定用户的关注数与粉丝数，data 包含 follow_count（关注数）与 fan_count（粉丝数）
// @Tags         社交模块
// @Produce      json
// @Param        target_id query int true "目标用户 ID"
// @Success      200 {object} response.Response{data=map[string]interface{}} "获取成功，data 包含 follow_count、fan_count 字段"
// @Failure      400 {object} response.Response "请求参数错误或获取关注数量失败"
// @Router       /social/count [get]
func (h *SocialHandler) GetCounts(c *gin.Context) {
	// 获取查询参数
	req, ok := bindQuery[dto.GetCountsRequest](c, "bind social query parameters failed")
	if !ok {
		return
	}

	// 调用服务层的GetCounts方法
	followCount, fanCount, err := h.SocialService.GetCounts(c.Request.Context(), req.TargetID)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get social counts failed", logger.Uint("target_user_id", req.TargetID), logger.Err(err))
		response.FailResponse(c, "获取关注数量失败")
		return
	}

	response.SuccessResponse(c, gin.H{
		"follow_count": followCount,
		"fan_count":    fanCount,
	})
}
