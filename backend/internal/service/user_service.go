package service

import (
	"context"
	"errors"
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
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// 预定义业务错误，供handler按HTTP语义映射状态码，避免向前端泄露底层错误细节
var (
	// ErrInvalidCredentials 账号不存在与密码错误统一使用该错误，防止账号枚举
	ErrInvalidCredentials = errors.New("invalid username or password")
	// ErrInvalidRefreshToken refresh token不存在、过期或与用户不匹配
	ErrInvalidRefreshToken = errors.New("invalid or expired refresh token")
	// ErrRefreshTokenUnavailable refresh token依赖的缓存服务不可用
	ErrRefreshTokenUnavailable = errors.New("refresh token service unavailable")
	// ErrOldPasswordIncorrect 修改密码时旧密码校验失败
	ErrOldPasswordIncorrect = errors.New("old password is incorrect")
	// ErrUsernameInvalid 用户名格式不合法
	ErrUsernameInvalid = errors.New("invalid username")
	// ErrUsernameExists 用户名已被占用
	ErrUsernameExists = errors.New("username already exists")
	// ErrPasswordTooShort 密码长度不足
	ErrPasswordTooShort = errors.New("password is too short")
	// ErrPasswordTooLong 密码超过 bcrypt 可处理的 72 字节上限
	ErrPasswordTooLong = errors.New("password exceeds 72 bytes")
	// ErrPasswordWeak 密码强度不足
	ErrPasswordWeak = errors.New("password is too weak")
	// ErrInvalidFileSize 上传文件大小不合法
	ErrInvalidFileSize = errors.New("invalid file size")
	// ErrInvalidFileType 上传文件后缀或真实内容类型不合法
	ErrInvalidFileType = errors.New("invalid file type")
	// ErrInvalidAvatarURL 头像地址不是本服务静态资源路径
	ErrInvalidAvatarURL = errors.New("invalid avatar url")
	// ErrNoFieldsToUpdate 更新资料时没有任何可更新字段
	ErrNoFieldsToUpdate = errors.New("no fields to update")
)

const (
	minUsernameLen   = 3  // 用户名最小长度
	maxUsernameLen   = 32 // 用户名最大长度
	minPasswordLen   = 8  // 密码最小长度（按字符数）
	maxPasswordBytes = 72 // bcrypt 输入上限，超过会被静默截断
)

// usernamePattern 用户名仅允许字母、数字、下划线
var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// validateUsername 校验用户名长度与字符集，禁止首尾空白和特殊符号
func validateUsername(username string) error {
	if username != strings.TrimSpace(username) {
		return fmt.Errorf("%w: leading or trailing whitespace", ErrUsernameInvalid)
	}
	length := utf8.RuneCountInString(username)
	if length < minUsernameLen || length > maxUsernameLen || !usernamePattern.MatchString(username) {
		return fmt.Errorf("%w: must be %d-%d chars of letters, digits or underscore", ErrUsernameInvalid, minUsernameLen, maxUsernameLen)
	}
	return nil
}

// validatePassword 校验密码强度与长度；超过 72 字节直接拒绝，避免 bcrypt 静默截断
func validatePassword(password string) error {
	if utf8.RuneCountInString(password) < minPasswordLen {
		return fmt.Errorf("%w: at least %d characters", ErrPasswordTooShort, minPasswordLen)
	}
	if len(password) > maxPasswordBytes {
		return fmt.Errorf("%w: max %d bytes", ErrPasswordTooLong, maxPasswordBytes)
	}
	var hasLetter, hasDigit bool
	for _, r := range password {
		if unicode.IsLetter(r) {
			hasLetter = true
		}
		if unicode.IsDigit(r) {
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return fmt.Errorf("%w: must contain both letters and digits", ErrPasswordWeak)
	}
	return nil
}

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
	// 用户名/密码格式校验在落库与 bcrypt 之前完成
	if err := validateUsername(username); err != nil {
		return nil, err
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}

	// 查找用户是否存在
	existingUser, err := s.UserRepo.FindByUsername(ctx, username)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("check username exists: %w", err)
	}
	if existingUser != nil {
		return nil, ErrUsernameExists
	}

	// 加密密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
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
		return nil, fmt.Errorf("register user: %w", err)
	}
	return user, nil
}

// Login 用户登录
func (s *UserService) Login(ctx context.Context, username, password string) (accessToken string, refreshToken string, err error) {
	// 空用户名/密码直接按凭证错误处理，禁止把空用户名下推到 repo 生成无 WHERE 查询
	if strings.TrimSpace(username) == "" || password == "" {
		return "", "", ErrInvalidCredentials
	}

	// 查找用户：账号不存在与底层错误分开处理，对外统一为凭证错误
	user, err := s.UserRepo.FindByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("Login failed: user not found")
			return "", "", ErrInvalidCredentials
		}
		return "", "", fmt.Errorf("find user for login: %w", err)
	}

	// 验证密码：失败时返回与账号不存在完全一致的错误
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		log.Printf("Login failed: invalid password")
		return "", "", ErrInvalidCredentials
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

	// 缓存禁用时无法持久化refresh token，仅返回令牌，不阻断登录主流程
	if !s.cache.IsEnabled() {
		log.Printf("WARN: cache disabled, refresh token not persisted, userID=%d", user.ID)
		return accessToken, refreshToken, nil
	}

	// 改用Redis缓存保存刷新令牌
	// key 格式 refresh_token:{refreshToken字符串}，value存userID，单个 token 独立 TTL
	redisKey := cache.RefreshTokenKey(refreshToken)
	ttl := cache.RefreshTokenTTL
	err = s.cache.Set(ctx, redisKey, user.ID, ttl)
	if err != nil {
		log.Printf("Error saving refresh token to Redis in UserService: %v", err)
		return "", "", fmt.Errorf("save refresh token to redis failed: %w", err)
	}
	// 双向索引<userId, refreshToken>
	if setErr := s.cache.AddUserRefreshSet(ctx, user.ID, refreshToken, ttl); setErr != nil {
		// 捕获内部真实错误，避免打印外层通常为 nil 的 err 导致日志误导
		log.Printf("Error adding refresh token to user set in Redis in UserService: userID=%d err=%v", user.ID, setErr)
		return "", "", fmt.Errorf("add refresh token to user set failed: %w", setErr)
	}

	return accessToken, refreshToken, nil
}

// RefreshToken 刷新令牌
func (s *UserService) RefreshToken(ctx context.Context, refreshToken string, OldAccessToken string) (newAccessToken string, newRefreshToken string, uid uint, uname string, err error) {
	if refreshToken == "" {
		return "", "", 0, "", ErrInvalidRefreshToken
	}
	// refresh token仅存储于Redis，缓存禁用时无法校验，返回明确错误而不是误判为令牌失效
	if !s.cache.IsEnabled() {
		log.Printf("ERROR: cache disabled, cannot validate refresh token")
		return "", "", 0, "", ErrRefreshTokenUnavailable
	}

	redisKey := cache.RefreshTokenKey(refreshToken)

	// 原子读取并删除旧refresh token：并发刷新时只有一个请求能拿到userID，其余直接失败
	valStr, err := s.cache.GetDel(ctx, redisKey)
	if err != nil {
		if errors.Is(err, cache.ErrCacheMiss) {
			return "", "", 0, "", fmt.Errorf("%w: token not found", ErrInvalidRefreshToken)
		}
		// Redis故障不能当成token失效，避免把服务异常误报为401
		log.Printf("Error getting refresh token from Redis in UserService: %v", err)
		return "", "", 0, "", fmt.Errorf("%w: get refresh token failed", ErrRefreshTokenUnavailable)
	}
	userIDuint64, err := strconv.ParseUint(valStr, 10, 64) // 将字符串转换为uint64
	if err != nil {
		log.Printf("Error parsing userID from Redis value in UserService: %v", err)
		return "", "", 0, "", fmt.Errorf("%w: invalid token data", ErrInvalidRefreshToken)
	}
	uid = uint(userIDuint64)

	// 查找用户
	user, err := s.UserRepo.FindByID(ctx, uid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", "", 0, "", fmt.Errorf("%w: user not found", ErrInvalidRefreshToken)
		}
		return "", "", 0, "", fmt.Errorf("find user for refresh token: %w", err)
	}

	err = s.cache.RemoveUserRefreshSetItem(ctx, uid, refreshToken) // 从用户的refresh token集合中移除旧的refresh token
	if err != nil {
		log.Printf("Error removing refresh token from user set in Redis in UserService: %v", err)
		return "", "", 0, "", fmt.Errorf("failed remove old refresh token from user set: %w", err)
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

	// 将新refreshToken写入Redis，单个 token 独立 TTL
	newRedisKey := cache.RefreshTokenKey(newRefreshToken)
	ttl := cache.RefreshTokenTTL
	err = s.cache.Set(ctx, newRedisKey, user.ID, ttl)
	if err != nil {
		return "", "", 0, "", fmt.Errorf("failed store new refresh token: %w", err)
	}
	// 双向索引<userId, refreshToken>
	err = s.cache.AddUserRefreshSet(ctx, user.ID, newRefreshToken, ttl)
	if err != nil {
		log.Printf("Error adding refresh token to user set in Redis in UserService: %v", err)
		return "", "", 0, "", fmt.Errorf("failed add refresh token to user set: %w", err)
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

	// 缓存禁用时无法校验与撤销refresh token，跳过缓存操作，登出本身不返回错误
	if !s.cache.IsEnabled() {
		log.Printf("WARN: cache disabled, skip refresh token revocation on logout, userID=%d", userID)
		return nil
	}

	// 获取refresh token对应的userID，与access token中解析出的userID进行比对，确保是同一个用户在登出
	redisKey := cache.RefreshTokenKey(refreshToken)
	valStr, err := s.cache.Get(ctx, redisKey)
	if err != nil {
		log.Printf("Error getting refresh token from Redis in UserService: %v", err)
		return fmt.Errorf("%w: token not found", ErrInvalidRefreshToken)
	}
	userIDuint64, err := strconv.ParseUint(valStr, 10, 64) // 将字符串转换为uint64
	if err != nil {
		log.Printf("Error parsing userID from Redis value in UserService: %v", err)
		return fmt.Errorf("%w: invalid token data", ErrInvalidRefreshToken)
	}
	if uint(userIDuint64) != userID { // 对比用户ID
		log.Printf("User ID mismatch during logout: token userID %d, context userID %d", userIDuint64, userID)
		return fmt.Errorf("%w: user ID mismatch", ErrInvalidRefreshToken)
	}

	// 删除refresh token缓存
	err = s.cache.Delete(ctx, redisKey)
	if err != nil {
		log.Printf("Error deleting refresh token from Redis in UserService: %v", err)
		return fmt.Errorf("failed to delete refresh token")
	}
	err = s.cache.RemoveUserRefreshSetItem(ctx, userID, refreshToken) // 从用户的refresh token集合中移除旧的refresh token
	if err != nil {
		log.Printf("Error removing refresh token from user set in Redis in UserService: %v", err)
		return fmt.Errorf("failed remove refresh token from user set: %w", err)
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
	if userID == 0 {
		return nil, fmt.Errorf("update profile: invalid user id")
	}

	updateMap := make(map[string]interface{})

	// 逐个检查请求参数是否为空，如果不为空则加入更新映射
	if req.UserName != nil {
		// 与注册共用同一套用户名规则，避免注册后改成非法用户名绕过校验
		if err := validateUsername(*req.UserName); err != nil {
			return nil, err
		}
		// 检查用户名是否已存在
		existingUser, err := s.UserRepo.FindByUsername(ctx, *req.UserName)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			// 出现错误，并且不是记录未找到的错误，说明查询失败
			log.Printf("Error checking existing username in UserService: %v", err)
			return nil, fmt.Errorf("check existing username: %w", err)
		}
		if existingUser != nil && existingUser.ID != userID {
			// 该用户名已被其他用户使用
			return nil, ErrUsernameExists
		}
		updateMap["user_name"] = *req.UserName
	}
	if req.AvatarURL != nil {
		// 只允许本服务静态资源路径，拒绝外部http/https地址
		if !strings.HasPrefix(*req.AvatarURL, "/upload/") {
			return nil, fmt.Errorf("%w: must start with /upload/", ErrInvalidAvatarURL)
		}
		updateMap["avatar_url"] = *req.AvatarURL
	}
	if req.Bio != nil {
		updateMap["bio"] = *req.Bio
	}

	// 如果没有任何字段需要更新，直接返回错误
	if len(updateMap) == 0 {
		return nil, ErrNoFieldsToUpdate
	}

	// 调用仓库层更新用户资料
	err := s.UserRepo.UpdateProfile(ctx, userID, updateMap)
	if err != nil {
		log.Printf("Error updating user profile in UserService: %v", err)
		return nil, fmt.Errorf("update user profile: %w", err)
	}

	// 更新成功后，查询最新的用户资料并返回
	return s.GetProfile(ctx, userID)
}

// UploadAvatar 上传用户头像
func (s *UserService) UploadAvatar(ctx context.Context, userID uint, fh *multipart.FileHeader) (string, error) {
	const maxSize = 10 << 20 // 设置最大文件大小为10MB
	if fh.Size <= 0 || fh.Size > maxSize {
		log.Printf("File size is invalid in UploadAvatar: %v", fh.Size)
		return "", fmt.Errorf("%w: size=%d", ErrInvalidFileSize, fh.Size)
	}

	// 检查文件扩展名是否合法
	ext := strings.ToLower(filepath.Ext(fh.Filename)) // 获取文件扩展名并转换为小写
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
	default:
		log.Printf("Invalid file type in UploadAvatar: %v", ext)
		return "", fmt.Errorf("%w: ext=%s", ErrInvalidFileType, ext)
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

	// 读取文件头嗅探真实 MIME，并与后缀交叉校验，防止伪造 .png 后缀上传任意内容
	mimeType, body, err := detectUploadMIME(srcFile)
	if err != nil {
		return "", fmt.Errorf("detect uploaded avatar type failed: %w", err)
	}
	if !uploadMIMEMatchesExt(ext, mimeType) {
		log.Printf("Invalid avatar content type in UploadAvatar: ext=%s mime=%s", ext, mimeType)
		return "", fmt.Errorf("%w: ext=%s content=%s", ErrInvalidFileType, ext, mimeType)
	}

	// 创建本地目标文件
	dstFile, err := os.Create(absPath)
	if err != nil {
		return "", fmt.Errorf("create target file failed: %w", err)
	}

	// 拷贝流写入磁盘；失败时清理半截文件，避免残留
	_, copyErr := io.Copy(dstFile, body)
	closeErr := dstFile.Close()
	if copyErr != nil {
		_ = os.Remove(absPath)
		return "", fmt.Errorf("copy file content failed: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(absPath)
		return "", fmt.Errorf("close target file failed: %w", closeErr)
	}

	// 返回相对路径，供handler层拼接成http可访问URL
	urlPath := path.Join("/upload", "avatars", strconv.FormatUint(uint64(userID), 10), filename)

	return urlPath, nil
}

// ChangePassword 修改用户密码
func (s *UserService) ChangePassword(ctx context.Context, userID uint, oldPassword, newPassword, access_token string, remainingExpire int64) error {
	// 新密码沿用注册的强度与长度规则，避免绕过 72 字节上限
	if err := validatePassword(newPassword); err != nil {
		return err
	}

	// 查找用户
	user, err := s.UserRepo.FindByID(ctx, userID)
	if err != nil {
		log.Printf("Error finding user in ChangePassword: %v", err)
		return fmt.Errorf("user not found")
	}

	// 验证旧密码
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(oldPassword)); err != nil {
		log.Printf("Old password does not match in ChangePassword for userID %d", userID)
		return ErrOldPasswordIncorrect
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

	// 缓存禁用时无法写入黑名单/清理refresh token，跳过缓存操作，密码已在数据库更新完成
	if !s.cache.IsEnabled() {
		log.Printf("WARN: cache disabled, skip token revocation after password change, userID=%d", userID)
		return nil
	}

	// 先删除该用户名下全部refresh token，保证改密后旧refresh无法继续换取access。
	// Redis 操作失败必须向上返回，不能吞掉错误让接口误报成功。
	if err := s.cache.CleanAllUserRefresh(ctx, userID); err != nil {
		log.Printf("Error cleaning all refresh tokens in ChangePassword for userID %d: %v", userID, err)
		return fmt.Errorf("failed to revoke refresh tokens: %w", err)
	}

	// 拉黑access token
	ttl := time.Duration(remainingExpire) * time.Second
	if ttl > 0 { // 如果剩余有效期大于0，才加入黑名单
		err = s.cache.AddTokenToBlacklist(ctx, access_token, ttl)
		if err != nil {
			log.Printf("Error adding access token to blacklist in ChangePassword: %v", err)
			return fmt.Errorf("failed to blacklist access token: %w", err)
		}
	}

	return nil
}

// FindByID 根据ID查找用户
func (s *UserService) FindByID(ctx context.Context, id uint) (*dto.ProfileResponse, error) {
	user, err := s.UserRepo.FindByID(ctx, id)
	if err != nil {
		log.Printf("Error finding user by ID in FindByID: %v", err)
		return nil, fmt.Errorf("user not found")
	}

	return &dto.ProfileResponse{
		ID:        user.ID,
		Username:  user.UserName,
		AvatarURL: user.AvatarURL,
		Bio:       user.Bio,
	}, nil
}

// FindByUsername 根据用户名查找用户
func (s *UserService) FindByUsername(ctx context.Context, username string) (*dto.ProfileResponse, error) {
	if strings.TrimSpace(username) == "" {
		return nil, fmt.Errorf("username is required")
	}

	user, err := s.UserRepo.FindByUsername(ctx, username)
	if err != nil {
		log.Printf("Error finding user by username in FindByUsername: %v", err)
		return nil, fmt.Errorf("user not found")
	}

	return &dto.ProfileResponse{
		ID:        user.ID,
		Username:  user.UserName,
		AvatarURL: user.AvatarURL,
		Bio:       user.Bio,
	}, nil
}
