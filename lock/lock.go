package lock

import (
	"context"
	"errors"
	"time"
)

// MinTTL 锁的最小过期时间。ttl 只是进程崩溃、忘记解锁时的兜底，正常由 Unlock 释放，
// 过短会因 GC 停顿、网络抖动在业务执行完之前过期，也会让 WithLock 续期过于频繁
const MinTTL = time.Second

var (
	// ErrNotAcquired 锁已被占用，加锁失败
	ErrNotAcquired = errors.New("lock: not acquired")
	// ErrNotHeld 锁已过期或已被释放，当前不再持有
	ErrNotHeld = errors.New("lock: not held")
	// ErrInvalidTTL ttl 小于 MinTTL
	ErrInvalidTTL = errors.New("lock: ttl must be at least 1s")
)

// Locker 按 key 加锁，ttl 为锁的过期时间，小于 MinTTL（1 秒）时返回 ErrInvalidTTL
type Locker interface {
	// TryLock 尝试加锁一次，锁被占用时立即返回 ErrNotAcquired
	TryLock(ctx context.Context, key string, ttl time.Duration) (Lock, error)
	// Lock 加锁，锁被占用时按间隔重试直到成功或 ctx 结束；
	// ctx 结束时返回的错误同时满足 errors.Is(err, ErrNotAcquired) 和 errors.Is(err, ctx.Err())
	Lock(ctx context.Context, key string, ttl time.Duration) (Lock, error)
}

// Lock 已持有的锁
type Lock interface {
	// Key 加锁时传入的 key，不含前缀
	Key() string
	// Refresh 续期，把过期时间重置为 ttl；锁已不再持有时返回 ErrNotHeld
	Refresh(ctx context.Context, ttl time.Duration) error
	// Unlock 释放锁；锁已过期或已被释放时返回 ErrNotHeld
	Unlock(ctx context.Context) error
}

// WithLock 加锁（阻塞等待）后执行 fn，fn 返回后释放锁。
// fn 执行期间每 ttl/3 自动续期；确认锁已丢失（被他人抢占，或连续续期失败超过 ttl）时取消传给 fn 的 ctx，
// 并在返回的错误中带上 ErrNotHeld
func WithLock(ctx context.Context, l Locker, key string, ttl time.Duration, fn func(ctx context.Context) error) error {
	lk, err := l.Lock(ctx, key, ttl)
	if err != nil {
		return err
	}
	return runHeld(ctx, lk, ttl, fn)
}

// TryWithLock 与 WithLock 相同，但加锁只尝试一次：锁被占用时立即返回 ErrNotAcquired，不执行 fn。
// 适用于拿不到锁就放弃的场景，如定时任务上一次未执行完时跳过本次
func TryWithLock(ctx context.Context, l Locker, key string, ttl time.Duration, fn func(ctx context.Context) error) error {
	lk, err := l.TryLock(ctx, key, ttl)
	if err != nil {
		return err
	}
	return runHeld(ctx, lk, ttl, fn)
}

// runHeld 在已持有锁的情况下执行 fn，期间自动续期，返回后释放锁
func runHeld(ctx context.Context, lk Lock, ttl time.Duration, fn func(ctx context.Context) error) error {
	runCtx, cancel := context.WithCancelCause(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		keepAlive(runCtx, cancel, lk, ttl)
	}()

	fnErr := fn(runCtx)
	cancel(nil)
	<-done

	if errors.Is(context.Cause(runCtx), ErrNotHeld) {
		return errors.Join(fnErr, ErrNotHeld)
	}
	// ctx 可能已被取消，释放锁不受其影响
	return errors.Join(fnErr, lk.Unlock(context.WithoutCancel(ctx)))
}

// keepAlive 定时续期直到 ctx 结束；确认锁丢失时以 ErrNotHeld 取消 ctx
func keepAlive(ctx context.Context, cancel context.CancelCauseFunc, lk Lock, ttl time.Duration) {
	ticker := time.NewTicker(ttl / 3)
	defer ticker.Stop()
	lastOK := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		err := lk.Refresh(ctx, ttl)
		switch {
		case err == nil:
			lastOK = time.Now()
		case errors.Is(err, ErrNotHeld):
			cancel(ErrNotHeld)
			return
		case ctx.Err() != nil:
			return
		case time.Since(lastOK) >= ttl:
			// 网络等临时错误持续超过 ttl，锁必然已过期
			cancel(ErrNotHeld)
			return
		}
	}
}
