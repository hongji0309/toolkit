# toolkit

常用 Go 工具包集合，每个子目录是一个独立的工具包。

```bash
go get github.com/hongji0309/toolkit
```

## 工具列表

| 包 | 说明 |
| --- | --- |
| [cache](cache) | 缓存存储接口 `CacheStore`，提供内存（`store.MemoryCacheStore`）和 Redis（`store.RedisCacheStore`）两种实现 |
| [upload](upload) | 文件/图片上传，`Uploader` 负责大小、类型校验和生成存储路径，存储支持本地（`storage.Local`）和阿里云 OSS（`storage.OSS`） |
| [excel](excel) | 列表数据导出为 xlsx，`Column[T]` 定义表头和取值，`ExportPaged` 分页查询并流式写入 |

## cache 使用示例

```go
import (
	"github.com/hongji0309/toolkit/cache"
	"github.com/hongji0309/toolkit/cache/store"
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
	"github.com/hongji0309/toolkit/upload"
	"github.com/hongji0309/toolkit/upload/storage"
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

## excel 使用示例

```go
import "github.com/hongji0309/toolkit/excel"

// 列定义，T 是列表的行类型
var userCols = []excel.Column[*model.User]{
	{Header: "用户ID", Width: 12, Value: func(u *model.User) any { return u.ID }},
	{Header: "姓名", Value: func(u *model.User) any { return u.Name }},
	{Header: "状态", Value: func(u *model.User) any {
		if u.Status == 1 {
			return "启用"
		}
		return "禁用"
	}},
	{Header: "注册时间", Value: func(u *model.User) any { return u.CreatedAt }},
}

// 数据已经查好
err := excel.Export(w, userCols, users, excel.WithSheet("用户"))

// 分页导出：复用列表接口的查询，每页条数由回调决定，返回空列表时结束
func (h *Handler) ExportUsers(c *gin.Context) {
	var req ListReq
	_ = c.ShouldBindQuery(&req)

	// DownloadWriter 在成功输出时才设置下载响应头，失败时仍可返回 JSON
	err := excel.ExportPaged(c, excel.DownloadWriter(c.Writer, "用户列表.xlsx"), userCols, func(ctx context.Context, page int) ([]*model.User, error) {
		req.Page, req.PageSize = page, 100
		list, _, err := h.svc.List(ctx, req)
		return list, err
	}, excel.WithSheet("用户"), excel.WithMaxRows(100000))
	if err != nil {
		c.JSON(500, gin.H{"msg": err.Error()})
	}
}
```

说明：

- 表头来自 `cols`，没有数据时也会输出表头；首行加粗并冻结。
- 基于 excelize `StreamWriter` 按行写入，内存占用不随数据量增长；单个 sheet 写满 1048575 行后自动续表为 `用户_2`。
- `Value` 返回 nil、nil 指针或零值时间时单元格为空；`time.Time` 默认格式为 `2006-01-02 15:04:05`，可用 `WithTimeLayout` 修改。
- 出错时不会向 `w` 写入任何内容。xlsx 是 zip 格式，文件在全部数据写完后才一次性输出。
