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
	return s.client.Set(ctx, key, value, time.Duration(ttl)*time.Second).Err()
}

// Expire 续约，ttl 单位为秒；key 不存在时返回 ErrCacheMiss
func (s *RedisCacheStore) Expire(ctx context.Context, key string, ttl int64) error {
	ok, err := s.client.Expire(ctx, key, time.Duration(ttl)*time.Second).Result()
	if err != nil {
		return err
	}
	if !ok {
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
