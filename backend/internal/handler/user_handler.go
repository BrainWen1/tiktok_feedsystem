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

// Register 用户注册
// @Summary      用户注册
// @Description  使用用户名和密码注册新用户；data 包含 status、user_id、username 字段
// @Tags         用户模块
// @Accept       json
// @Produce      json
// @Param        request body dto.RegisterRequest true "注册信息"
// @Success      200 {object} response.Response{data=map[string]interface{}} "注册成功"
// @Failure      400 {object} response.Response "参数错误或用户名已存在"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /user/register [post]
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
// @Summary      用户登录
// @Description  使用用户名和密码登录，成功后返回 access token 与 refresh token
// @Tags         用户模块
// @Accept       json
// @Produce      json
// @Param        request body dto.LoginRequest true "登录信息"
// @Success      200 {object} response.Response{data=dto.LoginResponse} "登录成功"
// @Failure      400 {object} response.Response "参数错误"
// @Failure      401 {object} response.Response "用户名或密码错误"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /user/login [post]
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
// @Summary      刷新令牌
// @Description  使用 refresh token 换取新的 access token 和 refresh token；data 包含 access_token、refresh_token、user_id、username 字段
// @Tags         用户模块
// @Accept       json
// @Produce      json
// @Param        request body dto.RefreshRequest true "刷新令牌信息"
// @Success      200 {object} response.Response{data=map[string]interface{}} "刷新成功"
// @Failure      400 {object} response.Response "参数错误"
// @Failure      401 {object} response.Response "refresh token 无效或已过期"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /user/refresh [post]
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
// @Summary      用户登出
// @Description  注销当前登录状态，并使 refresh token 与当前 access token 失效；data 包含 status、user_id、user_name 字段
// @Tags         用户模块
// @Accept       json
// @Produce      json
// @Security     Bearer
// @Param        request body dto.LogoutRequest true "登出信息"
// @Success      200 {object} response.Response{data=map[string]interface{}} "登出成功"
// @Failure      400 {object} response.Response "参数错误"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /user/logout [post]
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

// GetProfile 获取当前用户资料
// @Summary      获取当前用户资料
// @Description  根据 JWT 中的用户身份返回当前登录用户的资料
// @Tags         用户模块
// @Accept       json
// @Produce      json
// @Security     Bearer
// @Success      200 {object} response.Response{data=dto.ProfileResponse} "获取成功"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /user/profile [get]
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

// UpdateProfile 更新用户资料
// @Summary      更新用户资料
// @Description  更新当前登录用户的昵称、头像地址或简介；请求字段均可选，仅更新传入的字段
// @Tags         用户模块
// @Accept       json
// @Produce      json
// @Security     Bearer
// @Param        request body dto.UpdateProfileRequest true "待更新的资料"
// @Success      200 {object} response.Response{data=dto.ProfileResponse} "更新成功"
// @Failure      400 {object} response.Response "参数错误或用户名已存在"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /user/profile [patch]
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
// @Summary      上传用户头像
// @Description  以 multipart/form-data 上传头像文件，仅支持 jpg/jpeg/png/webp；data 包含 avatar_url 字段
// @Tags         用户模块
// @Accept       multipart/form-data
// @Produce      json
// @Security     Bearer
// @Param        file formData file true "头像文件（jpg/jpeg/png/webp）"
// @Success      200 {object} response.Response{data=map[string]interface{}} "上传成功"
// @Failure      400 {object} response.Response "文件参数错误、大小不符合要求或格式不支持"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /user/upload_avatar [post]
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
// @Summary      修改用户密码
// @Description  校验旧密码后修改为新密码，并使当前 access token 失效；data 包含 status 字段
// @Tags         用户模块
// @Accept       json
// @Produce      json
// @Security     Bearer
// @Param        request body dto.ChangePasswordRequest true "修改密码信息"
// @Success      200 {object} response.Response{data=map[string]interface{}} "修改成功"
// @Failure      400 {object} response.Response "参数错误、旧密码错误或新密码不符合要求"
// @Failure      401 {object} response.Response "未登录或登录已过期"
// @Failure      500 {object} response.Response "服务器内部错误"
// @Router       /user/change_password [post]
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
// @Summary      根据ID查找用户
// @Description  通过用户 ID 查询用户公开资料
// @Tags         用户模块
// @Accept       json
// @Produce      json
// @Param        request body dto.FindByIDRequest true "用户ID"
// @Success      200 {object} response.Response{data=dto.ProfileResponse} "查询成功"
// @Failure      400 {object} response.Response "参数错误或用户不存在"
// @Router       /user/find_by_id [post]
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
// @Summary      根据用户名查找用户
// @Description  通过用户名查询用户公开资料
// @Tags         用户模块
// @Accept       json
// @Produce      json
// @Param        request body dto.FindByUsernameRequest true "用户名"
// @Success      200 {object} response.Response{data=dto.ProfileResponse} "查询成功"
// @Failure      400 {object} response.Response "参数错误或用户不存在"
// @Router       /user/find_by_username [post]
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
