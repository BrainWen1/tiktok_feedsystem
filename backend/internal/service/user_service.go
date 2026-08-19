package service

import (
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"fmt"
	"log"

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
func (s *UserService) Register(username, password string) (*model.User, error) {
	// 查找用户是否存在
	existingUser, err := s.UserRepo.FindByUsername(username)
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
		Username: username,
		Password: string(hashedPassword),
	}
	err = s.UserRepo.Register(user)
	if err != nil {
		log.Printf("Error registering user in UserService: %v", err)
		return nil, err
	}
	return user, nil
}
