package jwt

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"feedsystem/internal/config"
	"log"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// 返回JWT密钥和过期时间
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
	UserID   string `json:"user_id"`
	UserName string `json:"user_name"`
	jwt.RegisteredClaims
}

// GenerateToken 生成 JWT token
func GenerateToken(userID, userName string) (string, error) {
	jwtSecretKey, expiration := jwtSecret() // 获取JWT密钥和过期时间
	// 创建自定义声明
	claims := Claims{
		UserID:   userID,
		UserName: userName,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "feedsystem",                                                              // 签发者
			IssuedAt:  jwt.NewNumericDate(time.Now()),                                            // 签发时间
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour * time.Duration(expiration))), // 过期时间
		},
	}
	// 创建 token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	// 签名
	return token.SignedString(jwtSecretKey)
}

// ParseToken 解析和验证JWT令牌
func ParseToken(tokenString string) (*Claims, error) {
	// 解析令牌
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
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
