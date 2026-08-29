package service

import (
	"context"
	"feedsystem/internal/dto"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"feedsystem/internal/utils/jwt"
	"fmt"
	"log"
	"strconv"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// UserService 用户服务
type UserService struct {
	UserRepo *repo.UserRepo
	cache    *cache.RedisCache
}

// NewUserService 创建用户服务
func NewUserService(userRepo *repo.UserRepo, cache *cache.RedisCache) *UserService {
	return &UserService{
		UserRepo: userRepo,
		cache:    cache,
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
	accessToken, err = jwt.GenerateToken(user.ID, user.UserName)
	if err != nil {
		return "", "", err
	}

	refreshToken, err = jwt.GenerateRefreshToken(user.ID)
	if err != nil {
		return "", "", err
	}

	// // 组装刷新令牌模型
	// refreshTokenModel := &model.UserRefreshToken{
	// 	UserID:    user.ID,
	// 	TokenHash: refreshToken,
	// 	ExpiresAt: time.Now().Add(7 * 24 * time.Hour), // refresh token 有效期为7天
	// }
	// // 保存刷新令牌到数据库
	// err = s.UserRepo.CreateRefreshToken(ctx, refreshTokenModel)
	// if err != nil {
	// 	return "", "", err
	// }

	// 改用Redis缓存保存刷新令牌
	// key 格式 refresh_token:{refreshToken字符串}，value存userID，TTL7天
	redisKey := fmt.Sprintf("refresh_token:%s", refreshToken)
	ttl := 7 * 24 * time.Hour
	err = s.cache.Set(ctx, redisKey, user.ID, ttl)
	if err != nil {
		log.Printf("Error saving refresh token to Redis in UserService: %v", err)
		return "", "", fmt.Errorf("save refresh token to redis failed: %w", err)
	}

	return accessToken, refreshToken, nil
}

// RefreshToken 刷新令牌
func (s *UserService) RefreshToken(ctx context.Context, refreshToken string, OldAccessToken string) (newAccessToken string, newRefreshToken string, uid uint, uname string, err error) {
	if refreshToken == "" {
		return "", "", 0, "", fmt.Errorf("refresh token is empty")
	}

	redisKey := fmt.Sprintf("refresh_token:%s", refreshToken)

	// 从Redis读取用户ID
	valStr, err := s.cache.Get(ctx, redisKey)
	if err != nil {
		log.Printf("Error getting refresh token from Redis in UserService: %v", err)
		return "", "", 0, "", fmt.Errorf("invalid or expired refresh token")
	}
	userIDuint64, err := strconv.ParseUint(valStr, 10, 64) // 将字符串转换为uint64
	if err != nil {
		log.Printf("Error parsing userID from Redis value in UserService: %v", err)
		return "", "", 0, "", fmt.Errorf("invalid refresh token data")
	}
	uid = uint(userIDuint64)

	// 查找用户
	user, err := s.UserRepo.FindByID(ctx, uid)
	if err != nil {
		return "", "", 0, "", fmt.Errorf("user not found")
	}

	// 删除旧的refresh token
	err = s.cache.Delete(ctx, redisKey)
	if err != nil {
		return "", "", 0, "", fmt.Errorf("failed revoke old refresh token: %w", err)
	}
	// 拉黑旧的access token，设置过期时间为剩余有效期
	// 此项一定要设计成非必填的，因为有时候刷新就是因为access token过期了，前端没有办法传回
	if OldAccessToken != "" {
		remainingExpire, err := jwt.GetTokenRemainingExpire(OldAccessToken)
		if err == nil {
			// 解析成功，加入黑名单
			err = s.cache.AddTokenToBlacklist(ctx, OldAccessToken, time.Duration(remainingExpire)*time.Second)
			if err != nil {
				// 解析成功，但加入黑名单失败，记录日志并返回错误
				log.Printf("Error adding old access token to blacklist in UserService: %v", err)
				return "", "", 0, "", fmt.Errorf("failed to blacklist old access token: %w", err)
			}
		} else {
			// 解析失败，直接忽略，不要返回错误
			log.Printf("Error getting remaining expire for old access token in UserService: %v", err)
		}
	}

	// 生成新的访问令牌和刷新令牌
	newAccessToken, err = jwt.GenerateToken(user.ID, user.UserName)
	if err != nil {
		return "", "", 0, "", err
	}

	newRefreshToken, err = jwt.GenerateRefreshToken(user.ID)
	if err != nil {
		return "", "", 0, "", err
	}

	// // 删除旧的刷新令牌
	// err = s.UserRepo.DeleteRefreshToken(ctx, refreshToken)
	// if err != nil {
	// 	return "", "", 0, "", err
	// }
	// // 保存新的刷新令牌到数据库
	// newRefreshTokenSession := &model.UserRefreshToken{
	// 	UserID:    user.ID,
	// 	TokenHash: newRefreshToken,
	// 	ExpiresAt: time.Now().Add(7 * 24 * time.Hour), // refresh token 有效期为7天
	// }
	// err = s.UserRepo.CreateRefreshToken(ctx, newRefreshTokenSession)
	// if err != nil {
	// 	return "", "", 0, "", err
	// }

	// 将新refreshToken写入Redis，7天TTL
	newRedisKey := fmt.Sprintf("refresh_token:%s", newRefreshToken)
	ttl := 7 * 24 * time.Hour
	err = s.cache.Set(ctx, newRedisKey, user.ID, ttl)
	if err != nil {
		return "", "", 0, "", fmt.Errorf("failed store new refresh token: %w", err)
	}

	return newAccessToken, newRefreshToken, user.ID, user.UserName, nil
}

// Logout 用户登出
func (s *UserService) Logout(ctx context.Context, userID uint, refreshToken, accessToken string, remainingExpire int64) error {
	// // 删除该用户的所有刷新令牌
	// err := s.UserRepo.DeleteRefreshTokenByUserID(ctx, userID)
	// if err != nil {
	// 	log.Printf("Error logging out user in UserService: %v", err)
	// 	return err
	// }
	// return nil

	// 改用Redis缓存删除该用户的所有刷新令牌
	// 由于我们在Redis中使用的key是 refresh_token:{refreshToken字符串}，而没有直接存储userID到key中
	// 所以我们无法直接通过userID删除所有相关的refresh token
	// 因此，我们需要在用户登出时，前端传递当前的refresh token，服务端根据这个token删除对应的缓存

	// 获取refresh token对应的userID，与access token中解析出的userID进行比对，确保是同一个用户在登出
	redisKey := fmt.Sprintf("refresh_token:%s", refreshToken)
	valStr, err := s.cache.Get(ctx, redisKey)
	if err != nil {
		log.Printf("Error getting refresh token from Redis in UserService: %v", err)
		return fmt.Errorf("invalid or expired refresh token")
	}
	userIDuint64, err := strconv.ParseUint(valStr, 10, 64) // 将字符串转换为uint64
	if err != nil {
		log.Printf("Error parsing userID from Redis value in UserService: %v", err)
		return fmt.Errorf("invalid refresh token data")
	}
	if uint(userIDuint64) != userID { // 对比用户ID
		log.Printf("User ID mismatch during logout: token userID %d, context userID %d", userIDuint64, userID)
		return fmt.Errorf("user ID mismatch")
	}

	// 删除refresh token缓存
	err = s.cache.Delete(ctx, redisKey)
	if err != nil {
		log.Printf("Error deleting refresh token from Redis in UserService: %v", err)
		return fmt.Errorf("failed to delete refresh token")
	}

	// 将access token加入黑名单，设置过期时间为剩余有效期
	ttl := time.Duration(remainingExpire) * time.Second // time.Duration是以纳秒为单位的，所以需要乘以time.Second
	if ttl <= 0 {                                       // 如果剩余有效期小于等于0，说明token已经过期，无需加入黑名单
		log.Printf("Access token already expired, no need to blacklist")
		return nil
	}

	err = s.cache.AddTokenToBlacklist(ctx, accessToken, ttl)
	if err != nil {
		log.Printf("Error adding access token to blacklist in UserService: %v", err)
		return fmt.Errorf("failed to blacklist access token")
	}

	return nil
}

func (s *UserService) GetProfile(ctx context.Context, userID uint) (*dto.ProfileResponse, error) {
	// 根据用户ID查询数据库
	user, err := s.UserRepo.FindByID(ctx, userID)
	if err != nil {
		log.Printf("Error getting user profile in UserService: %v", err)
		return nil, err
	}

	// 组装脱敏返回
	return &dto.ProfileResponse{
		ID:        user.ID,
		Username:  user.UserName,
		AvatarURL: user.AvatarURL,
		Bio:       user.Bio,
	}, nil
}
