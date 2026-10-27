package logger

import (
	"context"

	"feedsystem/internal/utils/requestid"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// requestIDField 日志中 requestId 的统一字段名，便于日志系统检索。
const requestIDField = "request_id"

// Logger 是带请求上下文的日志包装。
//
// 业务层通过 logger.Ctx(ctx) 获取该实例后，调用 Info/Debug/Warn/Error 时
// 会自动从 ctx 读取 requestId 并追加为 request_id 字段，无需手动传字段；
// ctx 中没有 requestId（如启动阶段、后台任务）时退化为普通日志。
type Logger struct {
	base      *zap.Logger
	requestID string
}

// Ctx 返回与 ctx 绑定的请求级日志实例。
// 每次请求内可重复调用，底层 requestId 只做一次 context 取值，不改变原有日志格式。
func Ctx(ctx context.Context) *Logger {
	return &Logger{
		base:      L(),
		requestID: requestid.FromContext(ctx),
	}
}

// withRequestID 返回追加了 request_id 字段的底层 logger；
// 没有 requestId 时直接复用全局 logger，避免无意义的 With 拷贝。
func (l *Logger) withRequestID() *zap.Logger {
	if l.requestID == "" {
		return l.base
	}
	return l.base.With(String(requestIDField, l.requestID))
}

// Debug 输出带 request_id 的调试日志。
func (l *Logger) Debug(msg string, fields ...Field) {
	// 级别未开启时提前返回，避免高频 Debug 调用产生无用的 With 拷贝
	if !l.base.Core().Enabled(zapcore.DebugLevel) {
		return
	}
	l.withRequestID().Debug(msg, fields...)
}

// Info 输出带 request_id 的常规运行日志。
func (l *Logger) Info(msg string, fields ...Field) {
	if !l.base.Core().Enabled(zapcore.InfoLevel) {
		return
	}
	l.withRequestID().Info(msg, fields...)
}

// Warn 输出带 request_id 的告警日志。
func (l *Logger) Warn(msg string, fields ...Field) {
	if !l.base.Core().Enabled(zapcore.WarnLevel) {
		return
	}
	l.withRequestID().Warn(msg, fields...)
}

// Error 输出带 request_id 的错误日志。
func (l *Logger) Error(msg string, fields ...Field) {
	if !l.base.Core().Enabled(zapcore.ErrorLevel) {
		return
	}
	l.withRequestID().Error(msg, fields...)
}

// Fatal 输出带 request_id 的致命日志后退出进程。
func (l *Logger) Fatal(msg string, fields ...Field) {
	l.withRequestID().Fatal(msg, fields...)
}
