package handler

import (
	"feedsystem/internal/dto"
	"feedsystem/internal/service"
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
	user, err := h.UserService.Register(req.Username, req.Password)
	if err != nil {
		response.FailResponse(ctx, err.Error())
		log.Printf("Error registering user in UserHandler: %v", err)
		return
	}

	// 返回成功响应
	response.SuccessResponse(ctx, gin.H{
		"status":   "Register successfully",
		"user_id":  user.ID,
		"username": user.Username,
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
	accessToken, refreshToken, err := h.UserService.Login(req.Username, req.Password)
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
