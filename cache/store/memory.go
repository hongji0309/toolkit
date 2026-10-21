package store

import (
	"context"
	"sync"
	"time"

	"gitee.com/hongjizhong/toolkit/cache"
)

var _ cache.CacheStore = (*MemoryCacheStore)(nil)

type memoryEntry struct {
	value    []byte
	expireAt time.Time // 零值表示永不过期
}

func (e memoryEntry) expired(now time.Time) bool {
	return !e.expireAt.IsZero() && now.After(e.expireAt)
}

// MemoryCacheStore 基于进程内存的缓存存储，仅适用于单实例部署
type MemoryCacheStore struct {
	mu      sync.RWMutex
	entries map[string]memoryEntry
	stop    chan struct{}
	once    sync.Once
}

// NewMemoryCacheStore cleanupInterval 为过期数据的清理间隔，小于等于 0 时只在访问时惰性删除
func NewMemoryCacheStore(cleanupInterval time.Duration) *MemoryCacheStore {
	s := &MemoryCacheStore{
		entries: make(map[string]memoryEntry),
		stop:    make(chan struct{}),
	}
	if cleanupInterval > 0 {
		go s.cleanup(cleanupInterval)
	}
	return s
}

// Get 未命中时返回 ErrCacheMiss
func (s *MemoryCacheStore) Get(ctx context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	entry, ok := s.entries[key]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrCacheMiss
	}
	if entry.expired(time.Now()) {
		s.deleteExpired(key)
		return nil, ErrCacheMiss
	}
	return append([]byte(nil), entry.value...), nil
}

// Set ttl 单位为秒，小于等于 0 时永不过期
func (s *MemoryCacheStore) Set(ctx context.Context, key string, value []byte, ttl int64) error {
	s.mu.Lock()
	s.entries[key] = memoryEntry{
		value:    append([]byte(nil), value...),
		expireAt: expireAt(ttl),
	}
	s.mu.Unlock()
	return nil
}

// Expire 续约，ttl 单位为秒；key 不存在时返回 ErrCacheMiss
func (s *MemoryCacheStore) Expire(ctx context.Context, key string, ttl int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok || entry.expired(time.Now()) {
		delete(s.entries, key)
		return ErrCacheMiss
	}
	entry.expireAt = expireAt(ttl)
	s.entries[key] = entry
	return nil
}

func (s *MemoryCacheStore) Delete(ctx context.Context, keys ...string) error {
	s.mu.Lock()
	for _, key := range keys {
		delete(s.entries, key)
	}
	s.mu.Unlock()
	return nil
}

// Close 停止后台清理
func (s *MemoryCacheStore) Close() {
	s.once.Do(func() { close(s.stop) })
}

// deleteExpired 删除已过期的 key，删除前再次确认，避免误删期间被重新写入的值
func (s *MemoryCacheStore) deleteExpired(key string) {
	s.mu.Lock()
	if entry, ok := s.entries[key]; ok && entry.expired(time.Now()) {
		delete(s.entries, key)
	}
	s.mu.Unlock()
}

func (s *MemoryCacheStore) cleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			s.mu.Lock()
			for key, entry := range s.entries {
				if entry.expired(now) {
					delete(s.entries, key)
				}
			}
			s.mu.Unlock()
		case <-s.stop:
			return
		}
	}
}

func expireAt(ttl int64) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(time.Duration(ttl) * time.Second)
}
