package jwt

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"feedsystem/internal/config"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	JWT "github.com/golang-jwt/jwt/v5"
)

// 返回JWT密钥和设置的过期时间
func jwtSecret() ([]byte, int) {
	key := config.AppConfig.JWT_secret
	expireHour := config.AppConfig.JWT_expire_hour
	if len(key) == 0 {
		log.Fatal("JWT 密钥为空，请在环境变量中设置 JWT_SECRET")
	}
	if expireHour == 0 {
		log.Fatal("JWT 过期时间未设置，请在环境变量中设置 JWT_EXPIRE_HOUR")
	}
	return []byte(key), expireHour
}

// Claims 结构体用于存储 JWT 的自定义声明
type Claims struct {
	UserID   uint   `json:"user_id"`
	UserName string `json:"user_name"`
	JWT.RegisteredClaims
}

// GenerateToken 生成 JWT token
func GenerateToken(userID uint, userName string) (string, error) {
	jwtSecretKey, expiration := jwtSecret() // 获取JWT密钥和过期时间
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
	token := JWT.NewWithClaims(jwt.SigningMethodHS256, claims)
	// 签名
	return token.SignedString(jwtSecretKey)
}

// ParseToken 解析和验证JWT令牌
func ParseToken(tokenString string) (*Claims, error) {
	// 解析令牌
	token, err := JWT.ParseWithClaims(tokenString, &Claims{}, func(token *JWT.Token) (interface{}, error) {
		jwtSecretKey, _ := jwtSecret()
		return jwtSecretKey, nil // 使用配置中的JWT密钥进行验证
	})
	if err != nil {
		return nil, err
	}

	// 验证令牌有效性
	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}
	return nil, errors.New("invalid token")
}

// GenerateRefreshToken 生成刷新令牌
func GenerateRefreshToken(user_id uint) (string, error) {
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
		log.Printf("Authorization header is empty")
		return "", errors.New("authorization header is empty")
	}
	// 检查 Authorization 字段的格式是否正确并获取token，通常是 "Bearer <token>"
	parts := strings.SplitN(authHeader, " ", 2)
	if !(len(parts) == 2 && parts[0] == "Bearer") { // parts[0]:"Bearer"  parts[1]:token
		log.Printf("Authorization header format is invalid: %s", authHeader)
		return "", errors.New("authorization header format is invalid")
	}
	return parts[1], nil
}

// GetTokenRemainingExpire 获取 token 剩余有效期（秒）
func GetTokenRemainingExpire(tokenStr string) (int64, error) {
	// 解析 token 并获取 claims
	token, err := JWT.Parse(tokenStr, func(token *JWT.Token) (interface{}, error) {
		jwtSecretKey, _ := jwtSecret()
		return jwtSecretKey, nil
	})
	if err != nil {
		log.Printf("Error parsing token: %v", err)
		return 0, err
	}
	// 检查 token 是否有效
	if !token.Valid {
		log.Printf("Invalid token: %v", err)
		return 0, errors.New("invalid token")
	}
	// 断言 claims 类型
	claims, ok := token.Claims.(JWT.MapClaims)
	if !ok {
		log.Printf("Error asserting token claims: %v", err)
		return 0, errors.New("invalid token claims")
	}
	// 获取 exp 字段并计算剩余时间
	exp := int64(claims["exp"].(float64))
	now := time.Now().Unix()
	remain := exp - now
	// 如果剩余时间小于0，说明token已经过期，返回错误
	if remain < 0 {
		log.Printf("Token has expired: exp=%d, now=%d", exp, now)
		return 0, errors.New("token has expired")
	}
	return remain, nil
}
