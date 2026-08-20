package service

import (
	"context"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"feedsystem/internal/utils/jwt"
	"fmt"
	"log"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// UserService 用户服务
type UserService struct {
	UserRepo *repo.UserRepo
}

// NewUserService 创建用户服务
func NewUserService(userRepo *repo.UserRepo) *UserService {
	return &UserService{
		UserRepo: userRepo,
	}
}

// Register 注册用户
func (s *UserService) Register(ctx context.Context, username, password string) (*model.User, error) {
	// 查找用户是否存在
	existingUser, err := s.UserRepo.FindByUsername(ctx, username)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	if existingUser != nil {
		return nil, fmt.Errorf("username already exists")
	}

	// 加密密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	// 创建用户
	user := &model.User{
		UserName: username,
		Password: string(hashedPassword),
	}
	err = s.UserRepo.Register(ctx, user)
	if err != nil {
		log.Printf("Error registering user in UserService: %v", err)
		return nil, err
	}
	return user, nil
}

// Login 用户登录
func (s *UserService) Login(ctx context.Context, username, password string) (accessToken string, refreshToken string, err error) {
	// 查找用户
	user, err := s.UserRepo.FindByUsername(ctx, username)
	if err != nil {
		return "", "", err
	}

	// 验证密码
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return "", "", fmt.Errorf("invalid password")
	}

	// 生成访问令牌和刷新令牌
	accessToken, err = jwt.GenerateToken(fmt.Sprintf("%d", user.ID), user.UserName)
	if err != nil {
		return "", "", err
	}

	refreshToken, err = jwt.GenerateRefreshToken(user.ID)
	if err != nil {
		return "", "", err
	}

	// 组装刷新令牌模型
	refreshTokenModel := &model.UserRefreshToken{
		UserID:    user.ID,
		TokenHash: refreshToken,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour), // refresh token 有效期为7天
	}

	// 保存刷新令牌到数据库
	err = s.UserRepo.CreateRefreshToken(ctx, refreshTokenModel)
	if err != nil {
		return "", "", err
	}

	return accessToken, refreshToken, nil
}

// RefreshToken 刷新令牌
func (s *UserService) RefreshToken(ctx context.Context, refreshToken string) (newAccessToken string, newRefreshToken string, uid uint, uname string, err error) {
	if refreshToken == "" {
		return "", "", 0, "", fmt.Errorf("refresh token is empty")
	}

	// 查找刷新令牌
	rt, err := s.UserRepo.GetRefreshToken(ctx, refreshToken)
	if err != nil {
		return "", "", 0, "", fmt.Errorf("invalid or expired refresh token")
	}

	// 查找用户
	user, err := s.UserRepo.FindByID(ctx, rt.UserID)
	if err != nil {
		return "", "", 0, "", fmt.Errorf("user not found")
	}

	// 生成新的访问令牌和刷新令牌
	newAccessToken, err = jwt.GenerateToken(fmt.Sprintf("%d", user.ID), user.UserName)
	if err != nil {
		return "", "", 0, "", err
	}

	newRefreshToken, err = jwt.GenerateRefreshToken(user.ID)
	if err != nil {
		return "", "", 0, "", err
	}

	// 删除旧的刷新令牌
	err = s.UserRepo.DeleteRefreshToken(ctx, refreshToken)
	if err != nil {
		return "", "", 0, "", err
	}

	// 保存新的刷新令牌到数据库
	newRefreshTokenSession := &model.UserRefreshToken{
		UserID:    user.ID,
		TokenHash: newRefreshToken,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour), // refresh token 有效期为7天
	}
	err = s.UserRepo.CreateRefreshToken(ctx, newRefreshTokenSession)
	if err != nil {
		return "", "", 0, "", err
	}

	return newAccessToken, newRefreshToken, user.ID, user.UserName, nil
}
