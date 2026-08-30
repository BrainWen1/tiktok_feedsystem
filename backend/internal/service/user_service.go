package service

import (
	"context"
	"feedsystem/internal/dto"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"feedsystem/internal/utils/file"
	"feedsystem/internal/utils/jwt"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
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

	// 注册直接填入默认头像和简介
	avatarURL := "/static/default_avatar.png" // 默认头像URL
	bio := "这个人很懒，什么都没有留下。"                   // 默认简介

	// 创建用户
	user := &model.User{
		UserName:  username,
		Password:  string(hashedPassword),
		AvatarURL: avatarURL,
		Bio:       bio,
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

// GetProfile 获取用户资料
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

// UpdateProfile 更新用户资料
func (s *UserService) UpdateProfile(ctx context.Context, userID uint, req dto.UpdateProfileRequest) (*dto.ProfileResponse, error) {
	updateMap := make(map[string]interface{})

	// 逐个检查请求参数是否为空，如果不为空则加入更新映射
	if req.UserName != nil {
		// 检查用户名是否已存在
		existingUser, err := s.UserRepo.FindByUsername(ctx, *req.UserName)
		if err != nil && err != gorm.ErrRecordNotFound {
			// 出现错误，并且不是记录未找到的错误，说明查询失败
			log.Printf("Error checking existing username in UserService: %v", err)
			return nil, fmt.Errorf("failed to check existing username")
		}
		if existingUser != nil && existingUser.ID != userID {
			// 该用户名已被其他用户使用
			return nil, fmt.Errorf("username already exists")
		}
		updateMap["user_name"] = *req.UserName
	}
	if req.AvatarURL != nil {
		// 只允许本服务静态资源路径，拒绝外部http/https地址
		if !strings.HasPrefix(*req.AvatarURL, "/upload/") {
			return nil, fmt.Errorf("invalid avatar URL, must start with /upload/")
		}
		updateMap["avatar_url"] = *req.AvatarURL
	}
	if req.Bio != nil {
		updateMap["bio"] = *req.Bio
	}

	// 如果没有任何字段需要更新，直接返回错误
	if len(updateMap) == 0 {
		return nil, fmt.Errorf("no fields to update")
	}

	// 调用仓库层更新用户资料
	err := s.UserRepo.UpdateProfile(ctx, userID, updateMap)
	if err != nil {
		log.Printf("Error updating user profile in UserService: %v", err)
		return nil, err
	}

	// 更新成功后，查询最新的用户资料并返回
	return s.GetProfile(ctx, userID)
}

// UploadAvatar 上传用户头像
func (s *UserService) UploadAvatar(ctx context.Context, userID uint, fh *multipart.FileHeader) (string, error) {
	const maxSize = 10 << 20 // 设置最大文件大小为10MB
	if fh.Size <= 0 || fh.Size > maxSize {
		log.Printf("File size is invalid in UploadAvatar: %v", fh.Size)
		return "", fmt.Errorf("invalid file size")
	}

	// 检查文件扩展名是否合法
	ext := strings.ToLower(filepath.Ext(fh.Filename)) // 获取文件扩展名并转换为小写
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
	default:
		log.Printf("Invalid file type in UploadAvatar: %v", ext)
		return "", fmt.Errorf("invalid file type")
	}

	// 创建用户头像存储目录
	dir := filepath.Join(".run", "upload", "avatars", strconv.FormatUint(uint64(userID), 10)) // 拼接目录路径
	if err := os.MkdirAll(dir, 0o755); err != nil {                                           // 创建目录，如果目录已存在则不会报错
		log.Printf("Failed to create directory in UploadAvatar: %v", err)
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	// 生成随机文件名
	filename, err := file.RandHex(16) // 生成16字节的随机十六进制字符串作为文件名
	if err != nil {
		log.Printf("Failed to generate random filename in UploadAvatar: %v", err)
		return "", fmt.Errorf("failed to generate random filename: %w", err)
	}
	filename = filename + ext // 拼接完整文件名

	// 拼接完整文件路径
	absPath := filepath.Join(dir, filename)

	// 保存上传的文件到指定路径，由于service层不能绑定gin.Context，所以这里使用io.Copy来保存文件
	// 打开上传的文件
	srcFile, err := fh.Open()
	if err != nil {
		return "", fmt.Errorf("open upload file failed: %w", err)
	}
	defer srcFile.Close() // 注册延迟关闭文件句柄

	// 创建本地目标文件
	dstFile, err := os.Create(absPath)
	if err != nil {
		return "", fmt.Errorf("create target file failed: %w", err)
	}
	defer dstFile.Close()

	// 拷贝流写入磁盘
	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return "", fmt.Errorf("copy file content failed: %w", err)
	}

	// 返回相对路径，供handler层拼接成http可访问URL
	urlPath := path.Join("/upload", "avatars", strconv.FormatUint(uint64(userID), 10), filename)

	return urlPath, nil
}

// ChangePassword 修改用户密码
func (s *UserService) ChangePassword(ctx context.Context, userID uint, oldPassword, newPassword, access_token string, remainingExpire int64) error {
	// 查找用户
	user, err := s.UserRepo.FindByID(ctx, userID)
	if err != nil {
		log.Printf("Error finding user in ChangePassword: %v", err)
		return fmt.Errorf("user not found")
	}

	// 验证旧密码
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(oldPassword)); err != nil {
		log.Printf("Old password does not match in ChangePassword for userID %d", userID)
		return fmt.Errorf("old password is incorrect")
	}

	// 加密新密码
	hashedNewPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("Error hashing new password in ChangePassword: %v", err)
		return fmt.Errorf("failed to hash new password")
	}

	// 更新密码
	err = s.UserRepo.UpdatePassword(ctx, userID, string(hashedNewPassword))
	if err != nil {
		log.Printf("Error updating password in ChangePassword: %v", err)
		return fmt.Errorf("failed to update password")
	}

	// 拉黑access token
	ttl := time.Duration(remainingExpire) * time.Second
	if ttl > 0 { // 如果剩余有效期大于0，才加入黑名单
		err = s.cache.AddTokenToBlacklist(ctx, access_token, ttl)
		if err != nil {
			log.Printf("Error adding access token to blacklist in ChangePassword: %v", err)
			return fmt.Errorf("failed to blacklist access token")
		}
	}

	// 删除该用户的所有refresh token，这一步需要redis里存在<id, refresh_token>的映射，才能删除，目前没有做这个键值对，先鸽着

	return nil
}
