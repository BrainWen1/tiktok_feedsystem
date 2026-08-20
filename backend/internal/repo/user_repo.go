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

// FindByID 根据ID查找用户
func (r *UserRepo) FindByID(id uint) (*model.User, error) {
	var user model.User
	if err := r.db.First(&user, id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// CreateRefreshToken 创建刷新令牌
func (r *UserRepo) CreateRefreshToken(rt *model.UserRefreshToken) error {
	return r.db.Create(rt).Error
}

// GetRefreshToken 根据token字符串获取刷新令牌
func (r *UserRepo) GetRefreshToken(tokenStr string) (*model.UserRefreshToken, error) {
	var rt model.UserRefreshToken
	err := r.db.Where("token_hash = ? AND expires_at > now()", tokenStr).First(&rt).Error // 查询未过期的刷新令牌
	if err != nil {
		return nil, err
	}
	return &rt, nil
}

// DeleteRefreshToken 删除刷新令牌
func (r *UserRepo) DeleteRefreshToken(tokenStr string) error {
	return r.db.Where("token_hash = ?", tokenStr).Delete(&model.UserRefreshToken{}).Error
}

// CleanExpiredRefreshToken 清理过期的刷新令牌，应设置为定时任务
func (r *UserRepo) CleanExpiredRefreshToken(userId uint) error {
	return r.db.Where("user_id=? AND expires_at < now()", userId).Delete(&model.UserRefreshToken{}).Error
}
