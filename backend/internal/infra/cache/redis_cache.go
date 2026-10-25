package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

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
		return "", err
	}
	return val, err
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
	log.Printf("Adding token to blacklist: [REDACTED] with expiration: %v", expiration)
	return r.Set(ctx, fmt.Sprintf("blacklist_token:%s", token), "blacklisted", expiration)
}

// IsTokenInBlacklist 检查Access Token是否在黑名单中
func (r *RedisCache) IsTokenInBlacklist(ctx context.Context, token string) (bool, error) {
	if !r.IsEnabled() {
		return false, nil // 缓存禁用，无法校验黑名单，按未注销处理
	}
	// 尝试从Redis的黑名单中获取token
	val, err := r.Get(ctx, fmt.Sprintf("blacklist_token:%s", token))
	// Redis返回缓存未命中
	if err != nil {
		// 不在黑名单中
		if err.Error() == "redis: nil" {
			return false, nil
		}
		// 其他错误
		log.Printf("Error checking token in blacklist in UserService: %v", err)
		return false, err
	}
	// 如果Redis返回的值是"blacklisted"，说明token在黑名单中
	return val == "blacklisted", nil
}

// AddUserRefreshSet 将refresh token加入用户集合
func (c *RedisCache) AddUserRefreshSet(ctx context.Context, uid uint, refresh_token string, expire time.Duration) error {
	if !c.IsEnabled() {
		return nil // 缓存禁用，跳过写入
	}
	key := fmt.Sprintf("refresh_set:%d", uid)
	// sadd 插入成员
	err := c.client.SAdd(ctx, key, refresh_token).Err()
	if err != nil {
		return err
	}
	// 设置集合整体过期时间
	return c.client.Expire(ctx, key, expire).Err()
}

// RemoveUserRefreshSetItem 用户集合移除单条refresh token
func (c *RedisCache) RemoveUserRefreshSetItem(ctx context.Context, uid uint, refresh_token string) error {
	if !c.IsEnabled() {
		return nil // 缓存禁用，跳过移除
	}
	key := fmt.Sprintf("refresh_set:%d", uid)
	return c.client.SRem(ctx, key, refresh_token).Err()
}

// CleanAllUserRefresh 清空该用户全部refresh
func (c *RedisCache) CleanAllUserRefresh(ctx context.Context, uid uint) error {
	if !c.IsEnabled() {
		return nil // 缓存禁用，跳过清理
	}
	setKey := fmt.Sprintf("refresh_set:%d", uid)
	// 获取集合里面全部rt
	refresh_tokens, err := c.client.SMembers(ctx, setKey).Result()
	if err != nil {
		return err
	}
	// 逐个删除 rt:{token}
	for _, refresh_token := range refresh_tokens {
		_ = c.client.Del(ctx, fmt.Sprintf("refresh_token:%s", refresh_token)).Err()
	}
	// 删除整个set集合
	return c.client.Del(ctx, setKey).Err()
}

// CleanStaleRefreshSetMembers 清扫集合里已经过期消失的refresh token垃圾成员
func (c *RedisCache) CleanStaleRefreshSetMembers(ctx context.Context) error {
	if !c.IsEnabled() {
		return nil // 缓存禁用，跳过定时清扫
	}
	var cursor uint64
	// Scan迭代查找所有 refresh_set:* 的key
	for {
		var keys []string
		var err error
		// 获取一批 refresh_set:* 的keys
		keys, cursor, err = c.client.Scan(ctx, cursor, "refresh_set:*", 100).Result()
		if err != nil {
			return err
		}
		// 获取单个key
		for _, setKey := range keys {
			var sCursor uint64
			// 获取该key对应的一批 refresh token
			for {
				members, nextSCursor, err := c.client.SScan(ctx, setKey, sCursor, "", 100).Result()
				if err != nil {
					return err
				}
				// 遍历该批次里的每个refresh token，检查其对应的key是否存在，如果不存在则从集合中移除
				for _, rt := range members {
					rtKey := fmt.Sprintf("refresh:%s", rt)
					exists, err := c.client.Exists(ctx, rtKey).Result()
					if err != nil {
						continue
					}
					// rt对应的key已经过期不存在，从集合删掉这条脏数据
					if exists == 0 {
						_ = c.client.SRem(ctx, setKey, rt).Err()
					}
				}
				sCursor = nextSCursor
				if sCursor == 0 {
					break
				}
			}
		}
		if cursor == 0 {
			break
		}
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
	log.Printf("trying to add member %v to set %s with ttl %v", member, key, ttl)
	err := c.client.SAdd(ctx, key, member).Err()
	if err != nil {
		log.Printf("Error adding member to set %s: %v", key, err)
		return err
	}
	// 设置集合整体过期时间
	if ttl > 0 {
		log.Printf("setting ttl for set %s to %v", key, ttl)
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
		log.Printf("Error removing member from set %s: %v", key, err)
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
		log.Printf("Error checking membership of %v in set %s: %v", member, key, err)
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
		log.Printf("Error checking membership of %v in set %s: %v", members, key, err)
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
		log.Printf("Error checking existence of key %s: %v", key, err)
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
		log.Printf("zList is empty, nothing to add to zset %s", key)
		return nil
	}

	// 使用ZAdd命令将元素添加到ZSet中
	err := c.client.ZAdd(ctx, key, zList...).Err()
	if err != nil {
		log.Printf("Error adding member to zset %s: %v", key, err)
		return err
	}
	// 设置ZSet整体过期时间
	if ttl > 0 {
		log.Printf("setting ttl for zset %s to %v", key, ttl)
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
		log.Printf("Error removing member from zset %s: %v", key, err)
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
		log.Printf("Error increasing value of key %s by %d: %v", key, increment, err)
		return 0, err
	}
	// 设置key的过期时间
	if ttl > 0 {
		log.Printf("setting ttl for key %s to %v", key, ttl)
		err = c.client.Expire(ctx, key, ttl).Err()
		if err != nil {
			log.Printf("Error setting expiration for key %s: %v", key, err)
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
		log.Printf("Error getting zset cardinality for key %s: %v", key, err)
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
		log.Printf("Error getting zset range for key %s: %v", key, err)
		return nil, err
	}
	return result, nil
}

// ZIsMember 判断 zset 里面有没有这个 member
func (c *RedisCache) ZIsMember(ctx context.Context, key string, member interface{}) (bool, error) {
	if !c.IsEnabled() {
		return false, nil // 缓存禁用，按未命中处理，由上层回源数据库
	}
	// 将 member 转换为字符串类型，因为 ZRank 方法需要字符串类型的 member
	memberStr := fmt.Sprintf("%v", member)

	_, err := c.client.ZRank(ctx, key, memberStr).Result()
	if err == redis.Nil {
		// 查不到，不存在
		return false, nil
	}
	if err != nil {
		log.Printf("ZRank check failed key=%s member=%v err=%v", key, member, err)
		return false, err
	}
	// 没报错，说明查到了，存在
	return true, nil
}
