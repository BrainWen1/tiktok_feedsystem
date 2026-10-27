package repo

// 用于用户相关的数据库操作

import (
	"context"
	"errors"
	"feedsystem/internal/model"
	"fmt"
	"strings"

	"feedsystem/internal/utils/logger"

	"gorm.io/gorm"
)

// ErrInvalidArgument repo 层参数校验失败。
// 用于拦截空用户名、零值主键等会导致 GORM 忽略零值条件、生成无 WHERE SQL 的请求。
var ErrInvalidArgument = errors.New("invalid argument")

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
		logger.Ctx(ctx).Error("register user failed", logger.Err(err))
		return err
	}
	return nil
}

// FindByUsername 根据用户名查找用户。
// 空用户名必须前置拦截：结构体条件会忽略空字符串，生成无 WHERE 的查询并返回第一条用户。
func (r *UserRepo) FindByUsername(ctx context.Context, username string) (*model.User, error) {
	if strings.TrimSpace(username) == "" {
		return nil, fmt.Errorf("find user by username: empty username: %w", ErrInvalidArgument)
	}

	var user model.User
	if err := r.db.WithContext(ctx).Where("user_name = ?", username).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// FindByID 根据ID查找用户
func (r *UserRepo) FindByID(ctx context.Context, userID uint) (*model.User, error) {
	var user model.User
	if err := r.db.WithContext(ctx).First(&user, userID).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// CreateRefreshToken 创建刷新令牌
// TODO: 待确认使用 —— 当前刷新令牌只写 Redis，此方法暂无调用方。
func (r *UserRepo) CreateRefreshToken(ctx context.Context, rt *model.UserRefreshToken) error {
	return r.db.WithContext(ctx).Create(rt).Error
}

// GetRefreshToken 根据 token 字符串查询未过期的刷新令牌（匹配 token_hash 列）。
// TODO: 待确认使用 —— 当前刷新令牌只写 Redis，此方法暂无调用方。
func (r *UserRepo) GetRefreshToken(ctx context.Context, tokenStr string) (*model.UserRefreshToken, error) {
	var rt model.UserRefreshToken
	// 避免硬编码表字段，使用结构体字段名，确保与模型一致
	err := r.db.WithContext(ctx).Where(&model.UserRefreshToken{TokenHash: tokenStr}).
		Where("expires_at > now()").
		First(&rt).Error
	if err != nil {
		return nil, err
	}
	return &rt, nil
}

// DeleteRefreshToken 按 token 哈希删除单条刷新令牌
// TODO: 待确认使用 —— 当前刷新令牌只写 Redis，此方法暂无调用方。
func (r *UserRepo) DeleteRefreshToken(ctx context.Context, tokenStr string) error {
	err := r.db.WithContext(ctx).Where(&model.UserRefreshToken{TokenHash: tokenStr}).Delete(&model.UserRefreshToken{}).Error
	if err != nil {
		logger.Ctx(ctx).Error("delete refresh token failed", logger.Err(err))
		return err
	}
	return nil
}

// CleanExpiredRefreshToken 清理指定用户已过期的刷新令牌
// TODO: 待确认使用 —— 当前刷新令牌只写 Redis，此方法暂无调用方。
func (r *UserRepo) CleanExpiredRefreshToken(ctx context.Context, userID uint) error {
	err := r.db.WithContext(ctx).Where(&model.UserRefreshToken{UserID: userID}).Where("expires_at <= now()").Delete(&model.UserRefreshToken{}).Error
	if err != nil {
		logger.Ctx(ctx).Error("clean expired refresh tokens failed", logger.Err(err))
		return err
	}
	return nil
}

// DeleteRefreshTokenByUserID 根据用户ID，删除该用户全部刷新令牌（登出全部设备）
// TODO: 待确认使用 —— 当前刷新令牌只写 Redis，此方法暂无调用方。
func (r *UserRepo) DeleteRefreshTokenByUserID(ctx context.Context, userID uint) error {
	err := r.db.WithContext(ctx).
		Where(&model.UserRefreshToken{UserID: userID}).
		Delete(&model.UserRefreshToken{}).Error
	if err != nil {
		logger.Ctx(ctx).Error("delete refresh tokens by user id failed", logger.Err(err))
		return err
	}
	return nil
}

// UpdateProfile 更新用户资料。
// userID 为 0 时必须拒绝，否则 GORM 会忽略结构体中的零值主键，更新到第一条用户。
func (r *UserRepo) UpdateProfile(ctx context.Context, userID uint, updatedFields map[string]interface{}) error {
	if userID == 0 {
		return fmt.Errorf("update user profile: empty user id: %w", ErrInvalidArgument)
	}
	if len(updatedFields) == 0 {
		return fmt.Errorf("update user profile: no fields to update: %w", ErrInvalidArgument)
	}

	err := r.db.WithContext(ctx).Model(&model.User{}).Where("id = ?", userID).Updates(updatedFields).Error
	if err != nil {
		logger.Ctx(ctx).Error("update user profile failed", logger.Err(err))
		return err
	}
	return nil
}

// UpdatePassword 更新用户密码
func (r *UserRepo) UpdatePassword(ctx context.Context, userID uint, newPasswordHash string) error {
	if userID == 0 {
		return fmt.Errorf("update user password: empty user id: %w", ErrInvalidArgument)
	}

	user := model.User{
		Password: newPasswordHash,
	}
	err := r.db.WithContext(ctx).
		Model(&model.User{}).
		Where("id = ?", userID).
		Select("Password").
		Updates(user).
		Error
	if err != nil {
		logger.Ctx(ctx).Error("update user password failed", logger.Err(err))
		return err
	}
	return nil
}
