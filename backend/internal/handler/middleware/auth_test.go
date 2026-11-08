package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"feedsystem/internal/config"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/utils/jwt"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
)

const authTestSecret = "auth-middleware-test-secret-0123456789"

// setupAuthRouter 构造带 Auth 中间件的最小路由，并返回 Redis 缓存用于黑名单断言。
func setupAuthRouter(t *testing.T) (*gin.Engine, *cache.RedisCache) {
	t.Helper()

	oldSecret, oldExpire := config.AppConfig.JWT_secret, config.AppConfig.JWT_expire_hour
	config.AppConfig.JWT_secret = authTestSecret
	config.AppConfig.JWT_expire_hour = 1
	t.Cleanup(func() {
		config.AppConfig.JWT_secret = oldSecret
		config.AppConfig.JWT_expire_hour = oldExpire
	})

	mr := miniredis.RunT(t)
	redisCache := cache.NewRedisCache(mr.Addr(), "", 0)
	t.Cleanup(func() { _ = redisCache.Close() })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	am := NewAuthMiddleware(redisCache)
	r.GET("/protected", am.Auth(), func(c *gin.Context) {
		userID, _ := c.Get("userID")
		username, _ := c.Get("username")
		c.JSON(http.StatusOK, gin.H{
			"code":     http.StatusOK,
			"user_id":  userID,
			"username": username,
		})
	})
	return r, redisCache
}

func doAuthRequest(r *gin.Engine, authHeader string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	r.ServeHTTP(w, req)
	return w
}

func decodeCode(t *testing.T, w *httptest.ResponseRecorder) int {
	t.Helper()
	var body struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return body.Code
}

func TestAuthMiddlewareRejectsMissingOrMalformedHeader(t *testing.T) {
	r, _ := setupAuthRouter(t)

	tests := []struct {
		name   string
		header string
	}{
		{name: "missing header", header: ""},
		{name: "wrong scheme", header: "Token abc"},
		{name: "no token", header: "Bearer"},
		{name: "invalid token", header: "Bearer not-a-jwt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := doAuthRequest(r, tt.header)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", w.Code)
			}
			if code := decodeCode(t, w); code != http.StatusUnauthorized {
				t.Fatalf("business code = %d, want 401", code)
			}
		})
	}
}

func TestAuthMiddlewareAcceptsValidTokenAndInjectsClaims(t *testing.T) {
	r, _ := setupAuthRouter(t)

	token, err := jwt.GenerateToken(42, "alice")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	w := doAuthRequest(r, "Bearer "+token)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var body struct {
		Code     int    `json:"code"`
		UserID   uint   `json:"user_id"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.UserID != 42 || body.Username != "alice" {
		t.Fatalf("injected claims = %d/%s, want 42/alice", body.UserID, body.Username)
	}
}

func TestAuthMiddlewareRejectsBlacklistedToken(t *testing.T) {
	r, redisCache := setupAuthRouter(t)

	token, err := jwt.GenerateToken(7, "bob")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	// 模拟登出/改密后将 access token 加入黑名单。
	if err := redisCache.AddTokenToBlacklist(context.Background(), token, time.Minute); err != nil {
		t.Fatalf("AddTokenToBlacklist: %v", err)
	}

	w := doAuthRequest(r, "Bearer "+token)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s, want 401", w.Code, w.Body.String())
	}
	if code := decodeCode(t, w); code != http.StatusUnauthorized {
		t.Fatalf("business code = %d, want 401", code)
	}
}

func TestAuthMiddlewareWithDisabledCacheStillValidatesSignature(t *testing.T) {
	r, redisCache := setupAuthRouter(t)
	redisCache.Disable()

	token, err := jwt.GenerateToken(9, "carol")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	// 缓存禁用时黑名单不可用，但 JWT 签名校验仍必须生效。
	if w := doAuthRequest(r, "Bearer "+token); w.Code != http.StatusOK {
		t.Fatalf("valid token status = %d, want 200", w.Code)
	}
	if w := doAuthRequest(r, "Bearer invalid"); w.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d, want 401", w.Code)
	}
}
