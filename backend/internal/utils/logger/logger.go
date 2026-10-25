// Package logger 提供项目统一的结构化日志入口。
//
// 所有业务代码必须通过本包打印日志，禁止直接使用标准库 log。
// 日志按 Debug/Info/Warn/Error/Fatal 分级，敏感字段（password、access_token、
// refresh_token 等）在写入前由 redactCore 自动脱敏。
package logger

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Field 是结构化日志字段，避免调用方直接依赖 zap。
type Field = zap.Field

// redactedValue 是敏感字段统一替换值。
const redactedValue = "[REDACTED]"

// global 保存当前生效的 logger，允许 Init 在启动阶段原子替换。
var global atomic.Pointer[zap.Logger]

func init() {
	// 未显式 Init 时提供可用的兜底 logger，避免任何早期日志丢失。
	global.Store(newLogger(zapcore.InfoLevel))
}

// Init 初始化全局 logger。level 为空时默认 info，可选 debug/info/warn/error。
// 必须在启动 goroutine 之前调用，确保后台日志使用同一实例。
func Init(level string) error {
	lvl, err := parseLevel(level)
	if err != nil {
		return err
	}
	global.Store(newLogger(lvl))
	return nil
}

// Sync 刷新缓冲区，进程退出前调用；stdout 同步失败可忽略。
func Sync() {
	_ = global.Load().Sync()
}

// parseLevel 解析日志级别，空字符串回退到 info。
func parseLevel(level string) (zapcore.Level, error) {
	level = strings.TrimSpace(strings.ToLower(level))
	if level == "" {
		return zapcore.InfoLevel, nil
	}
	lvl, err := zapcore.ParseLevel(level)
	if err != nil {
		return zapcore.InfoLevel, fmt.Errorf("parse log level %q: %w", level, err)
	}
	return lvl, nil
}

// newLogger 构建带脱敏 core 的 zap logger。
func newLogger(level zapcore.Level) *zap.Logger {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "ts"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.LowercaseLevelEncoder

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.Lock(os.Stdout),
		zap.NewAtomicLevelAt(level),
	)
	return zap.New(redactCore{Core: core}, zap.AddCaller())
}

// Debug 输出调试日志，用于缓存细节、内部流程等高频信息。
func Debug(msg string, fields ...Field) {
	global.Load().Debug(msg, fields...)
}

// Info 输出常规运行日志，用于启动、连接、生命周期等事件。
func Info(msg string, fields ...Field) {
	global.Load().Info(msg, fields...)
}

// Warn 输出告警日志，用于可降级的异常。
func Warn(msg string, fields ...Field) {
	global.Load().Warn(msg, fields...)
}

// Error 输出错误日志，用于需要人工关注的失败。
func Error(msg string, fields ...Field) {
	global.Load().Error(msg, fields...)
}

// Fatal 输出致命日志后退出进程，语义与 log.Fatal 一致。
func Fatal(msg string, fields ...Field) {
	global.Load().Fatal(msg, fields...)
}

// String 构造字符串字段，敏感 key 会由 redactCore 自动脱敏。
func String(key, value string) Field {
	return zap.String(key, value)
}

// Uint 构造 uint 字段。
func Uint(key string, value uint) Field {
	return zap.Uint(key, value)
}

// Int 构造 int 字段。
func Int(key string, value int) Field {
	return zap.Int(key, value)
}

// Int64 构造 int64 字段。
func Int64(key string, value int64) Field {
	return zap.Int64(key, value)
}

// Uint64 构造 uint64 字段。
func Uint64(key string, value uint64) Field {
	return zap.Uint64(key, value)
}

// Bool 构造 bool 字段。
func Bool(key string, value bool) Field {
	return zap.Bool(key, value)
}

// Duration 构造时长字段。
func Duration(key string, value time.Duration) Field {
	return zap.Duration(key, value)
}

// Any 构造任意类型字段。
func Any(key string, value interface{}) Field {
	return zap.Any(key, value)
}

// Err 构造 error 字段。
func Err(err error) Field {
	return zap.Error(err)
}

// redactCore 在字段写入前屏蔽敏感 key，覆盖 With 继承的字段。
type redactCore struct {
	zapcore.Core
}

// With 对继承字段先脱敏再交给底层 core。
func (c redactCore) With(fields []Field) zapcore.Core {
	return redactCore{Core: c.Core.With(redactFields(fields))}
}

// Write 对本次写入字段脱敏。
func (c redactCore) Write(entry zapcore.Entry, fields []Field) error {
	return c.Core.Write(entry, redactFields(fields))
}

// redactFields 返回脱敏后的字段副本，避免修改调用方切片。
func redactFields(fields []Field) []Field {
	out := make([]Field, len(fields))
	copy(out, fields)
	for i := range out {
		if isSensitiveKey(out[i].Key) {
			out[i] = zap.String(out[i].Key, redactedValue)
		}
	}
	return out
}

// isSensitiveKey 判断字段名是否属于敏感信息，忽略大小写与分隔符。
func isSensitiveKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(key))
	for _, marker := range []string{"password", "passwd", "token", "secret", "authorization", "credential", "dsn"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
