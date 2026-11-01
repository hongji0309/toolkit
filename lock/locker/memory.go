package locker

import (
	"context"
	"sync"
	"time"

	"github.com/hongji0309/toolkit/lock"
)

var (
	_ lock.Locker = (*MemoryLocker)(nil)
	_ lock.Lock   = (*memoryLock)(nil)
)

type memoryEntry struct {
	token    string
	expireAt time.Time
}

// MemoryLocker 基于进程内存的锁，仅在当前进程内互斥，适用于单实例部署；行为与 RedisLocker 保持一致
type MemoryLocker struct {
	mu      sync.Mutex
	entries map[string]memoryEntry
	opts    options
}

// NewMemoryLocker 创建内存锁
func NewMemoryLocker(opts ...Option) *MemoryLocker {
	return &MemoryLocker{entries: make(map[string]memoryEntry), opts: newOptions(opts)}
}

// TryLock 尝试加锁一次，锁被占用时返回 lock.ErrNotAcquired
func (l *MemoryLocker) TryLock(ctx context.Context, key string, ttl time.Duration) (lock.Lock, error) {
	if err := checkTTL(ttl); err != nil {
		return nil, err
	}
	fullKey := l.opts.keyPrefix + key
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.entries[fullKey]; ok && now.Before(e.expireAt) {
		return nil, lock.ErrNotAcquired
	}
	lk := &memoryLock{locker: l, key: key, fullKey: fullKey, token: newToken()}
	l.entries[fullKey] = memoryEntry{token: lk.token, expireAt: now.Add(ttl)}
	return lk, nil
}

// Lock 加锁，锁被占用时按间隔重试直到成功或 ctx 结束
func (l *MemoryLocker) Lock(ctx context.Context, key string, ttl time.Duration) (lock.Lock, error) {
	if err := checkTTL(ttl); err != nil {
		return nil, err
	}
	return acquire(ctx, l.opts.retryInterval, func() (lock.Lock, error) {
		return l.TryLock(ctx, key, ttl)
	})
}

// held 在持有 mu 时调用，判断 token 对应的锁是否仍然有效；已过期的条目顺带删除
func (l *MemoryLocker) held(fullKey, token string, now time.Time) bool {
	e, ok := l.entries[fullKey]
	if !ok || e.token != token {
		return false
	}
	if !now.Before(e.expireAt) {
		delete(l.entries, fullKey)
		return false
	}
	return true
}

type memoryLock struct {
	locker  *MemoryLocker
	key     string
	fullKey string
	token   string
}

func (lk *memoryLock) Key() string { return lk.key }

func (lk *memoryLock) Refresh(ctx context.Context, ttl time.Duration) error {
	if err := checkTTL(ttl); err != nil {
		return err
	}
	now := time.Now()
	lk.locker.mu.Lock()
	defer lk.locker.mu.Unlock()
	if !lk.locker.held(lk.fullKey, lk.token, now) {
		return lock.ErrNotHeld
	}
	lk.locker.entries[lk.fullKey] = memoryEntry{token: lk.token, expireAt: now.Add(ttl)}
	return nil
}

func (lk *memoryLock) Unlock(ctx context.Context) error {
	lk.locker.mu.Lock()
	defer lk.locker.mu.Unlock()
	if !lk.locker.held(lk.fullKey, lk.token, time.Now()) {
		return lock.ErrNotHeld
	}
	delete(lk.locker.entries, lk.fullKey)
	return nil
}
