package locker

import "time"

// options 锁实现的公共配置
type options struct {
	retryInterval time.Duration
	keyPrefix     string
}

// Option 锁实现的配置项
type Option func(*options)

// WithRetryInterval 设置 Lock 等待时的重试间隔，默认 100 毫秒；不大于 0 时忽略
func WithRetryInterval(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.retryInterval = d
		}
	}
}

// WithKeyPrefix 设置 key 前缀，默认 lock:
func WithKeyPrefix(prefix string) Option {
	return func(o *options) { o.keyPrefix = prefix }
}

func newOptions(opts []Option) options {
	o := options{
		retryInterval: 100 * time.Millisecond,
		keyPrefix:     "lock:",
	}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
