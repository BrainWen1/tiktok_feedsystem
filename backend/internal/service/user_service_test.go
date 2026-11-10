package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"feedsystem/internal/config"
	"feedsystem/internal/dto"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"feedsystem/internal/utils/jwt"

	"github.com/alicebob/miniredis/v2"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	testUserPassword = "abc12345"
	testNewPassword  = "newpass123"
)

// setupUserService 组装一套完全本地的测试环境：
//   - SQLite 内存库替代 MySQL，每个测试独立建表；
//   - miniredis 替代真实 Redis，覆盖 refresh token 轮换与黑名单逻辑；
//   - 显式注入 JWT 测试密钥，避免依赖 configs/.env.dev。
func setupUserService(t *testing.T) (*UserService, *cache.RedisCache, *gorm.DB) {
	t.Helper()

	oldSecret, oldExpire := config.AppConfig.JWT_secret, config.AppConfig.JWT_expire_hour
	config.AppConfig.JWT_secret = "user-service-test-secret-0123456789"
	config.AppConfig.JWT_expire_hour = 1
	t.Cleanup(func() {
		config.AppConfig.JWT_secret = oldSecret
		config.AppConfig.JWT_expire_hour = oldExpire
	})

	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", name)
	// 测试会主动触发唯一键冲突/record not found，关闭 GORM 默认 SQL 日志避免噪音。
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(&model.User{}); err != nil {
		t.Fatalf("auto migrate user: %v", err)
	}

	mr := miniredis.RunT(t)
	redisCache := cache.NewRedisCache(mr.Addr(), "", 0)
	t.Cleanup(func() { _ = redisCache.Close() })

	return NewUserService(repo.NewUserRepo(db), redisCache), redisCache, db
}

func mustRegister(t *testing.T, s *UserService, username, password string) *model.User {
	t.Helper()
	user, err := s.Register(context.Background(), username, password)
	if err != nil {
		t.Fatalf("register %q: %v", username, err)
	}
	return user
}

func mustLogin(t *testing.T, s *UserService, username, password string) (string, string) {
	t.Helper()
	accessToken, refreshToken, _, _, err := s.Login(context.Background(), username, password)
	if err != nil {
		t.Fatalf("login %q: %v", username, err)
	}
	return accessToken, refreshToken
}

func TestValidateUsername(t *testing.T) {
	tests := []struct {
		name     string
		username string
		wantErr  error
	}{
		{name: "valid", username: "alice_01", wantErr: nil},
		{name: "valid min length", username: "abc", wantErr: nil},
		{name: "valid max length", username: strings.Repeat("a", 32), wantErr: nil},
		{name: "too short", username: "ab", wantErr: ErrUsernameInvalid},
		{name: "too long", username: strings.Repeat("a", 33), wantErr: ErrUsernameInvalid},
		{name: "illegal hyphen", username: "alice-01", wantErr: ErrUsernameInvalid},
		{name: "illegal space inside", username: "alice 01", wantErr: ErrUsernameInvalid},
		{name: "leading space", username: " alice", wantErr: ErrUsernameInvalid},
		{name: "trailing space", username: "alice ", wantErr: ErrUsernameInvalid},
		{name: "unicode rejected", username: "用户名abc", wantErr: ErrUsernameInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateUsername(tt.username)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("validateUsername(%q) error = %v, want nil", tt.username, err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("validateUsername(%q) error = %v, want %v", tt.username, err, tt.wantErr)
			}
		})
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  error
	}{
		{name: "valid", password: "abcd1234", wantErr: nil},
		{name: "valid unicode letters", password: "密码密码密码密码1", wantErr: nil},
		// bcrypt 的输入上限是 72 字节，边界值必须精确放行/拒绝。
		{name: "exactly 72 bytes", password: strings.Repeat("a", 71) + "1", wantErr: nil},
		{name: "73 bytes", password: strings.Repeat("a", 72) + "1", wantErr: ErrPasswordTooLong},
		{name: "too short", password: "abcd123", wantErr: ErrPasswordTooShort},
		{name: "letters only", password: "abcdefgh", wantErr: ErrPasswordWeak},
		{name: "digits only", password: "12345678", wantErr: ErrPasswordWeak},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePassword(tt.password)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("validatePassword error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("validatePassword error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestRegisterHashesPasswordAndRejectsDuplicate(t *testing.T) {
	s, _, db := setupUserService(t)
	ctx := context.Background()

	user, err := s.Register(ctx, "alice_01", testUserPassword)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if user.ID == 0 {
		t.Fatal("register should return persisted user id")
	}
	if user.Password == testUserPassword {
		t.Fatal("password must not be stored in plaintext")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(testUserPassword)); err != nil {
		t.Fatalf("stored password is not a valid bcrypt hash: %v", err)
	}

	var stored model.User
	if err := db.First(&stored, user.ID).Error; err != nil {
		t.Fatalf("load stored user: %v", err)
	}
	if stored.UserName != "alice_01" {
		t.Fatalf("stored username = %q", stored.UserName)
	}
	if stored.AvatarURL == "" || stored.Bio == "" {
		t.Fatalf("register should fill default avatar/bio, got %+v", stored)
	}

	if _, err := s.Register(ctx, "alice_01", testUserPassword); !errors.Is(err, ErrUsernameExists) {
		t.Fatalf("duplicate register error = %v, want ErrUsernameExists", err)
	}
}

func TestRegisterValidation(t *testing.T) {
	s, _, _ := setupUserService(t)
	ctx := context.Background()

	tests := []struct {
		name     string
		username string
		password string
		wantErr  error
	}{
		{name: "invalid username", username: "a!", password: testUserPassword, wantErr: ErrUsernameInvalid},
		{name: "short password", username: "valid_01", password: "a1", wantErr: ErrPasswordTooShort},
		{name: "weak password", username: "valid_01", password: "abcdefgh", wantErr: ErrPasswordWeak},
		{name: "long password", username: "valid_01", password: strings.Repeat("a", 72) + "1", wantErr: ErrPasswordTooLong},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.Register(ctx, tt.username, tt.password); !errors.Is(err, tt.wantErr) {
				t.Fatalf("register error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoginEmptyCredentialsRejectedBeforeRepo(t *testing.T) {
	// 空用户名/密码必须在访问 repo 前拦截；这里故意不注入 repo，nil 也能安全返回。
	s := &UserService{}

	tests := []struct {
		name     string
		username string
		password string
	}{
		{name: "empty username", username: "", password: testUserPassword},
		{name: "blank username", username: "   ", password: testUserPassword},
		{name: "empty password", username: "alice", password: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, _, _, err := s.Login(context.Background(), tt.username, tt.password); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("login error = %v, want ErrInvalidCredentials", err)
			}
		})
	}
}

func TestLoginIssuesTokensAndPersistsRefreshToken(t *testing.T) {
	s, rc, _ := setupUserService(t)
	ctx := context.Background()
	user := mustRegister(t, s, "bob_01", testUserPassword)

	accessToken, refreshToken := mustLogin(t, s, "bob_01", testUserPassword)
	if accessToken == "" || refreshToken == "" {
		t.Fatal("login should return both access and refresh tokens")
	}

	claims, err := jwt.ParseToken(accessToken)
	if err != nil {
		t.Fatalf("parse access token: %v", err)
	}
	if claims.UserID != user.ID || claims.UserName != user.UserName {
		t.Fatalf("claims = %+v, want user %d/%s", claims, user.ID, user.UserName)
	}

	// refresh token 必须落 Redis，并写入用户级索引集合，供改密/登出批量撤销。
	got, err := rc.Get(ctx, cache.RefreshTokenKey(refreshToken))
	if err != nil {
		t.Fatalf("get refresh token from redis: %v", err)
	}
	if got != strconv.FormatUint(uint64(user.ID), 10) {
		t.Fatalf("refresh token value = %q, want user id %d", got, user.ID)
	}
	isMember, err := rc.IsMemberOfSet(ctx, cache.RefreshSetKey(user.ID), refreshToken)
	if err != nil {
		t.Fatalf("check refresh set: %v", err)
	}
	if !isMember {
		t.Fatal("refresh token should be indexed in user refresh set")
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	s, _, _ := setupUserService(t)
	ctx := context.Background()
	mustRegister(t, s, "carol_01", testUserPassword)

	for _, tt := range []struct {
		name     string
		username string
		password string
	}{
		{name: "wrong password", username: "carol_01", password: "wrong123"},
		{name: "missing user", username: "nobody", password: testUserPassword},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, _, _, err := s.Login(ctx, tt.username, tt.password); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("login error = %v, want ErrInvalidCredentials", err)
			}
		})
	}
}

func TestRefreshTokenRotation(t *testing.T) {
	s, rc, _ := setupUserService(t)
	ctx := context.Background()
	user := mustRegister(t, s, "dave_01", testUserPassword)
	oldAccess, oldRefresh := mustLogin(t, s, "dave_01", testUserPassword)

	newAccess, newRefresh, userID, userName, err := s.RefreshToken(ctx, oldRefresh, oldAccess)
	if err != nil {
		t.Fatalf("refresh token: %v", err)
	}
	if userID != user.ID || userName != user.UserName {
		t.Fatalf("refresh returned user %d/%s, want %d/%s", userID, userName, user.ID, user.UserName)
	}
	if newAccess == "" || newRefresh == "" || newRefresh == oldRefresh {
		t.Fatal("refresh should rotate both tokens")
	}

	// 旧 refresh token 必须一次性失效（GetDel 语义）。
	if _, err := rc.Get(ctx, cache.RefreshTokenKey(oldRefresh)); !errors.Is(err, cache.ErrCacheMiss) {
		t.Fatalf("old refresh token should be deleted, err = %v", err)
	}
	// 新 refresh token 必须可继续使用。
	if _, err := rc.Get(ctx, cache.RefreshTokenKey(newRefresh)); err != nil {
		t.Fatalf("new refresh token should exist: %v", err)
	}
	// 旧 access token 必须进入黑名单，避免刷新后旧令牌仍可访问。
	blacklisted, err := rc.IsTokenInBlacklist(ctx, oldAccess)
	if err != nil {
		t.Fatalf("check blacklist: %v", err)
	}
	if !blacklisted {
		t.Fatal("old access token should be blacklisted after refresh")
	}
	// 用户级索引只保留新 token。
	isMember, err := rc.IsMemberOfSet(ctx, cache.RefreshSetKey(user.ID), oldRefresh)
	if err != nil {
		t.Fatalf("check old set member: %v", err)
	}
	if isMember {
		t.Fatal("old refresh token should be removed from user refresh set")
	}
	isMember, err = rc.IsMemberOfSet(ctx, cache.RefreshSetKey(user.ID), newRefresh)
	if err != nil {
		t.Fatalf("check new set member: %v", err)
	}
	if !isMember {
		t.Fatal("new refresh token should be added to user refresh set")
	}

	claims, err := jwt.ParseToken(newAccess)
	if err != nil {
		t.Fatalf("parse new access token: %v", err)
	}
	if claims.UserID != user.ID {
		t.Fatalf("new access token user id = %d, want %d", claims.UserID, user.ID)
	}

	// 旧 refresh token 重放必须失败。
	if _, _, _, _, err := s.RefreshToken(ctx, oldRefresh, ""); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("replay old refresh token error = %v, want ErrInvalidRefreshToken", err)
	}
	// 重放检测会撤销该用户全部 refresh token，包括刚刚签发的新 token。
	if _, err := rc.Get(ctx, cache.RefreshTokenKey(newRefresh)); !errors.Is(err, cache.ErrCacheMiss) {
		t.Fatalf("new refresh token should be revoked after replay detection, err = %v", err)
	}
}

func TestRefreshTokenConcurrentOnlyOneSucceeds(t *testing.T) {
	s, _, _ := setupUserService(t)
	ctx := context.Background()
	mustRegister(t, s, "erin_01", testUserPassword)
	_, refreshToken := mustLogin(t, s, "erin_01", testUserPassword)

	const workers = 8
	start := make(chan struct{})
	var (
		wg       sync.WaitGroup
		success  atomic.Int32
		failures atomic.Int32
		newToken atomic.Value
	)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, newRefresh, _, _, err := s.RefreshToken(ctx, refreshToken, "")
			if err == nil {
				success.Add(1)
				newToken.Store(newRefresh)
				return
			}
			if errors.Is(err, ErrInvalidRefreshToken) {
				failures.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	// GetDel 保证并发刷新只有一个请求能消费旧 token。
	if success.Load() != 1 {
		t.Fatalf("concurrent refresh success = %d, want 1", success.Load())
	}
	if failures.Load() != workers-1 {
		t.Fatalf("concurrent refresh failures = %d, want %d", failures.Load(), workers-1)
	}
	if token, _ := newToken.Load().(string); token == "" {
		t.Fatal("winner should receive a new refresh token")
	}
}

func TestRefreshTokenRejectsMissingOrInvalidToken(t *testing.T) {
	s, rc, _ := setupUserService(t)
	ctx := context.Background()

	if _, _, _, _, err := s.RefreshToken(ctx, "", ""); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("empty token error = %v, want ErrInvalidRefreshToken", err)
	}
	if _, _, _, _, err := s.RefreshToken(ctx, "not-exists", ""); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("unknown token error = %v, want ErrInvalidRefreshToken", err)
	}

	// Redis 中的值被污染时必须按无效 token 处理，并从 Redis 删除。
	if err := rc.Set(ctx, cache.RefreshTokenKey("corrupt"), "not-a-number", time.Minute); err != nil {
		t.Fatalf("seed corrupt token: %v", err)
	}
	if _, _, _, _, err := s.RefreshToken(ctx, "corrupt", ""); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("corrupt token error = %v, want ErrInvalidRefreshToken", err)
	}
	if _, err := rc.Get(ctx, cache.RefreshTokenKey("corrupt")); !errors.Is(err, cache.ErrCacheMiss) {
		t.Fatalf("corrupt token should be consumed, err = %v", err)
	}

	// Redis 不可用时必须返回服务不可用，不能误报成 401。
	rc.Disable()
	if _, _, _, _, err := s.RefreshToken(ctx, "any", ""); !errors.Is(err, ErrRefreshTokenUnavailable) {
		t.Fatalf("cache disabled error = %v, want ErrRefreshTokenUnavailable", err)
	}
}

func TestRefreshTokenIgnoresMalformedOldAccessToken(t *testing.T) {
	s, rc, _ := setupUserService(t)
	ctx := context.Background()
	mustRegister(t, s, "frank_01", testUserPassword)
	_, refreshToken := mustLogin(t, s, "frank_01", testUserPassword)

	// old_access_token 是可选字段：前端在 access token 已过期时无法回传，
	// 解析失败只能忽略，不能阻断刷新主流程。
	newAccess, newRefresh, _, _, err := s.RefreshToken(ctx, refreshToken, "not-a-jwt")
	if err != nil {
		t.Fatalf("refresh with malformed old access token: %v", err)
	}
	if newAccess == "" || newRefresh == "" {
		t.Fatal("refresh should still return new tokens")
	}
	if _, err := rc.Get(ctx, cache.RefreshTokenKey(newRefresh)); err != nil {
		t.Fatalf("new refresh token should be persisted: %v", err)
	}
}

func TestLogoutRevokesRefreshAndAccessToken(t *testing.T) {
	s, rc, _ := setupUserService(t)
	ctx := context.Background()
	user := mustRegister(t, s, "grace_01", testUserPassword)
	accessToken, refreshToken := mustLogin(t, s, "grace_01", testUserPassword)

	if err := s.Logout(ctx, user.ID, refreshToken, accessToken, 3600); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := rc.Get(ctx, cache.RefreshTokenKey(refreshToken)); !errors.Is(err, cache.ErrCacheMiss) {
		t.Fatalf("refresh token should be deleted on logout, err = %v", err)
	}
	blacklisted, err := rc.IsTokenInBlacklist(ctx, accessToken)
	if err != nil {
		t.Fatalf("check blacklist: %v", err)
	}
	if !blacklisted {
		t.Fatal("access token should be blacklisted on logout")
	}
	isMember, err := rc.IsMemberOfSet(ctx, cache.RefreshSetKey(user.ID), refreshToken)
	if err != nil {
		t.Fatalf("check refresh set: %v", err)
	}
	if isMember {
		t.Fatal("refresh token should be removed from user refresh set on logout")
	}
}

func TestLogoutRejectsTokenOfAnotherUser(t *testing.T) {
	s, rc, _ := setupUserService(t)
	ctx := context.Background()

	userA := mustRegister(t, s, "henry_01", testUserPassword)
	userB := mustRegister(t, s, "henry_02", testUserPassword)
	accessA, _ := mustLogin(t, s, "henry_01", testUserPassword)
	_, refreshB := mustLogin(t, s, "henry_02", testUserPassword)

	// 必须校验 access token 所属用户与 refresh token 的归属一致，防止越权注销他人会话。
	err := s.Logout(ctx, userA.ID, refreshB, accessA, 3600)
	if !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("cross-user logout error = %v, want ErrInvalidRefreshToken", err)
	}
	if _, err := rc.Get(ctx, cache.RefreshTokenKey(refreshB)); err != nil {
		t.Fatalf("victim refresh token should not be deleted: %v", err)
	}
	blacklisted, err := rc.IsTokenInBlacklist(ctx, accessA)
	if err != nil {
		t.Fatalf("check blacklist: %v", err)
	}
	if blacklisted {
		t.Fatal("failed logout should not blacklist requester access token")
	}
	_ = userB
}

func TestLogoutWithNonPositiveRemainingExpire(t *testing.T) {
	s, rc, _ := setupUserService(t)
	ctx := context.Background()
	user := mustRegister(t, s, "ivy_01", testUserPassword)
	accessToken, refreshToken := mustLogin(t, s, "ivy_01", testUserPassword)

	// access token 已过期时无需再写黑名单，但 refresh token 仍必须撤销。
	if err := s.Logout(ctx, user.ID, refreshToken, accessToken, 0); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := rc.Get(ctx, cache.RefreshTokenKey(refreshToken)); !errors.Is(err, cache.ErrCacheMiss) {
		t.Fatalf("refresh token should be deleted, err = %v", err)
	}
	blacklisted, err := rc.IsTokenInBlacklist(ctx, accessToken)
	if err != nil {
		t.Fatalf("check blacklist: %v", err)
	}
	if blacklisted {
		t.Fatal("expired access token should not be blacklisted")
	}
}

func TestLogoutWithCacheDisabledIsNoop(t *testing.T) {
	s, rc, _ := setupUserService(t)
	rc.Disable()

	// 缓存禁用时无法撤销 refresh token，登出按降级语义返回成功。
	if err := s.Logout(context.Background(), 1, "rt", "at", 3600); err != nil {
		t.Fatalf("logout with cache disabled: %v", err)
	}
}

func TestChangePasswordRotatesHashAndRevokesTokens(t *testing.T) {
	s, rc, _ := setupUserService(t)
	ctx := context.Background()
	user := mustRegister(t, s, "jack_01", testUserPassword)
	accessToken, refreshToken := mustLogin(t, s, "jack_01", testUserPassword)

	if err := s.ChangePassword(ctx, user.ID, testUserPassword, testNewPassword, accessToken, 3600); err != nil {
		t.Fatalf("change password: %v", err)
	}

	// 旧密码必须立即失效，新密码必须可用。
	if _, _, _, _, err := s.Login(ctx, "jack_01", testUserPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password login error = %v, want ErrInvalidCredentials", err)
	}
	if _, _, _, _, err := s.Login(ctx, "jack_01", testNewPassword); err != nil {
		t.Fatalf("new password login: %v", err)
	}

	// 改密后所有旧 refresh token 必须被清理，当前 access token 必须拉黑。
	if _, err := rc.Get(ctx, cache.RefreshTokenKey(refreshToken)); !errors.Is(err, cache.ErrCacheMiss) {
		t.Fatalf("old refresh token should be revoked, err = %v", err)
	}
	blacklisted, err := rc.IsTokenInBlacklist(ctx, accessToken)
	if err != nil {
		t.Fatalf("check blacklist: %v", err)
	}
	if !blacklisted {
		t.Fatal("current access token should be blacklisted after password change")
	}
}

func TestChangePasswordRejectsInvalidInput(t *testing.T) {
	s, _, db := setupUserService(t)
	ctx := context.Background()
	user := mustRegister(t, s, "kate_01", testUserPassword)

	// 旧密码错误。
	if err := s.ChangePassword(ctx, user.ID, "wrong-pass", testNewPassword, "at", 3600); !errors.Is(err, ErrOldPasswordIncorrect) {
		t.Fatalf("wrong old password error = %v, want ErrOldPasswordIncorrect", err)
	}
	// 新密码校验必须在查库/改库之前完成。
	for _, tt := range []struct {
		name     string
		password string
		wantErr  error
	}{
		{name: "too short", password: "a1", wantErr: ErrPasswordTooShort},
		{name: "weak", password: "abcdefgh", wantErr: ErrPasswordWeak},
		{name: "too long", password: strings.Repeat("a", 72) + "1", wantErr: ErrPasswordTooLong},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.ChangePassword(ctx, user.ID, testUserPassword, tt.password, "at", 3600); !errors.Is(err, tt.wantErr) {
				t.Fatalf("change password error = %v, want %v", err, tt.wantErr)
			}
		})
	}

	// 失败的修改不能影响数据库中的原密码。
	var stored model.User
	if err := db.First(&stored, user.ID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored.Password), []byte(testUserPassword)); err != nil {
		t.Fatalf("original password should remain valid: %v", err)
	}
}

func TestChangePasswordWithCacheDisabledStillUpdatesPassword(t *testing.T) {
	s, rc, _ := setupUserService(t)
	ctx := context.Background()
	user := mustRegister(t, s, "lily_01", testUserPassword)
	rc.Disable()

	if err := s.ChangePassword(ctx, user.ID, testUserPassword, testNewPassword, "at", 3600); err != nil {
		t.Fatalf("change password with cache disabled: %v", err)
	}
	if _, _, _, _, err := s.Login(ctx, "lily_01", testNewPassword); err != nil {
		t.Fatalf("login with new password: %v", err)
	}
}

func TestUpdateProfile(t *testing.T) {
	s, _, _ := setupUserService(t)
	ctx := context.Background()
	user := mustRegister(t, s, "mike_01", testUserPassword)
	other := mustRegister(t, s, "mike_02", testUserPassword)

	newName := "mike_renamed"
	newAvatar := "/upload/avatars/1/avatar.png"
	newBio := "new bio"
	profile, err := s.UpdateProfile(ctx, user.ID, dto.UpdateProfileRequest{
		UserName:  &newName,
		AvatarURL: &newAvatar,
		Bio:       &newBio,
	})
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if profile.Username != newName || profile.AvatarURL != newAvatar || profile.Bio != newBio {
		t.Fatalf("updated profile = %+v", profile)
	}

	// 用户名冲突。
	if _, err := s.UpdateProfile(ctx, user.ID, dto.UpdateProfileRequest{UserName: &other.UserName}); !errors.Is(err, ErrUsernameExists) {
		t.Fatalf("duplicate username error = %v, want ErrUsernameExists", err)
	}

	// 只允许本服务上传目录，拒绝外部 URL。
	external := "https://evil.example.com/a.png"
	if _, err := s.UpdateProfile(ctx, user.ID, dto.UpdateProfileRequest{AvatarURL: &external}); !errors.Is(err, ErrInvalidAvatarURL) {
		t.Fatalf("external avatar error = %v, want ErrInvalidAvatarURL", err)
	}

	// 空更新。
	if _, err := s.UpdateProfile(ctx, user.ID, dto.UpdateProfileRequest{}); !errors.Is(err, ErrNoFieldsToUpdate) {
		t.Fatalf("empty update error = %v, want ErrNoFieldsToUpdate", err)
	}

	// 非法用户名。
	badName := "a"
	if _, err := s.UpdateProfile(ctx, user.ID, dto.UpdateProfileRequest{UserName: &badName}); !errors.Is(err, ErrUsernameInvalid) {
		t.Fatalf("invalid username error = %v, want ErrUsernameInvalid", err)
	}

	// 零值用户 ID 必须拒绝，避免更新到其他用户。
	if _, err := s.UpdateProfile(ctx, 0, dto.UpdateProfileRequest{Bio: &newBio}); err == nil {
		t.Fatal("zero user id should be rejected")
	}
}

func TestFindByIDAndUsername(t *testing.T) {
	s, _, _ := setupUserService(t)
	ctx := context.Background()
	user := mustRegister(t, s, "nancy_01", testUserPassword)

	byID, err := s.FindByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("find by id: %v", err)
	}
	if byID.ID != user.ID || byID.Username != user.UserName {
		t.Fatalf("find by id = %+v", byID)
	}

	byName, err := s.FindByUsername(ctx, user.UserName)
	if err != nil {
		t.Fatalf("find by username: %v", err)
	}
	if byName.ID != user.ID {
		t.Fatalf("find by username id = %d, want %d", byName.ID, user.ID)
	}

	if _, err := s.FindByID(ctx, user.ID+1000); err == nil {
		t.Fatal("missing user should return error")
	}
	if _, err := s.FindByUsername(ctx, "missing_user"); err == nil {
		t.Fatal("missing username should return error")
	}
	if _, err := s.FindByUsername(ctx, "   "); err == nil {
		t.Fatal("blank username should return error")
	}
}

func TestBatchGetUserNames(t *testing.T) {
	s, _, _ := setupUserService(t)
	ctx := context.Background()

	empty, err := s.BatchGetUserNames(ctx, nil)
	if err != nil {
		t.Fatalf("batch get empty: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("batch get empty len = %d, want 0", len(empty))
	}

	u1 := mustRegister(t, s, "olivia_01", testUserPassword)
	u2 := mustRegister(t, s, "olivia_02", testUserPassword)

	names, err := s.BatchGetUserNames(ctx, []uint{u1.ID, u2.ID, u1.ID + 1000})
	if err != nil {
		t.Fatalf("batch get names: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("batch get names len = %d, want 2", len(names))
	}
	if names[u1.ID] != u1.UserName || names[u2.ID] != u2.UserName {
		t.Fatalf("batch names = %+v", names)
	}
}
