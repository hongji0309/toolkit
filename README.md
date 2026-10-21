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
