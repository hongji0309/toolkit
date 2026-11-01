package scheduler

import (
	"log/slog"
	"time"

	"github.com/hongji0309/toolkit/lock"
	"github.com/hongji0309/toolkit/lock/locker"
)

// Option 调度器选项
type Option func(*options)

type options struct {
	locker         lock.Locker
	claim          bool
	loc            *time.Location
	logger         *slog.Logger
	keyPrefix      string
	reloadInterval time.Duration
}

func newOptions(opts []Option) options {
	o := options{
		locker:         locker.NewMemoryLocker(),
		loc:            time.Local,
		logger:         slog.Default(),
		keyPrefix:      "scheduler:",
		reloadInterval: 30 * time.Second,
	}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// WithLocker 使用分布式锁（如 Redis），多实例部署时传入：同一任务只有一个实例在执行，
// 每次自动调度只有一个实例认领；不传时用进程内锁，适用于单实例
func WithLocker(l lock.Locker) Option {
	return func(o *options) {
		o.locker = l
		o.claim = true
	}
}

// WithLocation 调度使用的时区，cron 表达式按此时区解释，默认 time.Local
func WithLocation(loc *time.Location) Option {
	return func(o *options) { o.loc = loc }
}

// WithLogger 调度器自身的日志（加锁失败、执行失败、panic 等），默认 slog.Default()
func WithLogger(l *slog.Logger) Option {
	return func(o *options) { o.logger = l }
}

// WithKeyPrefix 锁 key 的前缀，默认 "scheduler:"；多个系统共用同一个锁存储时用于区分
func WithKeyPrefix(prefix string) Option {
	return func(o *options) { o.keyPrefix = prefix }
}

// WithReloadInterval 定时重新读取 JobStore 的间隔，默认 30 秒，多实例时其他实例修改的配置据此生效；
// 小于等于 0 时不定时读取，只在 Start、Refresh 时读取
func WithReloadInterval(d time.Duration) Option {
	return func(o *options) { o.reloadInterval = d }
}
