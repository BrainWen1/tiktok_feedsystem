package repo

// 用于用户相关的数据库操作

import (
	"feedsystem/internal/model"
	"log"

	"gorm.io/gorm"
)

// UserRepo 用户仓库
type UserRepo struct {
	db *gorm.DB
}

// NewUserRepo 创建用户仓库
func NewUserRepo(db *gorm.DB) *UserRepo {
	return &UserRepo{db: db}
}

// Register 注册用户
func (r *UserRepo) Register(user *model.User) error {
	err := r.db.Create(user).Error
	if err != nil {
		log.Printf("Error registering user in UserRepo: %v", err)
		return err
	}
	return nil
}

// FindByUsername 根据用户名查找用户
func (r *UserRepo) FindByUsername(username string) (*model.User, error) {
	var user model.User
	if err := r.db.Where("username = ?", username).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}
