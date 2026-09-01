package handler

import (
	"errors"
	"feedsystem/internal/dto"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/jwt"
	"feedsystem/internal/utils/response"
	"fmt"
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

	// 判断用户名和密码是否为空
	if req.Username == "" || req.Password == "" {
		log.Printf("Username or password is empty: username='%s', password='%s'", req.Username, req.Password)
		response.FailResponse(ctx, "Username and password cannot be empty")
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

func (h *UserHandler) GetProfile(ctx *gin.Context) {
	// 从上下文中获取用户ID和用户名
	userID, _, err := getUserFromCtx(ctx)
	if err != nil {
		log.Printf("Error getting user from context in UserHandler: %v", err)
		response.FailResponse(ctx, "Failed to get user ID from context")
		return
	}

	// 调用服务层获取用户资料
	profile, err := h.UserService.GetProfile(ctx.Request.Context(), userID)
	if err != nil {
		log.Printf("Error getting user profile in UserHandler: %v", err)
		response.FailResponse(ctx, "Failed to get user profile")
		return
	}

	// 返回成功响应，包含用户资料
	response.SuccessResponse(ctx, profile)
}

func (h *UserHandler) UpdateProfile(ctx *gin.Context) {
	// 从上下文中获取用户ID和用户名
	userID, _, err := getUserFromCtx(ctx)
	if err != nil {
		log.Printf("Error getting user from context in UserHandler: %v", err)
		response.FailResponse(ctx, "Failed to get user ID from context")
		return
	}

	// 解析请求参数
	var req dto.UpdateProfileRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		log.Printf("Error parsing update profile request in UserHandler: %v", err)
		response.FailResponse(ctx, "Invalid request parameters")
		return
	}

	// 调用服务层更新用户资料
	updatedUser, err := h.UserService.UpdateProfile(ctx.Request.Context(), userID, req)
	if err != nil {
		log.Printf("Error updating user profile in UserHandler: %v", err)
		response.FailResponse(ctx, fmt.Sprintf("Failed to update user profile: %v", err))
		return
	}

	// 返回成功响应
	response.SuccessResponse(ctx, updatedUser)
}

// UploadAvatar 上传用户头像
func (h *UserHandler) UploadAvatar(ctx *gin.Context) {
	// 从上下文中获取用户ID
	userID, _, err := getUserFromCtx(ctx)
	if err != nil {
		log.Printf("Failed to get user from context in UploadAvatar: %v", err)
		response.FailAuthResponse(ctx, "Failed to get user ID from context")
		return
	}

	// 解析上传的文件
	fh, err := ctx.FormFile("file") // 获取表单中key为"file"的文件
	if err != nil {
		log.Printf("Failed to get uploaded file in UploadAvatar: %v", err)
		response.FailResponse(ctx, "Failed to get uploaded file: "+err.Error())
		return
	}

	// 调用服务层处理头像上传
	avatarURL, err := h.UserService.UploadAvatar(ctx.Request.Context(), userID, fh)
	if err != nil {
		log.Printf("Failed to upload avatar in UploadAvatar: %v", err)
		response.FailResponse(ctx, "Failed to upload avatar: "+err.Error())
		return
	}

	// 返回成功响应，直接返回相对路径URL
	response.SuccessResponse(ctx, gin.H{
		"avatar_url": avatarURL,
	})
}

// ChangePassword 修改用户密码
func (h *UserHandler) ChangePassword(ctx *gin.Context) {
	// 从上下文中获取用户ID
	userID, _, err := getUserFromCtx(ctx)
	if err != nil {
		log.Printf("Error getting user from context in ChangePassword: %v", err)
		response.FailResponse(ctx, "Failed to get user ID from context")
		return
	}

	// 解析请求参数
	var req dto.ChangePasswordRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		log.Printf("Error parsing change password request in ChangePassword: %v", err)
		response.FailResponse(ctx, "Invalid request parameters")
		return
	}

	// 判断三个字段是否有留空
	if req.OldPassword == "" || req.NewPassword == "" || req.ConfirmPassword == "" {
		log.Printf("Old password or new password or confirm password is empty: old='%s', new='%s', confirm='%s'", req.OldPassword, req.NewPassword, req.ConfirmPassword)
		response.FailResponse(ctx, "Old password and new password and confirm password cannot be empty")
		return
	}
	// 判断新密码和确认密码是否一致
	if req.NewPassword != req.ConfirmPassword {
		log.Printf("New password and confirm password do not match: new='%s', confirm='%s'", req.NewPassword, req.ConfirmPassword)
		response.FailResponse(ctx, "New password and confirm password do not match")
		return
	}
	// 判断新密码是否与旧密码相同
	if req.OldPassword == req.NewPassword {
		log.Printf("New password is the same as old password: old='%s', new='%s'", req.OldPassword, req.NewPassword)
		response.FailResponse(ctx, "New password cannot be the same as old password")
		return
	}

	// 获取access token用作service层的拉黑操作
	accessToken, err := jwt.ExtractBearerToken(ctx)
	if err != nil {
		log.Printf("Error extracting access token in UserHandler: %v", err)
		response.FailResponse(ctx, "Failed to extract access token")
		return
	}
	// 获取access token剩余有效期
	remainingExpire, err := jwt.GetTokenRemainingExpire(accessToken)
	if err != nil {
		log.Printf("Error getting token remaining expire in UserHandler: %v", err)
		response.FailResponse(ctx, "Failed to get token remaining expire")
		return
	}

	// 调用服务层修改密码
	err = h.UserService.ChangePassword(ctx.Request.Context(), userID, req.OldPassword, req.NewPassword, accessToken, remainingExpire)
	if err != nil {
		log.Printf("Error changing password in ChangePassword: %v", err)
		response.FailResponse(ctx, "Failed to change password: "+err.Error())
		return
	}

	// 返回成功响应
	response.SuccessResponse(ctx, gin.H{
		"status": "Password changed successfully",
	})
}

// FindByID 根据ID查找用户
func (h *UserHandler) FindByID(ctx *gin.Context) {
	// 解析请求参数
	var req dto.FindByIDRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.FailResponse(ctx, "Invalid request parameters")
		return
	}

	// 调用服务层根据ID查找用户
	user, err := h.UserService.FindByID(ctx.Request.Context(), req.UserID)
	if err != nil {
		response.FailResponse(ctx, err.Error())
		log.Printf("Error finding user by ID in UserHandler: %v", err)
		return
	}

	response.SuccessResponse(ctx, user)
}

// FindByUsername 根据用户名查找用户
func (h *UserHandler) FindByUsername(ctx *gin.Context) {
	// 解析请求参数
	var req dto.FindByUsernameRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.FailResponse(ctx, "Invalid request parameters")
		return
	}

	// 调用服务层根据用户名查找用户
	user, err := h.UserService.FindByUsername(ctx.Request.Context(), req.Username)
	if err != nil {
		response.FailResponse(ctx, err.Error())
		log.Printf("Error finding user by username in UserHandler: %v", err)
		return
	}

	response.SuccessResponse(ctx, user)
}
