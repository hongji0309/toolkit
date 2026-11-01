package captcha

import "time"

// Type 验证码字符类型
type Type int

const (
	// TypeDigit 纯数字
	TypeDigit Type = iota
	// TypeString 数字字母，去掉了易混淆的 0/O、1/I/L，校验时不区分大小写
	TypeString
)

// options 验证码配置
type options struct {
	typ       Type
	width     int
	height    int
	length    int
	ttl       time.Duration
	keyPrefix string
}

// Option 验证码配置项
type Option func(*options)

// WithType 设置字符类型，默认 TypeDigit
func WithType(t Type) Option {
	return func(o *options) { o.typ = t }
}

// WithSize 设置图片宽高（像素），默认 240×80；任一值不大于 0 时忽略
func WithSize(width, height int) Option {
	return func(o *options) {
		if width > 0 && height > 0 {
			o.width, o.height = width, height
		}
	}
}

// WithLength 设置字符个数，默认 4；不大于 0 时忽略
func WithLength(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.length = n
		}
	}
}

// WithTTL 设置有效期，默认 3 分钟；小于 1 秒时忽略
func WithTTL(ttl time.Duration) Option {
	return func(o *options) {
		if ttl >= time.Second {
			o.ttl = ttl
		}
	}
}

// WithKeyPrefix 设置缓存 key 前缀，默认 captcha:
func WithKeyPrefix(prefix string) Option {
	return func(o *options) { o.keyPrefix = prefix }
}

func newOptions(opts []Option) options {
	o := options{
		typ:       TypeDigit,
		width:     240,
		height:    80,
		length:    4,
		ttl:       3 * time.Minute,
		keyPrefix: "captcha:",
	}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
