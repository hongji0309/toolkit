package captcha

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mojocn/base64Captcha"

	"github.com/hongji0309/toolkit/cache"
	"github.com/hongji0309/toolkit/cache/store"
)

// stringSource TypeString 的字符集，去掉了易混淆的 0/O、1/I/L
const stringSource = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// Captcha 图形验证码，图片由 base64Captcha 生成，答案存在 CacheStore 中，校验一次即作废
type Captcha struct {
	driver    base64Captcha.Driver
	store     cache.CacheStore
	ttl       int64
	keyPrefix string
}

// New 创建验证码，store 用于保存答案，多实例部署时应使用共享存储（如 Redis）
func New(store cache.CacheStore, opts ...Option) *Captcha {
	o := newOptions(opts)
	var driver base64Captcha.Driver
	switch o.typ {
	case TypeString:
		driver = base64Captcha.NewDriverString(o.height, o.width, 0,
			base64Captcha.OptionShowSlimeLine|base64Captcha.OptionShowSineLine,
			o.length, stringSource, nil, nil, nil)
	default:
		driver = base64Captcha.NewDriverDigit(o.height, o.width, o.length, 0.7, 80)
	}
	return &Captcha{
		driver:    driver,
		store:     store,
		ttl:       int64(o.ttl / time.Second),
		keyPrefix: o.keyPrefix,
	}
}

// Generate 生成验证码，返回验证码 ID 与 data URI 格式的 base64 图片，图片可直接作为 img 的 src
func (c *Captcha) Generate(ctx context.Context) (id, image string, err error) {
	id, content, answer := c.driver.GenerateIdQuestionAnswer()
	item, err := c.driver.DrawCaptcha(content)
	if err != nil {
		return "", "", err
	}
	if err := c.store.Set(ctx, c.keyPrefix+id, []byte(strings.ToLower(answer)), c.ttl); err != nil {
		return "", "", err
	}
	return id, item.EncodeB64string(), nil
}

// Verify 校验验证码，忽略首尾空格、不区分大小写；无论对错都会作废该验证码。
// 验证码不存在、已过期或答案错误返回 false；存储出错时返回 error
func (c *Captcha) Verify(ctx context.Context, id, answer string) (bool, error) {
	answer = strings.TrimSpace(answer)
	if id == "" || answer == "" {
		return false, nil
	}
	key := c.keyPrefix + id
	stored, err := c.store.Get(ctx, key)
	if errors.Is(err, store.ErrCacheMiss) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := c.store.Delete(ctx, key); err != nil {
		return false, err
	}
	return string(stored) == strings.ToLower(answer), nil
}
