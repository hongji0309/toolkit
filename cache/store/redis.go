package store

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"gitee.com/hongjizhong/toolkit/cache"
)

var _ cache.CacheStore = (*RedisCacheStore)(nil)

// RedisCacheStore 基于 Redis 的缓存存储
type RedisCacheStore struct {
	client *redis.Client
}

func NewRedisCacheStore(client *redis.Client) *RedisCacheStore {
	return &RedisCacheStore{client: client}
}

// Get 未命中时返回 ErrCacheMiss
func (s *RedisCacheStore) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := s.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrCacheMiss
	}
	return value, err
}

// Set ttl 单位为秒，小于等于 0 时永不过期
func (s *RedisCacheStore) Set(ctx context.Context, key string, value []byte, ttl int64) error {
	// go-redis 中 -1 表示 KeepTTL，统一转为 0 才是永不过期
	if ttl <= 0 {
		ttl = 0
	}
	return s.client.Set(ctx, key, value, time.Duration(ttl)*time.Second).Err()
}

// Expire 续约，ttl 单位为秒，小于等于 0 时改为永不过期；key 不存在时返回 ErrCacheMiss
func (s *RedisCacheStore) Expire(ctx context.Context, key string, ttl int64) error {
	// Redis 的 EXPIRE 传入小于等于 0 会直接删除 key，这里改用 PERSIST 与内存实现保持一致
	if ttl <= 0 {
		return s.persist(ctx, key)
	}
	ok, err := s.client.Expire(ctx, key, time.Duration(ttl)*time.Second).Result()
	if err != nil {
		return err
	}
	if !ok {
		return ErrCacheMiss
	}
	return nil
}

// persist 移除过期时间；PERSIST 在 key 不存在或本身没有过期时间时都返回 false，需要再判断 key 是否存在
func (s *RedisCacheStore) persist(ctx context.Context, key string) error {
	ok, err := s.client.Persist(ctx, key).Result()
	if err != nil || ok {
		return err
	}
	n, err := s.client.Exists(ctx, key).Result()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrCacheMiss
	}
	return nil
}

func (s *RedisCacheStore) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return s.client.Del(ctx, keys...).Err()
}
