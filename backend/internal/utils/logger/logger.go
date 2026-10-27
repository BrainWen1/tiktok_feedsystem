// Package logger 提供项目统一的结构化日志入口。
//
// 设计目标：同一套调用接口，按 APP_ENV 切换两种日志形态——
//   - dev/development：Console 文本编码，时间格式 2006-01-02-15:04:05，
//     带 [INFO]/[ERROR] 等级标识和 caller，面向开发者阅读；
//   - prod/production：zap 标准 JSON 编码，字段固定、单行输出，
//     面向 Loki/ELK 等日志采集与检索系统解析。
//
// 业务代码只依赖本包的 Info/Debug/Error 等方法，不感知底层编码器；
// 所有日志写入前由 redactCore 自动脱敏 password、token 等敏感字段。
//
// 需要全链路追踪时，请求内日志改用 Ctx(ctx).Info/Debug/...，
// 会自动从 ctx 提取 requestId 并追加 request_id 字段，无需手动拼装。
package logger

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// 运行环境取值，对外暴露便于 main 层判断。
const (
	// EnvDev 开发环境：人类可读的文本日志。
	EnvDev = "dev"
	// EnvProd 生产环境：JSON 结构化日志，供采集系统消费。
	EnvProd = "prod"
)

// devTimeLayout 开发环境时间格式，精确到秒，便于肉眼扫读。
const devTimeLayout = "2006-01-02-15:04:05"

// Field 是结构化日志字段，避免调用方直接依赖 zap。
type Field = zap.Field

// redactedValue 是敏感字段统一替换值。
const redactedValue = "[REDACTED]"

// global 保存当前生效的 logger，允许 Init 在启动阶段原子替换。
var global atomic.Pointer[zap.Logger]

func init() {
	// 未显式 Init 时提供可用的兜底 logger，避免任何早期日志丢失。
	// 此时 .env 可能尚未加载，APP_ENV 为空会按 dev 处理，保证启动期日志可读。
	env, _ := normalizeEnv(os.Getenv("APP_ENV"))
	global.Store(newLogger(env, defaultLevel(env)))
}

// Init 初始化全局 logger，保留原有调用方式：level 为空时按 APP_ENV 选择默认级别。
// APP_ENV 从进程环境读取，兼容仍使用旧签名的调用方；需要显式传入环境时用 InitWithEnv。
func Init(level string) error {
	return InitWithEnv(os.Getenv("APP_ENV"), level)
}

// InitWithEnv 按运行环境初始化全局 logger。
//
//   - appEnv 为空或未知时回退 dev，避免线上误配导致日志不可读；
//   - level 非空时覆盖环境默认级别（dev 默认 debug，prod 默认 info）；
//   - 同时根据环境设置 Gin 模式：prod=release（关闭 [GIN-debug] 路由打印），
//     dev=debug（保留路由信息便于本地调试）。
//
// 必须在启动 goroutine 之前调用，确保后台日志使用同一实例。
func InitWithEnv(appEnv, level string) error {
	env, known := normalizeEnv(appEnv)
	lvl, err := resolveLevel(env, level)
	if err != nil {
		return fmt.Errorf("init logger (env=%s): %w", env, err)
	}

	configureGin(env)
	global.Store(newLogger(env, lvl))

	if !known {
		// APP_ENV 拼错时明确告警，而不是静默使用 dev 配置。
		Warn("unknown APP_ENV, fallback to dev", String("app_env", appEnv), String("fallback", EnvDev))
	}
	return nil
}

// Sync 刷新缓冲区，进程退出前调用；stdout 同步失败可忽略。
func Sync() {
	_ = global.Load().Sync()
}

// L 返回当前全局 *zap.Logger，供需要 zap 原生能力（With、Nop 等）的场景使用。
// 业务代码仍应优先调用 Info/Error 等包级函数，保持日志格式与脱敏逻辑统一。
func L() *zap.Logger {
	return global.Load()
}

// normalizeEnv 归一化 APP_ENV，兼容 development/production 全称。
// 返回值 known 为 false 表示配置了无法识别的非空值，调用方应告警。
func normalizeEnv(raw string) (env string, known bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "dev", "development":
		return EnvDev, true
	case "prod", "production":
		return EnvProd, true
	default:
		return EnvDev, false
	}
}

// defaultLevel 返回环境默认级别：开发 debug 便于排查，生产 info 控制日志量。
func defaultLevel(env string) zapcore.Level {
	if env == EnvProd {
		return zapcore.InfoLevel
	}
	return zapcore.DebugLevel
}

// resolveLevel 解析显式日志级别，为空时回退环境默认级别。
func resolveLevel(env, raw string) (zapcore.Level, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultLevel(env), nil
	}
	lvl, err := zapcore.ParseLevel(strings.ToLower(raw))
	if err != nil {
		return defaultLevel(env), fmt.Errorf("parse log level %q: %w", raw, err)
	}
	return lvl, nil
}

// configureGin 同步 Gin 运行模式，避免 Gin 的原生文本输出污染结构化日志流：
//   - prod: release 模式，关闭 [GIN-debug] 路由注册打印，JSON 日志可直接被采集；
//   - dev: debug 模式，保留路由信息方便本地调试。
//
// 注意：这里只切换框架模式，Gin 访问日志中间件仍由 router 层决定。
func configureGin(env string) {
	if env == EnvProd {
		gin.SetMode(gin.ReleaseMode)
		return
	}
	gin.SetMode(gin.DebugMode)
}

// newLogger 构建带脱敏 core 的 zap logger。
func newLogger(env string, level zapcore.Level) *zap.Logger {
	core := zapcore.NewCore(
		newEncoder(env),
		zapcore.Lock(os.Stdout),
		zap.NewAtomicLevelAt(level),
	)
	return zap.New(
		redactCore{Core: core},
		zap.AddCaller(),
		// 包级 Info/Error 等函数是业务调用与 zap 之间的一层包装，跳过后
		// caller 才指向真正的业务代码，而不是 logger.go 自身。
		zap.AddCallerSkip(1),
		zap.AddStacktrace(zapcore.ErrorLevel),
	)
}

// newEncoder 按环境返回编码器，两者字段名保持一致（ts/level/caller/msg），
// 让本地和线上日志使用同一套检索习惯。
func newEncoder(env string) zapcore.Encoder {
	if env == EnvProd {
		// 生产：标准 zap JSON 格式，单行输出，Loki/ELK 可直接按字段检索。
		cfg := zap.NewProductionEncoderConfig()
		cfg.TimeKey = "ts"
		cfg.EncodeTime = zapcore.ISO8601TimeEncoder // 保留时区信息，便于按时间聚合
		cfg.EncodeLevel = zapcore.LowercaseLevelEncoder
		cfg.EncodeCaller = zapcore.ShortCallerEncoder
		return zapcore.NewJSONEncoder(cfg)
	}

	// 开发：Console 文本格式，保留 caller，等级渲染成 [INFO]/[ERROR]。
	cfg := zap.NewDevelopmentEncoderConfig()
	cfg.TimeKey = "ts"
	cfg.LevelKey = "level"
	cfg.CallerKey = "caller"
	cfg.MessageKey = "msg"
	cfg.StacktraceKey = "stacktrace"
	cfg.EncodeTime = zapcore.TimeEncoderOfLayout(devTimeLayout)
	cfg.EncodeLevel = bracketLevelEncoder
	cfg.EncodeDuration = zapcore.StringDurationEncoder
	cfg.EncodeCaller = zapcore.ShortCallerEncoder
	return zapcore.NewConsoleEncoder(cfg)
}

// bracketLevelEncoder 把等级渲染成 [INFO]/[ERROR]，便于终端快速定位。
func bracketLevelEncoder(level zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString("[" + level.CapitalString() + "]")
}

// Debug 输出调试日志，用于缓存细节、内部流程等高频信息。
func Debug(msg string, fields ...Field) {
	L().Debug(msg, fields...)
}

// Info 输出常规运行日志，用于启动、连接、生命周期等事件。
func Info(msg string, fields ...Field) {
	L().Info(msg, fields...)
}

// Warn 输出告警日志，用于可降级的异常。
func Warn(msg string, fields ...Field) {
	L().Warn(msg, fields...)
}

// Error 输出错误日志，用于需要人工关注的失败。
func Error(msg string, fields ...Field) {
	L().Error(msg, fields...)
}

// Fatal 输出致命日志后退出进程，语义与 log.Fatal 一致。
func Fatal(msg string, fields ...Field) {
	L().Fatal(msg, fields...)
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

// Check 必须显式实现：zap 默认会把内层 core 放进 CheckedEntry，
// 从而绕过本包装层的 Write，导致脱敏失效。
func (c redactCore) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return ce.AddCore(entry, c)
	}
	return ce
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
