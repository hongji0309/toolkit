package locker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/hongji0309/toolkit/lock"
)

// acquire 循环调用 try，锁被占用时等待 interval 后重试，直到成功、出错或 ctx 结束
func acquire(ctx context.Context, interval time.Duration, try func() (lock.Lock, error)) (lock.Lock, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w: %w", lock.ErrNotAcquired, err)
		}
		lk, err := try()
		if !errors.Is(err, lock.ErrNotAcquired) {
			return lk, err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %w", lock.ErrNotAcquired, ctx.Err())
		case <-time.After(interval):
		}
	}
}

// checkTTL 校验 ttl 不小于 lock.MinTTL，错误信息带上实际传入的值，便于发现误传（如漏写 time.Second）
func checkTTL(ttl time.Duration) error {
	if ttl < lock.MinTTL {
		return fmt.Errorf("%w, got %s", lock.ErrInvalidTTL, ttl)
	}
	return nil
}

// newToken 生成随机 token，标识锁的持有者
func newToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
