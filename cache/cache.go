package cache

import "context"

// CacheStore 缓存存储，ttl 单位为秒
type CacheStore interface {
	// Get 未命中时返回 store.ErrCacheMiss
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl int64) error
	// Expire 续约，重新设置过期时间；key 不存在时返回 store.ErrCacheMiss
	Expire(ctx context.Context, key string, ttl int64) error
	Delete(ctx context.Context, keys ...string) error
}
