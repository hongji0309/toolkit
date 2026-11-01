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
| [captcha](captcha) | 图形验证码，基于 base64Captcha 生成图片，答案存在 `cache.CacheStore` 中，校验一次即作废 |
| [lock](lock) | 按 key 加锁，接口 `Locker`，提供 Redis（`locker.RedisLocker`）和内存（`locker.MemoryLocker`）两种实现，`WithLock` / `TryWithLock` 自动续期 |
| [scheduler](scheduler) | 定时任务调度，任务在代码中注册，执行时间与启停由调用方的配置（`JobStore`）决定，执行结果交给 `Recorder`；基于 robfig/cron，用 `lock` 防重复执行，支持多实例 |

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

## captcha 使用示例

```go
import (
	"github.com/hongji0309/toolkit/cache/store"
	"github.com/hongji0309/toolkit/captcha"
)

// 多实例部署时用 store.NewRedisCacheStore，保证生成与校验落在不同实例时也能取到答案
c := captcha.New(store.NewMemoryCacheStore(time.Minute),
	captcha.WithType(captcha.TypeDigit), // 或 captcha.TypeString（数字字母）
	captcha.WithLength(4),
	captcha.WithSize(240, 80),
	captcha.WithTTL(3*time.Minute),
)

// 获取验证码接口：返回给前端，image 可直接作为 img 的 src
id, image, err := c.Generate(ctx)

// 登录时校验
ok, err := c.Verify(ctx, req.CaptchaID, req.CaptchaCode)
if err != nil {
	// 存储出错
}
if !ok {
	// 验证码错误或已过期，前端需重新获取
}
```

说明：

- 验证码无论校验对错都会作废，同一个 ID 不能重复尝试。
- `TypeString` 去掉了易混淆的 0/O、1/I/L，校验忽略首尾空格、不区分大小写。
- 校验时先读后删，并发提交同一个 ID 时有极小概率都通过；用于登录等场景足够。

## lock 使用示例

```go
import (
	"github.com/hongji0309/toolkit/lock"
	"github.com/hongji0309/toolkit/lock/locker"
)

// 多实例部署用 Redis；单实例、没有 Redis 时用内存实现，业务代码不用改
var l lock.Locker = locker.NewRedisLocker(rdb, locker.WithKeyPrefix("lock:"))
// var l lock.Locker = locker.NewMemoryLocker()

// 推荐：加锁执行，执行期间自动续期，结束后自动释放
err := lock.WithLock(ctx, l, "order:"+orderNo, 10*time.Second, func(ctx context.Context) error {
	return h.svc.Pay(ctx, orderNo)
})
if errors.Is(err, lock.ErrNotHeld) {
	// 执行期间锁丢失，ctx 已被取消
}

// 拿不到锁就放弃：只尝试一次，同样自动续期、自动释放（如定时任务上一次未执行完时跳过本次）
err = lock.TryWithLock(ctx, l, "job:"+jobKey, 30*time.Second, func(ctx context.Context) error {
	return job.Run(ctx)
})
if errors.Is(err, lock.ErrNotAcquired) {
	// 锁被占用，fn 未执行
}

// 防重复提交：拿不到锁直接返回
lk, err := l.TryLock(ctx, "submit:"+userID, 5*time.Second)
if errors.Is(err, lock.ErrNotAcquired) {
	// 返回"操作太频繁"
}
defer lk.Unlock(context.WithoutCancel(ctx))

// 阻塞等待，最多等 3 秒
waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
defer cancel()
lk, err = l.Lock(waitCtx, "job:sync", time.Minute)
```

说明：

- 加锁时生成随机 token，`Unlock`、`Refresh` 校验 token 后才操作，不会误删别人的锁；锁已过期时返回 `lock.ErrNotHeld`。
- ttl 最小为 `lock.MinTTL`（1 秒），小于时返回 `lock.ErrInvalidTTL`（如 `lock: ttl must be at least 1s, got 10ns`，多半是漏写了 `time.Second`）。ttl 只是崩溃时的兜底，正常由 `Unlock` 释放，宁长勿短。
- `TryLock` / `Lock` 不会自动续期，ttl 要大于业务执行时间；执行时间不确定时用 `WithLock` / `TryWithLock`，每 ttl/3 续期一次。两者区别只在加锁：`WithLock` 阻塞等待，`TryWithLock` 只尝试一次，锁被占用时返回 `lock.ErrNotAcquired`。
- Redis 实现是单节点锁，主从切换时可能丢锁，适合防重复提交、定时任务防并发；资金等强一致场景要在数据库层再做幂等校验。
- 内存实现只在当前进程内互斥，多实例部署时不能用。

## scheduler 使用示例

```go
import (
	"github.com/hongji0309/toolkit/lock/locker"
	"github.com/hongji0309/toolkit/scheduler"
)

// 任务：实现 Key、Name、Run；需要超时控制时再实现 Timeout() time.Duration
type CleanLogJob struct{ repo LogRepo }

func (j *CleanLogJob) Key() string  { return "clean_log" } // 唯一标识，上线后不可修改
func (j *CleanLogJob) Name() string { return "清理过期日志" }
func (j *CleanLogJob) Run(ctx context.Context) error {
	return j.repo.DeleteBefore(ctx, time.Now().AddDate(0, 0, -30))
}

// store 返回各任务的调度配置（如从数据库表读取），recorder 记录执行结果（如写执行日志表）
s := scheduler.New(store, recorder,
	scheduler.WithLocation(loc),     // cron 表达式按此时区解释，默认 time.Local
	scheduler.WithLogger(logger),    // *slog.Logger，默认 slog.Default()
	scheduler.WithLocker(locker.NewRedisLocker(rdb, locker.WithKeyPrefix("lock:"))), // 多实例部署时传入
)
if err := s.Register(&CleanLogJob{repo: repo}); err != nil {
	// key 为空或重复
}
if err := s.Start(ctx); err != nil {
	// 首次读取 JobStore 失败
}
defer s.Stop() // 停止调度，取消并等待正在执行的任务

s.Refresh(ctx)                         // 修改配置后立即重新加载
err := s.RunJob(ctx, "clean_log")      // 手动执行：拿到运行锁后立即返回，任务在后台执行
if errors.Is(err, scheduler.ErrJobRunning) {
	// 任务正在执行
}
err = scheduler.ParseSpec("0 0 2 * * *") // 校验表达式，保存配置前调用
next, err := s.NextRun("0 0 2 * * *")    // 下一次执行时间
```

说明：

- cron 表达式为 6 段含秒（`秒 分 时 日 月 周`），支持 `@daily`、`@hourly` 等写法；不支持 `@every`（返回 `scheduler.ErrUnsupportedSpec`），间隔调度从各实例启动时计时，多实例下无法对齐。
- `JobStore.ListJobs` 在 `Start`、`Refresh` 时及每隔 `WithReloadInterval`（默认 30 秒，小于等于 0 时关闭）读取一次，启用且已注册的任务加入调度，表达式变化的重建，其余移出。
- 同一任务不并发执行：执行前用 `lock.TryWithLock` 获取运行锁并自动续期，上一次未执行完时本次跳过，`RunJob` 返回 `ErrJobRunning`。传入 `WithLocker` 时还会按调度时间认领，各实例时钟有偏差时同一次调度也只执行一次；不传时用内存锁，只能单实例部署。
- 执行失败、超时（`ErrJobTimeout`）、运行锁丢失（`ErrLockLost`）、panic（`ErrJobPanic`，错误信息带调用栈）都通过 `Record.Err` 交给 `Recorder`，同时由调度器记录日志（失败 Warn、panic Error）；后台重新加载、加锁失败等调用方拿不到的错误也由调度器记录。
- 停服、超时、运行锁丢失时任务的 ctx 被取消，任务内访问数据库要使用传入的 ctx。
