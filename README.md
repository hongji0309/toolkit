# toolkit

常用 Go 工具包集合，每个子目录是一个独立的工具包。

```bash
go get gitee.com/hongjizhong/toolkit
```

## 工具列表

| 包 | 说明 |
| --- | --- |
| [cache](cache) | 缓存存储接口 `CacheStore`，提供内存（`store.MemoryCacheStore`）和 Redis（`store.RedisCacheStore`）两种实现 |

## 使用示例

```go
import (
	"gitee.com/hongjizhong/toolkit/cache"
	"gitee.com/hongjizhong/toolkit/cache/store"
)

var c cache.CacheStore = store.NewMemoryCacheStore(time.Minute)

_ = c.Set(ctx, "key", []byte("value"), 60)
v, err := c.Get(ctx, "key")
if errors.Is(err, store.ErrCacheMiss) {
	// 未命中
}
```
