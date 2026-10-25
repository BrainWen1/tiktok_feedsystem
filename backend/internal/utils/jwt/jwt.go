package jwt

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"feedsystem/internal/config"
	"fmt"
	"strings"
	"time"

	"feedsystem/internal/utils/logger"

	"github.com/gin-gonic/gin"
	JWT "github.com/golang-jwt/jwt/v5"
)

var (
	// ErrInvalidSigningMethod 表示 token 使用了非预期的签名算法，拒绝解析以防止算法混淆攻击
	ErrInvalidSigningMethod = errors.New("unexpected jwt signing method")
	// ErrJWTNotConfigured 表示 JWT 密钥或过期时间配置缺失，由上层转换为业务错误
	ErrJWTNotConfigured = errors.New("jwt is not configured")
)

// 返回JWT密钥和设置的过期时间；配置缺失时返回error，禁止在请求链路中退出进程
func jwtSecret() ([]byte, int, error) {
	key := config.AppConfig.JWT_secret
	expireHour := config.AppConfig.JWT_expire_hour
	if len(key) == 0 {
		return nil, 0, fmt.Errorf("%w: JWT_SECRET is empty", ErrJWTNotConfigured)
	}
	if expireHour == 0 {
		return nil, 0, fmt.Errorf("%w: JWT_EXPIRE_HOUR is not set", ErrJWTNotConfigured)
	}
	return []byte(key), expireHour, nil
}

// validateSigningMethod 校验 token 头部算法与实际的签名方法，只允许 HS256
func validateSigningMethod(token *JWT.Token) error {
	if token == nil {
		return errors.New("invalid token")
	}
	alg, ok := token.Header["alg"].(string)
	if !ok || alg != JWT.SigningMethodHS256.Alg() {
		return fmt.Errorf("%w: alg=%v", ErrInvalidSigningMethod, token.Header["alg"])
	}
	if token.Method != JWT.SigningMethodHS256 {
		return fmt.Errorf("%w: method=%v", ErrInvalidSigningMethod, token.Method)
	}
	return nil
}

// Claims 结构体用于存储 JWT 的自定义声明
type Claims struct {
	UserID   uint   `json:"user_id"`
	UserName string `json:"user_name"`
	JWT.RegisteredClaims
}

// GenerateToken 生成 JWT token
func GenerateToken(userID uint, userName string) (string, error) {
	jwtSecretKey, expiration, err := jwtSecret() // 获取JWT密钥和过期时间
	if err != nil {
		return "", fmt.Errorf("generate access token: %w", err)
	}
	// 创建自定义声明
	claims := Claims{
		UserID:   userID,
		UserName: userName,
		RegisteredClaims: JWT.RegisteredClaims{
			Issuer:    "feedsystem",                                                              // 签发者
			IssuedAt:  JWT.NewNumericDate(time.Now()),                                            // 签发时间
			ExpiresAt: JWT.NewNumericDate(time.Now().Add(time.Hour * time.Duration(expiration))), // 过期时间
		},
	}
	// 创建 token
	token := JWT.NewWithClaims(JWT.SigningMethodHS256, claims)
	// 签名
	return token.SignedString(jwtSecretKey)
}

// ParseToken 解析和验证JWT令牌
func ParseToken(tokenString string) (*Claims, error) {
	// 解析令牌，仅允许 HS256，并在 keyfunc 中再次校验算法头部
	token, err := JWT.ParseWithClaims(tokenString, &Claims{}, func(token *JWT.Token) (interface{}, error) {
		if err := validateSigningMethod(token); err != nil {
			return nil, err
		}
		jwtSecretKey, _, err := jwtSecret()
		if err != nil {
			return nil, err
		}
		return jwtSecretKey, nil // 使用配置中的JWT密钥进行验证
	}, JWT.WithValidMethods([]string{JWT.SigningMethodHS256.Alg()}))
	if err != nil {
		return nil, fmt.Errorf("parse access token: %w", err)
	}

	// 验证令牌有效性
	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}
	return nil, errors.New("invalid token")
}

// GenerateRefreshToken 生成刷新令牌
func GenerateRefreshToken(userID uint) (string, error) {
	// 生成一个随机的刷新令牌
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// 将32位随机字节转换为十六进制字符串
	return hex.EncodeToString(b), nil
}

// ExtractBearerToken 从请求中提取 access token
func ExtractBearerToken(ctx *gin.Context) (string, error) {
	// 从请求头中获取 Authorization 字段
	authHeader := ctx.GetHeader("Authorization")
	if authHeader == "" {
		logger.Debug("authorization header is empty")
		return "", errors.New("authorization header is empty")
	}
	// 检查 Authorization 字段的格式是否正确并获取token，通常是 "Bearer <token>"
	parts := strings.SplitN(authHeader, " ", 2)
	if !(len(parts) == 2 && parts[0] == "Bearer") { // parts[0]:"Bearer"  parts[1]:token
		logger.Warn("authorization header format is invalid")
		return "", errors.New("authorization header format is invalid")
	}
	return parts[1], nil
}

// GetTokenRemainingExpire 获取 token 剩余有效期（秒）
func GetTokenRemainingExpire(tokenStr string) (int64, error) {
	// 解析 token 并获取 claims
	token, err := JWT.Parse(tokenStr, func(token *JWT.Token) (interface{}, error) {
		if err := validateSigningMethod(token); err != nil {
			return nil, err
		}
		jwtSecretKey, _, err := jwtSecret()
		if err != nil {
			return nil, err
		}
		return jwtSecretKey, nil
	}, JWT.WithValidMethods([]string{JWT.SigningMethodHS256.Alg()}))
	if err != nil {
		logger.Warn("parse token failed", logger.Err(err))
		return 0, fmt.Errorf("parse token: %w", err)
	}
	// 检查 token 是否有效
	if !token.Valid {
		logger.Warn("invalid token")
		return 0, errors.New("invalid token")
	}
	// 断言 claims 类型
	claims, ok := token.Claims.(JWT.MapClaims)
	if !ok {
		logger.Warn("assert token claims failed")
		return 0, errors.New("invalid token claims")
	}
	// 获取 exp 字段并计算剩余时间；先做类型断言，缺失或非法时返回错误而不是panic
	expValue, exists := claims["exp"]
	if !exists {
		return 0, errors.New("token exp is missing")
	}
	expFloat, ok := expValue.(float64)
	if !ok {
		return 0, fmt.Errorf("invalid token exp type: %T", expValue)
	}
	exp := int64(expFloat)
	now := time.Now().Unix()
	remain := exp - now
	// 如果剩余时间小于0，说明token已经过期，返回错误
	if remain < 0 {
		logger.Debug("token has expired", logger.Int64("exp", exp), logger.Int64("now", now))
		return 0, errors.New("token has expired")
	}
	return remain, nil
}
