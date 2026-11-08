package jwt

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"feedsystem/internal/config"

	"github.com/gin-gonic/gin"
	JWT "github.com/golang-jwt/jwt/v5"
)

// 使用 .env.example 中的示例 JWT_SECRET，64 位十六进制字符串，256 bit，满足 HS256 的最小要求。
const testJWTSecret = "461973b1420752ca07baad055abab988ac87497fed5cf7225ed989e82df41eb8"

// withJWTConfig 在测试期间覆盖全局 JWT 配置，结束后恢复，避免污染其他测试。
func withJWTConfig(t *testing.T, secret string, expireHour int) {
	t.Helper()
	oldSecret, oldExpire := config.AppConfig.JWT_secret, config.AppConfig.JWT_expire_hour
	config.AppConfig.JWT_secret = secret
	config.AppConfig.JWT_expire_hour = expireHour
	t.Cleanup(func() {
		config.AppConfig.JWT_secret = oldSecret
		config.AppConfig.JWT_expire_hour = oldExpire
	})
}

func TestGenerateAndParseToken(t *testing.T) {
	withJWTConfig(t, testJWTSecret, 2)

	tokenStr, err := GenerateToken(42, "alice")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if tokenStr == "" {
		t.Fatal("GenerateToken returned empty token")
	}

	claims, err := ParseToken(tokenStr)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.UserID != 42 || claims.UserName != "alice" {
		t.Fatalf("claims = %+v, want user 42/alice", claims)
	}
	if claims.Issuer != "feedsystem" {
		t.Fatalf("issuer = %q, want feedsystem", claims.Issuer)
	}
	if claims.ExpiresAt == nil {
		t.Fatal("expires_at should be set")
	}
	remaining := time.Until(claims.ExpiresAt.Time)
	if remaining <= 0 || remaining > 2*time.Hour+time.Minute {
		t.Fatalf("token remaining lifetime = %v, want within (0, 2h]", remaining)
	}

	rest, err := GetTokenRemainingExpire(tokenStr)
	if err != nil {
		t.Fatalf("GetTokenRemainingExpire: %v", err)
	}
	if rest <= 0 || rest > int64(2*time.Hour/time.Second)+60 {
		t.Fatalf("remaining expire = %d seconds, want within (0, 2h]", rest)
	}
}

func TestParseTokenRejectsTamperedToken(t *testing.T) {
	withJWTConfig(t, testJWTSecret, 1)

	tokenStr, err := GenerateToken(1, "alice")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	// 修改签名最后一个字符，签名校验必须失败。
	last := tokenStr[len(tokenStr)-1]
	replacement := byte('A')
	if last == 'A' {
		replacement = 'B'
	}
	tampered := tokenStr[:len(tokenStr)-1] + string(replacement)
	if _, err := ParseToken(tampered); err == nil {
		t.Fatal("tampered token should be rejected")
	}

	if _, err := ParseToken(tokenStr + "x"); err == nil {
		t.Fatal("token with trailing garbage should be rejected")
	}
}

func TestParseTokenRejectsWrongSigningMethod(t *testing.T) {
	withJWTConfig(t, testJWTSecret, 1)

	claims := Claims{
		UserID:   1,
		UserName: "alice",
		RegisteredClaims: JWT.RegisteredClaims{
			Issuer:    "feedsystem",
			ExpiresAt: JWT.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	// 使用同一密钥但 HS512 算法签发，ParseToken 必须只接受 HS256，防止算法混淆。
	token := JWT.NewWithClaims(JWT.SigningMethodHS512, claims)
	tokenStr, err := token.SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatalf("sign HS512 token: %v", err)
	}

	if _, err := ParseToken(tokenStr); err == nil {
		t.Fatal("HS512 token should be rejected")
	}
}

func TestJWTNotConfigured(t *testing.T) {
	withJWTConfig(t, "", 1)
	if _, err := GenerateToken(1, "alice"); !errors.Is(err, ErrJWTNotConfigured) {
		t.Fatalf("GenerateToken error = %v, want ErrJWTNotConfigured", err)
	}

	withJWTConfig(t, testJWTSecret, 0)
	if _, err := GenerateToken(1, "alice"); !errors.Is(err, ErrJWTNotConfigured) {
		t.Fatalf("GenerateToken with zero expire error = %v, want ErrJWTNotConfigured", err)
	}
}

func TestGetTokenRemainingExpireRejectsExpiredAndMissingExp(t *testing.T) {
	withJWTConfig(t, testJWTSecret, 1)

	// 已过期 token。
	expired := JWT.NewWithClaims(JWT.SigningMethodHS256, Claims{
		UserID: 1,
		RegisteredClaims: JWT.RegisteredClaims{
			ExpiresAt: JWT.NewNumericDate(time.Now().Add(-time.Minute)),
		},
	})
	expiredStr, err := expired.SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatalf("sign expired token: %v", err)
	}
	if _, err := GetTokenRemainingExpire(expiredStr); err == nil {
		t.Fatal("expired token should be rejected")
	}

	// 缺少 exp 字段。
	noExp := JWT.NewWithClaims(JWT.SigningMethodHS256, Claims{UserID: 1})
	noExpStr, err := noExp.SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatalf("sign token without exp: %v", err)
	}
	if _, err := GetTokenRemainingExpire(noExpStr); err == nil {
		t.Fatal("token without exp should be rejected")
	}

	if _, err := GetTokenRemainingExpire("not-a-token"); err == nil {
		t.Fatal("malformed token should be rejected")
	}
}

func TestExtractBearerToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newCtx := func(header string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			c.Request.Header.Set("Authorization", header)
		}
		return c
	}

	tests := []struct {
		name    string
		header  string
		want    string
		wantErr bool
	}{
		{name: "valid", header: "Bearer abc.def.ghi", want: "abc.def.ghi"},
		{name: "missing header", header: "", wantErr: true},
		{name: "wrong scheme", header: "Token abc", wantErr: true},
		{name: "empty header", header: "Bearer", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractBearerToken(newCtx(tt.header))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ExtractBearerToken(%q) error = nil, want error", tt.header)
				}
				return
			}
			if err != nil {
				t.Fatalf("ExtractBearerToken(%q) error = %v", tt.header, err)
			}
			if got != tt.want {
				t.Fatalf("ExtractBearerToken(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

func TestGenerateRefreshToken(t *testing.T) {
	first, err := GenerateRefreshToken(1)
	if err != nil {
		t.Fatalf("GenerateRefreshToken: %v", err)
	}
	second, err := GenerateRefreshToken(1)
	if err != nil {
		t.Fatalf("GenerateRefreshToken: %v", err)
	}
	if len(first) != 64 {
		t.Fatalf("refresh token length = %d, want 64 hex chars", len(first))
	}
	if strings.ContainsFunc(first, func(r rune) bool {
		return !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f'))
	}) {
		t.Fatalf("refresh token is not lowercase hex: %q", first)
	}
	if first == second {
		t.Fatal("refresh tokens must be unique")
	}
}
