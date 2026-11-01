package locker

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hongji0309/toolkit/lock"
)

var (
	_ lock.Locker = (*RedisLocker)(nil)
	_ lock.Lock   = (*redisLock)(nil)
)

// unlockScript token 一致时才删除，避免误删他人的锁
var unlockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0`)

// refreshScript token 一致时才续期
var refreshScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0`)

// RedisLocker 基于单节点 Redis 的分布式锁（SET NX PX + Lua 校验 token）。
// 主从切换时可能丢锁，适用于防重复提交、定时任务防并发等场景，不适用于强一致场景
type RedisLocker struct {
	client redis.UniversalClient
	opts   options
}

// NewRedisLocker 创建 Redis 锁，client 可传 *redis.Client
func NewRedisLocker(client redis.UniversalClient, opts ...Option) *RedisLocker {
	return &RedisLocker{client: client, opts: newOptions(opts)}
}

// TryLock 尝试加锁一次，锁被占用时返回 lock.ErrNotAcquired
func (l *RedisLocker) TryLock(ctx context.Context, key string, ttl time.Duration) (lock.Lock, error) {
	if err := checkTTL(ttl); err != nil {
		return nil, err
	}
	lk := &redisLock{client: l.client, key: key, fullKey: l.opts.keyPrefix + key, token: newToken()}
	ok, err := l.client.SetNX(ctx, lk.fullKey, lk.token, ttl).Result()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, lock.ErrNotAcquired
	}
	return lk, nil
}

// Lock 加锁，锁被占用时按间隔重试直到成功或 ctx 结束
func (l *RedisLocker) Lock(ctx context.Context, key string, ttl time.Duration) (lock.Lock, error) {
	if err := checkTTL(ttl); err != nil {
		return nil, err
	}
	return acquire(ctx, l.opts.retryInterval, func() (lock.Lock, error) {
		return l.TryLock(ctx, key, ttl)
	})
}

type redisLock struct {
	client  redis.UniversalClient
	key     string
	fullKey string
	token   string
}

func (lk *redisLock) Key() string { return lk.key }

func (lk *redisLock) Refresh(ctx context.Context, ttl time.Duration) error {
	if err := checkTTL(ttl); err != nil {
		return err
	}
	n, err := refreshScript.Run(ctx, lk.client, []string{lk.fullKey}, lk.token, ttl.Milliseconds()).Int64()
	if err != nil {
		return err
	}
	if n == 0 {
		return lock.ErrNotHeld
	}
	return nil
}

func (lk *redisLock) Unlock(ctx context.Context) error {
	n, err := unlockScript.Run(ctx, lk.client, []string{lk.fullKey}, lk.token).Int64()
	if err != nil {
		return err
	}
	if n == 0 {
		return lock.ErrNotHeld
	}
	return nil
}
