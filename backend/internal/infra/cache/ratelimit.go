package cache

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// slidingWindowScript 原子执行滑动窗口限流：
// 1. 移除窗口外的历史请求；2. 判断当前窗口内请求数是否达到上限；3. 未超限则记录本次请求。
// 使用毫秒时间戳作为 ZSet score，窗口长度为毫秒。
var slidingWindowScript = redis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local member = ARGV[4]

redis.call('ZREMRANGEBYSCORE', key, '-inf', now - window)
local count = redis.call('ZCARD', key)
if count >= limit then
    return 0
end
redis.call('ZADD', key, now, member)
redis.call('PEXPIRE', key, window)
return 1
`)

// AllowSlidingWindow 使用 Redis ZSet 实现滑动窗口限流。
// 返回 true 表示放行；缓存禁用时降级为放行，保证 Redis 故障不影响主流程。
// key 由调用方按业务维度拼接（如 ratelimit:login:ip:1.2.3.4）。
func (r *RedisCache) AllowSlidingWindow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	if r == nil || !r.IsEnabled() {
		return true, nil
	}
	if limit <= 0 || window <= 0 {
		return true, nil
	}

	nowMs := time.Now().UnixMilli()
	// 毫秒时间戳可能在同一毫秒内重复，追加纳秒保证 ZSet member 唯一
	member := strconv.FormatInt(nowMs, 10) + ":" + strconv.FormatInt(time.Now().UnixNano(), 10)

	allowed, err := slidingWindowScript.Run(
		ctx,
		r.client,
		[]string{key},
		nowMs,
		window.Milliseconds(),
		limit,
		member,
	).Int()
	if err != nil {
		return true, fmt.Errorf("sliding window limit key=%s: %w", key, err)
	}
	return allowed == 1, nil
}
