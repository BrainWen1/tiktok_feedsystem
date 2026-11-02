package repo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"feedsystem/internal/infra/cache"
	"feedsystem/internal/model"
)

// ChunkRepo 分片会话redis存储repo层，只做纯粹读写，无业务逻辑
type ChunkRepo struct {
	cache *cache.RedisCache
}

func NewChunkRepo(cache *cache.RedisCache) *ChunkRepo {
	return &ChunkRepo{cache: cache}
}

const (
	chunkSessionKeyPrefix  = "chunk:upload:"
	chunkSessionLockPrefix = "chunk:lock:"
)

func getSessionKey(uploadID string) string {
	return chunkSessionKeyPrefix + uploadID
}

func getSessionLockKey(uploadID string) string {
	return chunkSessionLockPrefix + uploadID
}

// Enabled 分片会话依赖 Redis 才能保证多实例下状态一致。
// Redis 被禁用时上传功能应明确失败，而不是退化成无状态写入。
func (r *ChunkRepo) Enabled() bool {
	return r != nil && r.cache != nil && r.cache.IsEnabled()
}

// SaveSession 写入/更新会话
func (r *ChunkRepo) SaveSession(ctx context.Context, sess *model.ChunkUploadSession, ttl time.Duration) error {
	key := getSessionKey(sess.UploadID)
	data, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	return r.cache.Set(ctx, key, data, ttl)
}

// GetSession 获取会话
func (r *ChunkRepo) GetSession(ctx context.Context, uploadID string) (*model.ChunkUploadSession, error) {
	key := getSessionKey(uploadID)
	val, err := r.cache.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	var sess model.ChunkUploadSession
	if err := json.Unmarshal([]byte(val), &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

// DeleteSession 删除上传会话（合并完成/取消上传时调用）
func (r *ChunkRepo) DeleteSession(ctx context.Context, uploadID string) error {
	key := getSessionKey(uploadID)
	return r.cache.Delete(ctx, key)
}

// LockSession 获取上传会话的分布式锁，序列化同一 upload_id 的分片写入与合并。
// 否则并发上传多个分片时，会话里的 UploadedBits 会出现读旧写新导致分片标记丢失。
func (r *ChunkRepo) LockSession(ctx context.Context, uploadID string, ttl time.Duration) (string, bool, error) {
	return r.cache.Lock(ctx, getSessionLockKey(uploadID), ttl)
}

// UnlockSession 释放上传会话锁，仅当 token 匹配时才删除。
func (r *ChunkRepo) UnlockSession(ctx context.Context, uploadID, token string) error {
	return r.cache.Unlock(ctx, getSessionLockKey(uploadID), token)
}

// IsSessionNotFound 判断错误是否为 Redis key 不存在（会话过期或已清理）。
func IsSessionNotFound(err error) bool {
	return errors.Is(err, cache.ErrCacheMiss)
}
