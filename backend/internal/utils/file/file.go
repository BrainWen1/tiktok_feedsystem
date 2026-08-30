package file

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/gin-gonic/gin"
)

// RandHex 生成长度为 n 的十六进制随机字符串（生成头像文件名）
func RandHex(n int) (string, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// BuildAbsoluteURL 拼接完整可访问的 http 绝对地址
func BuildAbsoluteURL(ctx *gin.Context, relativePath string) string {
	scheme := "http"
	if ctx.Request.TLS != nil {
		scheme = "https"
	}
	host := ctx.Request.Host
	return fmt.Sprintf("%s://%s%s", scheme, host, relativePath)
}
