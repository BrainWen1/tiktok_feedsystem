package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisCache struct {
	client *redis.Client
}

func NewRedisCache(addr, password string, db int) *RedisCache {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	return &RedisCache{client: client}
}

func (r *RedisCache) Close() error {
	return r.client.Close()
}

// Ping检查Redis连接是否正常
func (r *RedisCache) Ping(ctx context.Context) error {
	_, err := r.client.Ping(ctx).Result()
	return err
}

// Get获取缓存值
func (r *RedisCache) Get(ctx context.Context, key string) (string, error) {
	val, err := r.client.Get(ctx, key).Result()
	if err != nil {
		return "", err
	}
	return val, err
}

// Set设置缓存值
func (r *RedisCache) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return r.client.Set(ctx, key, value, expiration).Err()
}

// Delete删除缓存值
func (r *RedisCache) Delete(ctx context.Context, keys ...string) error {
	return r.client.Del(ctx, keys...).Err()
}

// AddTokenToBlacklist 将Access Token加入黑名单
func (r *RedisCache) AddTokenToBlacklist(ctx context.Context, token string, expiration time.Duration) error {
	// 将token加入黑名单，设置过期时间为token的剩余有效期
	log.Printf("Adding token to blacklist: %s with expiration: %v", token, expiration)
	return r.Set(ctx, fmt.Sprintf("blacklist_token:%s", token), "blacklisted", expiration)
}

// IsTokenInBlacklist 检查Access Token是否在黑名单中
func (r *RedisCache) IsTokenInBlacklist(ctx context.Context, token string) (bool, error) {
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
	key := fmt.Sprintf("refresh_set:%d", uid)
	return c.client.SRem(ctx, key, refresh_token).Err()
}

// CleanAllUserRefresh 清空该用户全部refresh
func (c *RedisCache) CleanAllUserRefresh(ctx context.Context, uid uint) error {
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
	if c == nil || c.client == nil {
		return "", false, nil
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
	if c == nil || c.client == nil {
		return nil
	}
	// 使用Lua脚本原子性地检查锁的值是否与token匹配，如果匹配则删除锁，否则不做任何操作
	_, err := unlockScript.Run(ctx, c.client, []string{key}, token).Result()
	return err
}
