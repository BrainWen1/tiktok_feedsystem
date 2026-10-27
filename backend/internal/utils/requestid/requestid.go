// Package requestid 负责生成与传递全链路追踪ID。
//
// requestId 在 HTTP 入口由中间件生成（或复用上游 X-Request-Id），
// 写入 context.Context 后，后续 handler/service/repo 只需透传 ctx，
// 即可通过 logger.Ctx(ctx) 自动带上 request_id，无需业务层手动拼字段。
package requestid

import (
	"context"

	"github.com/google/uuid"
)

// Header 全链路追踪ID的 HTTP 头名称，请求与响应统一使用该头。
const Header = "X-Request-Id"

// contextKey 私有类型避免与其它包的 context key 冲突。
type contextKey struct{}

// New 生成一个 UUID v4 格式的 requestId。
func New() string {
	return uuid.NewString()
}

// WithContext 把 requestId 写入 context，供下游日志与业务透传。
func WithContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// FromContext 从 context 读取 requestId；不存在或类型不符时返回空字符串。
func FromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}
