package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"feedsystem/internal/utils/logger"

	"github.com/redis/go-redis/v9"
)

// ErrCacheMiss 表示缓存key不存在，用于区分未命中与Redis故障
var ErrCacheMiss = errors.New("cache miss")

type RedisCache struct {
	client  *redis.Client
	enabled atomic.Bool // 缓存是否可用；Redis Ping失败时禁用，所有缓存操作降级为空操作
}

func NewRedisCache(addr, password string, db int) *RedisCache {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	c := &RedisCache{client: client}
	c.enabled.Store(true) // 默认启用，由启动探测结果决定是否禁用
	return c
}

// IsEnabled 返回缓存组件是否可用，nil实例视为不可用
func (r *RedisCache) IsEnabled() bool {
	return r != nil && r.enabled.Load()
}

// Enable 启用缓存
func (r *RedisCache) Enable() {
	if r != nil {
		r.enabled.Store(true)
	}
}

// Disable 禁用缓存，后续缓存读写全部跳过，业务降级走数据库
func (r *RedisCache) Disable() {
	if r != nil {
		r.enabled.Store(false)
	}
}

func (r *RedisCache) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.Close()
}

// Ping检查Redis连接是否正常
func (r *RedisCache) Ping(ctx context.Context) error {
	_, err := r.client.Ping(ctx).Result()
	return err
}

// Get获取缓存值
func (r *RedisCache) Get(ctx context.Context, key string) (string, error) {
	if !r.IsEnabled() {
		return "", nil // 缓存禁用，按未命中处理，由上层回源数据库
	}
	val, err := r.client.Get(ctx, key).Result()
	if err != nil {
		// key 不存在属于正常未命中，统一转换为 ErrCacheMiss，避免调用方依赖错误字符串
		if errors.Is(err, redis.Nil) {
			return "", ErrCacheMiss
		}
		return "", fmt.Errorf("get cache key %s: %w", key, err)
	}
	return val, nil
}

// GetDel 原子读取并删除key，用于一次性凭证（refresh token轮换），避免Get+Delete并发竞争
func (r *RedisCache) GetDel(ctx context.Context, key string) (string, error) {
	if !r.IsEnabled() {
		return "", ErrCacheMiss // 缓存禁用，按未命中处理
	}
	val, err := r.client.GetDel(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrCacheMiss
	}
	if err != nil {
		return "", fmt.Errorf("getdel: %w", err)
	}
	return val, nil
}

// ConvInt64 将 (valStr,err) 转 int64
func ConvInt64(val string, err error) (int64, error) {
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(val, 10, 64)
}

// ConvInt 转int
func ConvInt(val string, err error) (int, error) {
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseInt(val, 10, 64)
	return int(v), err
}

// ConvBool 转bool，"1"/"0"
func ConvBool(val string, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	return strconv.ParseBool(val)
}

// ConvFloat64 转float64
func ConvFloat64(val string, err error) (float64, error) {
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(val, 64)
}

// Set设置缓存值
func (r *RedisCache) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	if !r.IsEnabled() {
		return nil // 缓存禁用，跳过写入
	}
	return r.client.Set(ctx, key, value, expiration).Err()
}

// Delete删除缓存值
func (r *RedisCache) Delete(ctx context.Context, keys ...string) error {
	if !r.IsEnabled() {
		return nil // 缓存禁用，跳过删除
	}
	return r.client.Del(ctx, keys...).Err()
}

// AddTokenToBlacklist 将Access Token加入黑名单
func (r *RedisCache) AddTokenToBlacklist(ctx context.Context, token string, expiration time.Duration) error {
	if !r.IsEnabled() {
		return nil // 缓存禁用，跳过黑名单写入
	}
	// 将token加入黑名单，设置过期时间为token的剩余有效期
	// 屏蔽敏感信息，防止日志泄露
	logger.Debug("adding token to blacklist", logger.Duration("expiration", expiration))
	return r.Set(ctx, BlacklistTokenKey(token), "blacklisted", expiration)
}

// IsTokenInBlacklist 检查Access Token是否在黑名单中
func (r *RedisCache) IsTokenInBlacklist(ctx context.Context, token string) (bool, error) {
	if !r.IsEnabled() {
		return false, nil // 缓存禁用，无法校验黑名单，按未注销处理
	}
	// 尝试从Redis的黑名单中获取token
	val, err := r.Get(ctx, BlacklistTokenKey(token))
	// Redis返回缓存未命中，说明token不在黑名单中
	if err != nil {
		if errors.Is(err, ErrCacheMiss) || errors.Is(err, redis.Nil) {
			return false, nil
		}
		logger.Error("check token in blacklist failed", logger.Err(err))
		return false, fmt.Errorf("check token blacklist: %w", err)
	}
	// 如果Redis返回的值是"blacklisted"，说明token在黑名单中
	return val == "blacklisted", nil
}

// AddUserRefreshSet 将refresh token加入用户集合
// 每个 refresh token 的 refresh_token:{token} key 单独设置 TTL，到期后自动失效；
// 索引集合只负责维护 <userID, tokens> 映射，不再依赖定时任务批量清理。
func (c *RedisCache) AddUserRefreshSet(ctx context.Context, userID uint, refreshToken string, expire time.Duration) error {
	if !c.IsEnabled() {
		return nil // 缓存禁用，跳过写入
	}
	key := RefreshSetKey(userID)
	// sadd 插入成员
	err := c.client.SAdd(ctx, key, refreshToken).Err()
	if err != nil {
		return err
	}
	// 设置集合整体过期时间，保证索引不会早于成员 token 过期
	return c.client.Expire(ctx, key, expire).Err()
}

// RemoveUserRefreshSetItem 用户集合移除单条refresh token
func (c *RedisCache) RemoveUserRefreshSetItem(ctx context.Context, userID uint, refreshToken string) error {
	if !c.IsEnabled() {
		return nil // 缓存禁用，跳过移除
	}
	return c.client.SRem(ctx, RefreshSetKey(userID), refreshToken).Err()
}

// CleanAllUserRefresh 删除该用户名下全部 refresh token
// 先取索引集合，再用一次多 key DEL 原子删除所有 refresh_token:{token} 与索引集合；
// Redis 任一操作失败都会返回 error，由调用方决定是否向上抛出。
func (c *RedisCache) CleanAllUserRefresh(ctx context.Context, userID uint) error {
	if !c.IsEnabled() {
		return nil // 缓存禁用，跳过清理
	}
	setKey := RefreshSetKey(userID)
	// 获取集合里面全部 rt
	refreshTokens, err := c.client.SMembers(ctx, setKey).Result()
	if err != nil {
		return fmt.Errorf("list refresh tokens for user %d: %w", userID, err)
	}
	// 批量删除 token key 和索引集合，DEL 的多 key 删除是原子操作
	keys := make([]string, 0, len(refreshTokens)+1)
	for _, token := range refreshTokens {
		keys = append(keys, RefreshTokenKey(token))
	}
	keys = append(keys, setKey)
	if err := c.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("delete refresh tokens for user %d: %w", userID, err)
	}
	return nil
}

// 工具函数：randToken 生成随机token字符串
func randToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Lock 尝试获取分布式锁，返回锁的token和是否成功获取锁
func (c *RedisCache) Lock(ctx context.Context, key string, ttl time.Duration) (string, bool, error) {
	if !c.IsEnabled() {
		return "", false, nil // 缓存禁用，不加锁，由上层直接回源数据库
	}
	// 生成随机token作为锁的值
	token, err := randToken(16)
	if err != nil {
		return "", false, err
	}
	// 尝试使用SETNX命令设置锁，成功则返回token和true，失败则返回false
	ok, err := c.client.SetNX(ctx, key, token, ttl).Result()
	return token, ok, err
}

// unlockScript 是一个Lua脚本，用于在释放锁时检查锁的值是否与token匹配，如果匹配则删除锁，否则不做任何操作
var unlockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
else
  return 0
end
`)

// Unlock 释放分布式锁，只有当锁的值与token匹配时才会释放锁
func (c *RedisCache) Unlock(ctx context.Context, key string, token string) error {
	if !c.IsEnabled() {
		return nil // 缓存禁用，跳过解锁
	}
	// 使用Lua脚本原子性地检查锁的值是否与token匹配，如果匹配则删除锁，否则不做任何操作
	_, err := unlockScript.Run(ctx, c.client, []string{key}, token).Result()
	return err
}

// AddToSet 将元素添加到Redis集合中
func (c *RedisCache) AddToSet(ctx context.Context, key string, member interface{}, ttl time.Duration) error {
	if !c.IsEnabled() {
		return nil // 缓存禁用，跳过写入
	}
	logger.Debug("adding member to set", logger.Any("member", member), logger.String("key", key), logger.Duration("ttl", ttl))
	err := c.client.SAdd(ctx, key, member).Err()
	if err != nil {
		logger.Error("add member to set failed", logger.String("key", key), logger.Err(err))
		return err
	}
	// 设置集合整体过期时间
	if ttl > 0 {
		logger.Debug("setting ttl for set", logger.String("key", key), logger.Duration("ttl", ttl))
		return c.client.Expire(ctx, key, ttl).Err()
	}
	return nil
}

// RemoveFromSet 将元素从Redis集合中移除
func (c *RedisCache) RemoveFromSet(ctx context.Context, key string, member interface{}) error {
	if !c.IsEnabled() {
		return nil // 缓存禁用，跳过移除
	}
	err := c.client.SRem(ctx, key, member).Err()
	if err != nil {
		logger.Error("remove member from set failed", logger.String("key", key), logger.Err(err))
		return err
	}
	return nil
}

// IsMemberOfSet 检查元素是否在Redis集合中
func (c *RedisCache) IsMemberOfSet(ctx context.Context, key string, member interface{}) (bool, error) {
	if !c.IsEnabled() {
		return false, nil // 缓存禁用，按未命中处理，由上层回源数据库
	}
	isMember, err := c.client.SIsMember(ctx, key, member).Result()
	if err != nil {
		logger.Error("check set membership failed", logger.Any("member", member), logger.String("key", key), logger.Err(err))
		return false, err
	}
	return isMember, nil
}

// MIsMemberOfSet 批量检查元素是否在Redis集合中
func (c *RedisCache) MIsMemberOfSet(ctx context.Context, key string, members []interface{}) ([]bool, error) {
	if !c.IsEnabled() {
		// 缓存禁用，返回与入参等长的false切片，保证上层可安全按下标访问并回源数据库
		return make([]bool, len(members)), nil
	}
	// 调用SMIsMember命令批量检查每个元素是否在集合中，返回一个布尔切片
	result, err := c.client.SMIsMember(ctx, key, members...).Result()
	if err != nil {
		logger.Error("check set membership failed", logger.Any("members", members), logger.String("key", key), logger.Err(err))
		return nil, err
	}
	return result, nil
}

// Exists 检查Redis中是否存在指定的key
func (c *RedisCache) Exists(ctx context.Context, key string) (bool, error) {
	if !c.IsEnabled() {
		return false, nil // 缓存禁用，按key不存在处理
	}
	exists, err := c.client.Exists(ctx, key).Result()
	if err != nil {
		logger.Error("check key existence failed", logger.String("key", key), logger.Err(err))
		return false, err
	}
	return exists > 0, nil
}

// ZAdd 将元素添加到Redis有序集合ZSet中，并设置过期时间
func (c *RedisCache) ZAdd(ctx context.Context, key string, zList []redis.Z, ttl time.Duration) error {
	if !c.IsEnabled() {
		return nil // 缓存禁用，跳过写入
	}
	if len(zList) == 0 {
		logger.Debug("zset list is empty, nothing to add", logger.String("key", key))
		return nil
	}

	// 使用ZAdd命令将元素添加到ZSet中
	err := c.client.ZAdd(ctx, key, zList...).Err()
	if err != nil {
		logger.Error("add member to zset failed", logger.String("key", key), logger.Err(err))
		return err
	}
	// 设置ZSet整体过期时间
	if ttl > 0 {
		logger.Debug("setting ttl for zset", logger.String("key", key), logger.Duration("ttl", ttl))
		return c.client.Expire(ctx, key, ttl).Err()
	}
	return nil
}

// ZRem 将元素从Redis有序集合ZSet中移除
func (c *RedisCache) ZRem(ctx context.Context, key string, member interface{}) error {
	if !c.IsEnabled() {
		return nil // 缓存禁用，跳过移除
	}
	err := c.client.ZRem(ctx, key, member).Err()
	if err != nil {
		logger.Error("remove member from zset failed", logger.String("key", key), logger.Err(err))
		return err
	}
	return nil
}

// Increase 将Redis中的key对应的数值增加指定的增量，并设置过期时间
func (c *RedisCache) Increase(ctx context.Context, key string, increment int64, ttl time.Duration) (int64, error) {
	if !c.IsEnabled() {
		return 0, nil // 缓存禁用，跳过计数更新，由数据库统计兜底
	}
	newValue, err := c.client.IncrBy(ctx, key, increment).Result()
	if err != nil {
		logger.Error("increase key value failed", logger.String("key", key), logger.Int64("increment", increment), logger.Err(err))
		return 0, err
	}
	// 设置key的过期时间
	if ttl > 0 {
		logger.Debug("setting ttl for key", logger.String("key", key), logger.Duration("ttl", ttl))
		err = c.client.Expire(ctx, key, ttl).Err()
		if err != nil {
			logger.Error("set key expiration failed", logger.String("key", key), logger.Err(err))
			return newValue, err
		}
	}
	return newValue, nil
}

// ZCard 获取Redis有序集合ZSet的元素数量
func (c *RedisCache) ZCard(ctx context.Context, key string) (int64, error) {
	if !c.IsEnabled() {
		return 0, nil // 缓存禁用，返回0由上层回源数据库
	}
	count, err := c.client.ZCard(ctx, key).Result()
	if err != nil {
		logger.Error("get zset cardinality failed", logger.String("key", key), logger.Err(err))
		return 0, err
	}
	return count, nil
}

// ZRange 获取Redis有序集合ZSet中指定范围的元素，可指定顺倒序
func (c *RedisCache) ZRange(ctx context.Context, key string, start, stop int64, reverse bool) ([]string, error) {
	if !c.IsEnabled() {
		return nil, nil // 缓存禁用，按未命中处理，由上层回源数据库
	}
	var result []string
	var err error

	result, err = c.client.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key:   key,
		Start: start,
		Stop:  stop,
		Rev:   reverse, // 顺倒序
	}).Result()

	if err != nil {
		logger.Error("get zset range failed", logger.String("key", key), logger.Err(err))
		return nil, err
	}
	return result, nil
}

// ZRangeByScore 按分数区间读取 ZSet 成员，参数语义与 Redis ZRANGE 一致：
// reverse=false 时 start=min、stop=max；reverse=true 时 start=max、stop=min。
// 支持 Redis 原生语法，如 "-inf"、"+inf"、"(123" 表示不包含 123。
func (c *RedisCache) ZRangeByScore(ctx context.Context, key, start, stop string, offset, count int64, reverse bool) ([]string, error) {
	if !c.IsEnabled() {
		return nil, nil // 缓存禁用，按未命中处理
	}

	result, err := c.client.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key:     key,
		Start:   start,
		Stop:    stop,
		ByScore: true,
		Offset:  offset,
		Count:   count,
		Rev:     reverse,
	}).Result()
	if err != nil {
		logger.Error("get zset range by score failed", logger.String("key", key), logger.Err(err))
		return nil, err
	}
	return result, nil
}

// ZScore 获取 ZSet 成员的分数，用于生成复合游标。
func (c *RedisCache) ZScore(ctx context.Context, key, member string) (float64, error) {
	if !c.IsEnabled() {
		return 0, ErrCacheMiss // 缓存禁用，按未命中处理
	}
	score, err := c.client.ZScore(ctx, key, member).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, ErrCacheMiss
		}
		logger.Error("get zset score failed", logger.String("key", key), logger.String("member", member), logger.Err(err))
		return 0, fmt.Errorf("get zset score key %s member %s: %w", key, member, err)
	}
	return score, nil
}

// ZIsMember 判断 zset 里面有没有这个 member
func (c *RedisCache) ZIsMember(ctx context.Context, key string, member interface{}) (bool, error) {
	if !c.IsEnabled() {
		return false, nil // 缓存禁用，按未命中处理，由上层回源数据库
	}
	// 将 member 转换为字符串类型，因为 ZRank 方法需要字符串类型的 member
	memberStr := fmt.Sprintf("%v", member)

	_, err := c.client.ZRank(ctx, key, memberStr).Result()
	if errors.Is(err, redis.Nil) {
		// 查不到，不存在
		return false, nil
	}
	if err != nil {
		logger.Error("zrank check failed", logger.String("key", key), logger.Any("member", member), logger.Err(err))
		return false, fmt.Errorf("check zset member for key %s: %w", key, err)
	}
	// 没报错，说明查到了，存在
	return true, nil
}

// Publish 向Redis发布消息，用于SSE推送通知
func (c *RedisCache) Publish(ctx context.Context, channel string, message interface{}) error {
	if !c.IsEnabled() {
		return nil // 缓存禁用，跳过发布
	}
	// 使用Redis的Publish方法向指定频道发布消息
	err := c.client.Publish(ctx, channel, message).Err()
	if err != nil {
		logger.Error("publish message failed", logger.String("channel", channel), logger.Err(err))
		return fmt.Errorf("publish message to channel %s: %w", channel, err)
	}
	return nil
}

// Subscribe 订阅Redis频道，用于SSE推送通知
func (c *RedisCache) Subscribe(ctx context.Context, channel string) (*redis.PubSub, error) {
	if !c.IsEnabled() {
		return nil, fmt.Errorf("cache is disabled") // 缓存禁用，返回明确错误
	}
	return c.client.Subscribe(ctx, channel), nil
}
