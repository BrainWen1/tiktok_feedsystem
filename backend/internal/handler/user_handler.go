package handler

import (
	"errors"
	"feedsystem/internal/dto"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/jwt"
	"feedsystem/internal/utils/response"
	"log"

	"github.com/gin-gonic/gin"
)

// UserHandler 用户处理器
type UserHandler struct {
	UserService *service.UserService
}

// NewUserHandler 创建用户处理器
func NewUserHandler(userService *service.UserService) *UserHandler {
	return &UserHandler{
		UserService: userService,
	}
}

// Register 注册用户
func (h *UserHandler) Register(ctx *gin.Context) {
	// 解析请求参数
	var req dto.RegisterRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.FailResponse(ctx, "Invalid request parameters")
		return
	}

	// 调用服务层注册用户
	user, err := h.UserService.Register(ctx.Request.Context(), req.Username, req.Password)
	if err != nil {
		response.FailResponse(ctx, err.Error())
		log.Printf("Error registering user in UserHandler: %v", err)
		return
	}

	// 返回成功响应
	response.SuccessResponse(ctx, gin.H{
		"status":   "Register successfully",
		"user_id":  user.ID,
		"username": user.UserName,
	})
}

// Login 用户登录
func (h *UserHandler) Login(ctx *gin.Context) {
	// 解析请求参数
	var req dto.LoginRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.FailResponse(ctx, "Invalid request parameters")
		return
	}

	// 调用服务层进行登录
	accessToken, refreshToken, err := h.UserService.Login(ctx.Request.Context(), req.Username, req.Password)
	if err != nil {
		response.FailResponse(ctx, err.Error())
		log.Printf("Error logging in user in UserHandler: %v", err)
		return
	}

	// 返回成功响应，将 accessToken 和 refreshToken 打包发回前端
	response.SuccessResponse(ctx, dto.LoginResponse{
		Token:        accessToken,
		RefreshToken: refreshToken,
		Username:     req.Username,
	})
}

// RefreshToken 刷新令牌
func (h *UserHandler) RefreshToken(ctx *gin.Context) {
	// 解析请求参数
	var req dto.RefreshRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.FailResponse(ctx, "Invalid request parameters")
		return
	}

	// 调用服务层刷新令牌
	newAccessToken, newRefreshToken, uid, uname, err := h.UserService.RefreshToken(ctx.Request.Context(), req.RefreshToken, req.OldAccessToken)
	if err != nil {
		response.FailResponse(ctx, err.Error())
		log.Printf("Error refreshing token in UserHandler: %v", err)
		return
	}

	// 返回成功响应，将新的 accessToken 和 refreshToken 打包发回前端
	response.SuccessResponse(ctx, gin.H{
		"access_token":  newAccessToken,
		"refresh_token": newRefreshToken,
		"user_id":       uid,
		"username":      uname,
	})
}

// 工具函数：getUserFromCtx 从context中获取 userID 和 username
func getUserFromCtx(c *gin.Context) (uint, string, error) {
	// 获取 userID
	userID, exists := c.Get("userID")
	if !exists { // 如果不存在 userID，返回错误
		return 0, "", errors.New("userID not found in context")
	}

	uid, ok := userID.(uint) // 如果类型断言失败，返回错误
	if !ok {
		return 0, "", errors.New("userID in context is not of type uint")
	}

	// 获取 username
	username, exists := c.Get("username")
	if !exists {
		return 0, "", errors.New("username not found in context")
	}

	uname, ok := username.(string)
	if !ok {
		return 0, "", errors.New("username in context is not of type string")
	}

	return uid, uname, nil
}

// Logout 用户登出
func (h *UserHandler) Logout(ctx *gin.Context) {
	// 解析请求参数
	var req dto.LogoutRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		log.Printf("Error parsing logout request in UserHandler: %v", err)
		response.FailResponse(ctx, "Invalid request parameters")
		return
	}

	// 从上下文中获取用户ID
	userID, username, err := getUserFromCtx(ctx)
	if err != nil {
		log.Printf("Error getting user from context in UserHandler: %v", err)
		response.FailResponse(ctx, "Failed to get user ID from context")
		return
	}

	// 获取原始 access token
	accessToken, err := jwt.ExtractBearerToken(ctx)
	if err != nil {
		log.Printf("Error extracting access token in UserHandler: %v", err)
		response.FailResponse(ctx, "Failed to extract access token")
		return
	}
	// 获取 access token 剩余有效期
	remainingExpire, err := jwt.GetTokenRemainingExpire(accessToken)
	if err != nil {
		log.Printf("Error getting token remaining expire in UserHandler: %v", err)
		response.FailResponse(ctx, "Failed to get token remaining expire")
		return
	}

	// 调用服务层进行登出
	err = h.UserService.Logout(ctx.Request.Context(), userID, req.RefreshToken, accessToken, remainingExpire)
	if err != nil {
		response.FailResponse(ctx, "Failed to logout user")
		log.Printf("Error logging out user in UserHandler: %v", err)
		return
	}

	// 返回成功响应
	response.SuccessResponse(ctx, gin.H{
		"status":    "Logout successfully",
		"user_id":   userID,
		"user_name": username,
	})
}
