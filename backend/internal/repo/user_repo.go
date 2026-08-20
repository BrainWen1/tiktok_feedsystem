package repo

// 用于用户相关的数据库操作

import (
	"context"
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
func (r *UserRepo) Register(ctx context.Context, user *model.User) error {
	err := r.db.WithContext(ctx).Create(user).Error
	if err != nil {
		log.Printf("Error registering user in UserRepo: %v", err)
		return err
	}
	return nil
}

// FindByUsername 根据用户名查找用户
func (r *UserRepo) FindByUsername(ctx context.Context, username string) (*model.User, error) {
	var user model.User
	if err := r.db.WithContext(ctx).Where(&model.User{UserName: username}).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// FindByID 根据ID查找用户
func (r *UserRepo) FindByID(ctx context.Context, id uint) (*model.User, error) {
	var user model.User
	if err := r.db.WithContext(ctx).First(&user, id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// CreateRefreshToken 创建刷新令牌
func (r *UserRepo) CreateRefreshToken(ctx context.Context, rt *model.UserRefreshToken) error {
	return r.db.WithContext(ctx).Create(rt).Error
}

// GetRefreshToken 根据token字符串获取刷新令牌
func (r *UserRepo) GetRefreshToken(ctx context.Context, tokenStr string) (*model.UserRefreshToken, error) {
	var rt model.UserRefreshToken
	// 避免硬编码表字段，使用结构体字段名，确保与模型一致
	err := r.db.WithContext(ctx).Where(&model.UserRefreshToken{TokenHash: tokenStr}).
		Where("expires_at > now()").
		First(&rt).Error
	// err := r.db.Where("token_hash = ? AND expires_at > now()", tokenStr).First(&rt).Error // 查询未过期的刷新令牌
	if err != nil {
		return nil, err
	}
	return &rt, nil
}

// DeleteRefreshToken 删除刷新令牌
func (r *UserRepo) DeleteRefreshToken(ctx context.Context, tokenStr string) error {
	err := r.db.WithContext(ctx).Where(&model.UserRefreshToken{TokenHash: tokenStr}).Delete(&model.UserRefreshToken{}).Error
	if err != nil {
		log.Printf("Error deleting refresh token in UserRepo: %v", err)
		return err
	}
	return nil
}

// CleanExpiredRefreshToken 清理过期的刷新令牌，应设置为定时任务
func (r *UserRepo) CleanExpiredRefreshToken(ctx context.Context, userId uint) error {
	err := r.db.WithContext(ctx).Where(&model.UserRefreshToken{UserID: userId}).Where("expires_at <= now()").Delete(&model.UserRefreshToken{}).Error
	if err != nil {
		log.Printf("Error cleaning expired refresh tokens in UserRepo: %v", err)
		return err
	}
	return nil
}
