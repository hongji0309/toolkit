# toolkit

常用 Go 工具包集合，每个子目录是一个独立的工具包。

```bash
go get gitee.com/hongjizhong/toolkit
```

## 工具列表

| 包 | 说明 |
| --- | --- |
| [cache](cache) | 缓存存储接口 `CacheStore`，提供内存（`store.MemoryCacheStore`）和 Redis（`store.RedisCacheStore`）两种实现 |
| [upload](upload) | 文件/图片上传，`Uploader` 负责大小、类型校验和生成存储路径，存储支持本地（`storage.Local`）和阿里云 OSS（`storage.OSS`） |

## cache 使用示例

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

## upload 使用示例

```go
import (
	"gitee.com/hongjizhong/toolkit/upload"
	"gitee.com/hongjizhong/toolkit/upload/storage"
)

// 本地存储
st, err := storage.NewLocal(storage.LocalConfig{
	Root:    "/data/uploads",
	BaseURL: "https://static.example.com/uploads",
})

// 或阿里云 OSS
st, err := storage.NewOSS(storage.OSSConfig{
	Region:          "cn-hangzhou",
	Bucket:          "my-bucket",
	AccessKeyID:     cfg.AccessKeyID,
	AccessKeySecret: cfg.AccessKeySecret,
	Domain:          "https://cdn.example.com",
})

images := upload.NewUploader(st, upload.ImageOptions) // 图片，最大 10MB
files := upload.NewUploader(st, upload.FileOptions)   // 普通文件，最大 100MB

// gin 中使用
fh, err := c.FormFile("file")
obj, err := images.UploadFileHeader(ctx, fh)
switch {
case errors.Is(err, upload.ErrFileTooLarge), errors.Is(err, upload.ErrTypeNotAllowed), errors.Is(err, upload.ErrEmptyFile):
	// 返回 400
}
// 业务表保存 obj.Key，返回给前端 obj.URL
```

说明：

- 文件类型按内容识别（`http.DetectContentType`），不信任扩展名和客户端传的 Content-Type。
- HTML、XML（含 SVG）始终禁止上传，`.html`、`.php` 等扩展名不会出现在存储路径中，避免 XSS 和脚本执行。
- 存储路径格式为 `{Dir}/{yyyy}/{mm}/{dd}/{随机ID}{扩展名}`，原始文件名只保存在 `Object.Filename`。
- OSS 使用阿里云 V2 SDK（`alibabacloud-oss-go-sdk-v2`）。长度未知的流由 SDK 分片流式上传（默认每片 6MB、3 并发），不落盘；上传失败会自动取消分片任务。

## 两个 bucket 使用示例（公开 + 私有）

按访问级别拆成两个 bucket，按业务分目录。新增业务只需要登记一个类别，不需要新建 bucket：

```
company-public（公共读，接 CDN）     products/、banners/、avatars/ ...
company-private（私有，阻止公共访问） id-cards/、contracts/、invoices/ ...
```

toolkit 只负责存储和校验，"哪个类别放哪个 bucket"属于业务规则，由调用方定义，例如：

```go
package file

import (
	"context"
	"fmt"
	"mime/multipart"
	"time"

	"gitee.com/hongjizhong/toolkit/upload"
	"gitee.com/hongjizhong/toolkit/upload/storage"
)

// Category 文件类别
type Category struct {
	storage  upload.Storage
	uploader *upload.Uploader
}

type Registry struct {
	categories map[string]Category
}

func NewRegistry(cfg Config) (*Registry, error) {
	// 公开 bucket：返回 CDN 直链
	publicStore, err := storage.NewOSS(storage.OSSConfig{
		Region:          cfg.Region,
		Bucket:          "company-public",
		AccessKeyID:     cfg.AccessKeyID,
		AccessKeySecret: cfg.AccessKeySecret,
		Domain:          "https://cdn.example.com",
	})
	if err != nil {
		return nil, err
	}

	// 私有 bucket：返回带签名的临时地址
	privateStore, err := storage.NewOSS(storage.OSSConfig{
		Region:          cfg.Region,
		Bucket:          "company-private",
		AccessKeyID:     cfg.AccessKeyID,
		AccessKeySecret: cfg.AccessKeySecret,
		Private:         true,
		SignExpire:      5 * time.Minute,
	})
	if err != nil {
		return nil, err
	}

	r := &Registry{categories: make(map[string]Category)}
	// 新增业务在这里登记一行，选择放公开还是私有 bucket
	r.register("product", publicStore, upload.Options{Dir: "products", MaxSize: 10 << 20, AllowedTypes: []string{"image/*"}})
	r.register("banner", publicStore, upload.Options{Dir: "banners", MaxSize: 5 << 20, AllowedTypes: []string{"image/*"}})
	r.register("id_card", privateStore, upload.Options{Dir: "id-cards", MaxSize: 5 << 20, AllowedTypes: []string{"image/jpeg", "image/png"}})
	r.register("contract", privateStore, upload.Options{Dir: "contracts", MaxSize: 20 << 20, AllowedTypes: []string{"application/pdf"}})
	return r, nil
}

func (r *Registry) register(name string, s upload.Storage, opts upload.Options) {
	r.categories[name] = Category{storage: s, uploader: upload.NewUploader(s, opts)}
}

func (r *Registry) get(name string) (Category, error) {
	c, ok := r.categories[name]
	if !ok {
		return Category{}, fmt.Errorf("unknown file category %q", name)
	}
	return c, nil
}

// Upload 业务表保存 category 和 obj.Key，不要保存 URL
func (r *Registry) Upload(ctx context.Context, category string, fh *multipart.FileHeader) (*upload.Object, error) {
	c, err := r.get(category)
	if err != nil {
		return nil, err
	}
	return c.uploader.UploadFileHeader(ctx, fh)
}

// URL 公开类别返回直链，私有类别返回带签名的临时地址；私有文件调用前先校验当前用户的查看权限
func (r *Registry) URL(ctx context.Context, category, key string) (string, error) {
	c, err := r.get(category)
	if err != nil {
		return "", err
	}
	return c.storage.URL(ctx, key)
}
```

约定：

- 新增类别默认放私有 bucket，只有明确需要"未登录的外部用户也能看"时才放公开 bucket。
- 业务表保存 `category + key`。类别从私有改为公开、更换 CDN 域名时，只需修改登记并迁移文件，业务数据不变。
- 签名有效期按 `storage.OSS` 实例设置。私有 bucket 中某些类别需要不同有效期时（如证件照 5 分钟、合同 1 小时），对同一个 `company-private` 再创建一个 `SignExpire` 不同的实例给该类别使用，bucket 仍然只有两个。
- 证件照、合同等敏感文件每次展示时现生成签名地址；更严格的场景可以不返回 URL，由后端校验权限后通过 `Storage.Get` 读取并返回文件内容。
