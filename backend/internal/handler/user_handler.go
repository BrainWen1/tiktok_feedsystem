package handler

import (
	"errors"
	"feedsystem/internal/dto"
	"feedsystem/internal/service"
	"feedsystem/internal/utils/jwt"
	"feedsystem/internal/utils/logger"
	"feedsystem/internal/utils/response"

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
func (h *UserHandler) Register(c *gin.Context) {
	// 解析请求参数
	var req dto.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("bind register request failed", logger.Err(err))
		response.FailResponse(c, "Invalid request parameters")
		return
	}

	// 判断用户名和密码是否为空
	if req.Username == "" || req.Password == "" {
		// 屏蔽敏感信息，防止日志泄露
		logger.Ctx(c.Request.Context()).Warn("username or password is empty", logger.String("username", req.Username))
		response.FailResponse(c, "用户名和密码不能为空")
		return
	}

	// 调用服务层注册用户
	user, err := h.UserService.Register(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		// 原始错误仅写服务端日志，前端只返回统一业务码与友好提示
		logger.Ctx(c.Request.Context()).Error("register user failed", logger.String("username", req.Username), logger.Err(err))
		switch {
		case errors.Is(err, service.ErrUsernameInvalid):
			response.FailResponse(c, "用户名长度需为3-32位，且只能包含字母、数字和下划线")
		case errors.Is(err, service.ErrUsernameExists):
			response.FailResponse(c, "用户名已存在")
		case errors.Is(err, service.ErrPasswordTooShort):
			response.FailResponse(c, "密码长度不能少于8位")
		case errors.Is(err, service.ErrPasswordTooLong):
			response.FailResponse(c, "密码不能超过72字节")
		case errors.Is(err, service.ErrPasswordWeak):
			response.FailResponse(c, "密码需同时包含字母和数字")
		default:
			response.ServerErrorResponse(c, "注册失败，请稍后重试")
		}
		return
	}

	// 返回成功响应
	response.SuccessResponse(c, gin.H{
		"status":   "Register successfully",
		"user_id":  user.ID,
		"username": user.UserName,
	})
}

// Login 用户登录
func (h *UserHandler) Login(c *gin.Context) {
	// 解析请求参数
	var req dto.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("bind login request failed", logger.Err(err))
		response.FailResponse(c, "Invalid request parameters")
		return
	}

	// 调用服务层进行登录
	accessToken, refreshToken, userID, userName, err := h.UserService.Login(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("login user failed", logger.String("username", req.Username), logger.Err(err))
		// 账号不存在与密码错误返回完全一致的提示，防止账号枚举
		if errors.Is(err, service.ErrInvalidCredentials) {
			response.FailAuthResponse(c, "用户名或密码错误")
			return
		}
		response.ServerErrorResponse(c, "登录失败，请稍后重试")
		return
	}

	// 返回成功响应，将 accessToken 和 refreshToken 打包发回前端
	response.SuccessResponse(c, dto.LoginResponse{
		UserID:       userID,
		Token:        accessToken,
		RefreshToken: refreshToken,
		Username:     userName,
	})
}

// RefreshToken 刷新令牌
func (h *UserHandler) RefreshToken(c *gin.Context) {
	// 解析请求参数
	var req dto.RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("bind refresh request failed", logger.Err(err))
		response.FailResponse(c, "Invalid request parameters")
		return
	}

	// 调用服务层刷新令牌
	newAccessToken, newRefreshToken, userID, userName, err := h.UserService.RefreshToken(c.Request.Context(), req.RefreshToken, req.OldAccessToken)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("refresh token failed", logger.Err(err))
		// refresh token无效属于鉴权失败返回401，服务不可用返回500
		if errors.Is(err, service.ErrInvalidRefreshToken) {
			response.FailAuthResponse(c, "refresh token无效或已过期")
			return
		}
		if errors.Is(err, service.ErrRefreshTokenUnavailable) {
			response.ServerErrorResponse(c, "刷新令牌服务暂不可用")
			return
		}
		response.ServerErrorResponse(c, "刷新令牌失败")
		return
	}

	// 返回成功响应，将新的 accessToken 和 refreshToken 打包发回前端
	response.SuccessResponse(c, gin.H{
		"access_token":  newAccessToken,
		"refresh_token": newRefreshToken,
		"user_id":       userID,
		"username":      userName,
	})
}

// 工具函数：getUserFromCtx 从context中获取 userID 和 username
func getUserFromCtx(c *gin.Context) (uint, string, error) {
	// 获取 userID
	userID, exists := c.Get("userID")
	if !exists { // 如果不存在 userID，返回错误
		return 0, "", errors.New("userID not found in context")
	}

	userIDUint, ok := userID.(uint) // 如果类型断言失败，返回错误
	if !ok {
		return 0, "", errors.New("userID in context is not of type uint")
	}

	// 获取 username
	username, exists := c.Get("username")
	if !exists {
		return 0, "", errors.New("username not found in context")
	}

	userName, ok := username.(string)
	if !ok {
		return 0, "", errors.New("username in context is not of type string")
	}

	return userIDUint, userName, nil
}

// Logout 用户登出
func (h *UserHandler) Logout(c *gin.Context) {
	// 解析请求参数
	var req dto.LogoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("parse logout request failed", logger.Err(err))
		response.FailResponse(c, "Invalid request parameters")
		return
	}

	// 从上下文中获取用户ID
	userID, username, err := getUserFromCtx(c)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get user from context failed", logger.Err(err))
		response.FailAuthResponse(c, "未登录或登录已过期")
		return
	}

	// 获取原始 access token
	accessToken, err := jwt.ExtractBearerToken(c)
	if err != nil {
		logger.Ctx(c.Request.Context()).Warn("extract access token failed", logger.Err(err))
		response.FailAuthResponse(c, "未登录或登录已过期")
		return
	}
	// 获取 access token 剩余有效期
	remainingExpire, err := jwt.GetTokenRemainingExpire(accessToken)
	if err != nil {
		logger.Ctx(c.Request.Context()).Warn("get token remaining expire failed", logger.Err(err))
		response.FailAuthResponse(c, "无效的token")
		return
	}

	// 调用服务层进行登出
	err = h.UserService.Logout(c.Request.Context(), userID, req.RefreshToken, accessToken, remainingExpire)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("logout user failed", logger.Uint("user_id", userID), logger.Err(err))
		if errors.Is(err, service.ErrInvalidRefreshToken) {
			response.FailAuthResponse(c, "refresh token无效或已过期")
			return
		}
		response.ServerErrorResponse(c, "登出失败，请稍后重试")
		return
	}

	// 返回成功响应
	response.SuccessResponse(c, gin.H{
		"status":    "Logout successfully",
		"user_id":   userID,
		"user_name": username,
	})
}

func (h *UserHandler) GetProfile(c *gin.Context) {
	// 从上下文中获取用户ID和用户名
	userID, _, err := getUserFromCtx(c)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get user from context failed", logger.Err(err))
		response.FailAuthResponse(c, "未登录或登录已过期")
		return
	}

	// 调用服务层获取用户资料
	profile, err := h.UserService.GetProfile(c.Request.Context(), userID)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get user profile failed", logger.Uint("user_id", userID), logger.Err(err))
		response.ServerErrorResponse(c, "获取用户资料失败，请稍后重试")
		return
	}

	// 返回成功响应，包含用户资料
	response.SuccessResponse(c, profile)
}

func (h *UserHandler) UpdateProfile(c *gin.Context) {
	// 从上下文中获取用户ID和用户名
	userID, _, err := getUserFromCtx(c)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get user from context failed", logger.Err(err))
		response.FailAuthResponse(c, "未登录或登录已过期")
		return
	}

	// 解析请求参数
	var req dto.UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("parse update profile request failed", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(c, "Invalid request parameters")
		return
	}

	// 调用服务层更新用户资料
	updatedUser, err := h.UserService.UpdateProfile(c.Request.Context(), userID, req)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("update user profile failed", logger.Uint("user_id", userID), logger.Err(err))
		switch {
		case errors.Is(err, service.ErrUsernameInvalid):
			response.FailResponse(c, "用户名长度需为3-32位，且只能包含字母、数字和下划线")
		case errors.Is(err, service.ErrUsernameExists):
			response.FailResponse(c, "用户名已存在")
		case errors.Is(err, service.ErrInvalidAvatarURL):
			response.FailResponse(c, "头像地址不合法")
		case errors.Is(err, service.ErrNoFieldsToUpdate):
			response.FailResponse(c, "没有需要更新的字段")
		default:
			response.ServerErrorResponse(c, "更新资料失败，请稍后重试")
		}
		return
	}

	// 返回成功响应
	response.SuccessResponse(c, updatedUser)
}

// UploadAvatar 上传用户头像
func (h *UserHandler) UploadAvatar(c *gin.Context) {
	// 从上下文中获取用户ID
	userID, _, err := getUserFromCtx(c)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get user from context failed", logger.Err(err))
		response.FailAuthResponse(c, "未登录或登录已过期")
		return
	}

	// 解析上传的文件
	fh, err := c.FormFile("file") // 获取表单中key为"file"的文件
	if err != nil {
		logger.Ctx(c.Request.Context()).Warn("get uploaded file failed", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(c, "文件上传参数错误")
		return
	}

	// 调用服务层处理头像上传
	avatarURL, err := h.UserService.UploadAvatar(c.Request.Context(), userID, fh)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("upload avatar failed", logger.Uint("user_id", userID), logger.Err(err))
		switch {
		case errors.Is(err, service.ErrInvalidFileSize):
			response.FailResponse(c, "文件大小不符合要求")
		case errors.Is(err, service.ErrInvalidFileType):
			response.FailResponse(c, "仅支持 jpg/jpeg/png/webp 格式的图片")
		default:
			response.ServerErrorResponse(c, "头像上传失败，请稍后重试")
		}
		return
	}

	// 返回成功响应，直接返回相对路径URL
	response.SuccessResponse(c, gin.H{
		"avatar_url": avatarURL,
	})
}

// ChangePassword 修改用户密码
func (h *UserHandler) ChangePassword(c *gin.Context) {
	// 从上下文中获取用户ID
	userID, _, err := getUserFromCtx(c)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("get user from context failed", logger.Err(err))
		response.FailAuthResponse(c, "未登录或登录已过期")
		return
	}

	// 解析请求参数
	var req dto.ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("parse change password request failed", logger.Uint("user_id", userID), logger.Err(err))
		response.FailResponse(c, "Invalid request parameters")
		return
	}

	// 判断三个字段是否有留空
	if req.OldPassword == "" || req.NewPassword == "" || req.ConfirmPassword == "" {
		// 屏蔽敏感信息，防止日志泄露
		logger.Ctx(c.Request.Context()).Warn("old or new or confirm password is empty", logger.Uint("user_id", userID))
		response.FailResponse(c, "Old password and new password and confirm password cannot be empty")
		return
	}
	// 判断新密码和确认密码是否一致
	if req.NewPassword != req.ConfirmPassword {
		// 屏蔽敏感信息，防止日志泄露
		logger.Ctx(c.Request.Context()).Warn("new password and confirm password do not match", logger.Uint("user_id", userID))
		response.FailResponse(c, "New password and confirm password do not match")
		return
	}
	// 判断新密码是否与旧密码相同
	if req.OldPassword == req.NewPassword {
		// 屏蔽敏感信息，防止日志泄露
		logger.Ctx(c.Request.Context()).Warn("new password is the same as old password", logger.Uint("user_id", userID))
		response.FailResponse(c, "New password cannot be the same as old password")
		return
	}

	// 获取access token用作service层的拉黑操作
	accessToken, err := jwt.ExtractBearerToken(c)
	if err != nil {
		logger.Ctx(c.Request.Context()).Warn("extract access token failed", logger.Err(err))
		response.FailAuthResponse(c, "未登录或登录已过期")
		return
	}
	// 获取access token剩余有效期
	remainingExpire, err := jwt.GetTokenRemainingExpire(accessToken)
	if err != nil {
		logger.Ctx(c.Request.Context()).Warn("get token remaining expire failed", logger.Err(err))
		response.FailAuthResponse(c, "无效的token")
		return
	}

	// 调用服务层修改密码
	err = h.UserService.ChangePassword(c.Request.Context(), userID, req.OldPassword, req.NewPassword, accessToken, remainingExpire)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("change password failed", logger.Uint("user_id", userID), logger.Err(err))
		switch {
		case errors.Is(err, service.ErrOldPasswordIncorrect):
			response.FailResponse(c, "旧密码错误")
		case errors.Is(err, service.ErrPasswordTooShort):
			response.FailResponse(c, "新密码长度不能少于8位")
		case errors.Is(err, service.ErrPasswordTooLong):
			response.FailResponse(c, "新密码不能超过72字节")
		case errors.Is(err, service.ErrPasswordWeak):
			response.FailResponse(c, "新密码需同时包含字母和数字")
		default:
			response.ServerErrorResponse(c, "修改密码失败，请稍后重试")
		}
		return
	}

	// 返回成功响应
	response.SuccessResponse(c, gin.H{
		"status": "Password changed successfully",
	})
}

// FindByID 根据ID查找用户
func (h *UserHandler) FindByID(c *gin.Context) {
	// 解析请求参数
	var req dto.FindByIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("bind find by id request failed", logger.Err(err))
		response.FailResponse(c, "Invalid request parameters")
		return
	}

	// 调用服务层根据ID查找用户
	user, err := h.UserService.FindByID(c.Request.Context(), req.UserID)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("find user by ID failed", logger.Uint("user_id", req.UserID), logger.Err(err))
		response.FailResponse(c, "用户不存在")
		return
	}

	response.SuccessResponse(c, user)
}

// FindByUsername 根据用户名查找用户
func (h *UserHandler) FindByUsername(c *gin.Context) {
	// 解析请求参数
	var req dto.FindByUsernameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Ctx(c.Request.Context()).Warn("bind find by username request failed", logger.Err(err))
		response.FailResponse(c, "Invalid request parameters")
		return
	}

	// 调用服务层根据用户名查找用户
	user, err := h.UserService.FindByUsername(c.Request.Context(), req.Username)
	if err != nil {
		logger.Ctx(c.Request.Context()).Error("find user by username failed", logger.String("username", req.Username), logger.Err(err))
		response.FailResponse(c, "用户不存在")
		return
	}

	response.SuccessResponse(c, user)
}
