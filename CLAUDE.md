# toolkit

供多个业务项目复用的 Go 工具包集合，模块路径 `github.com/hongji0309/toolkit`，Go 1.25。业务项目通过打 tag 发版后 `go get` 引用（如 scaffold-server），改动导出 API 前要考虑已有调用方。

## 包结构

| 包 | 说明 |
| --- | --- |
| `cache` | 缓存接口 `CacheStore`（ttl 单位秒）；实现在 `cache/store`：`MemoryCacheStore`、`RedisCacheStore`，两者行为必须保持一致 |
| `upload` | `Uploader` 负责大小、类型校验和生成存储路径；`Storage` 接口实现在 `upload/storage`：`Local`、`OSS`（阿里云 V2 SDK） |
| `excel` | 基于 excelize 的 xlsx 工具。`excel.go` 放公共的 `Column[T]`，`export.go` 放导出（`Export`、`ExportPaged`、`DownloadWriter`）；以后的导入放 `import.go`，不另建包 |
| `captcha` | 图形验证码，图片由 base64Captcha 生成（不对外暴露其类型），答案存在 `cache.CacheStore`，校验一次即作废 |
| `lock` | 按 key 加锁，接口 `Locker` / `Lock` 和 `WithLock`、`TryWithLock`（自动续期）在包根；实现在 `lock/locker`：`RedisLocker`、`MemoryLocker`，两者行为必须保持一致 |
| `scheduler` | 定时任务调度，`job.go` 放调用方实现的接口与数据结构（`Job`、`JobStore`、`Recorder` 等）和哨兵错误，`options.go` 放选项，`scheduler.go` 放调度与执行；依赖 robfig/cron（不对外暴露其类型）和 `lock` |

接口定义放在包根，具体实现放子包（`cache/store`、`upload/storage`、`lock/locker`），实现文件里用 `var _ Iface = (*Impl)(nil)` 做编译期检查。

## 命令

```bash
go vet ./...
gofmt -l .
```

项目不写单元测试，改动后以 `go vet` 和编译通过为准；需要验证行为时在临时目录写一次性脚本，不提交到仓库。AccessKey 等凭据不要写入仓库内的任何文件。

## 代码约定

- 注释、文档、提交信息使用中文；导出标识符都要有注释，以标识符名开头。
- 错误：包级哨兵错误用 `errors.New("包名: 小写英文描述")`，如 `upload: file too large`、`excel: too many rows`；包装错误用 `fmt.Errorf("excel: fetch page %d: %w", page, err)`。调用方用 `errors.Is` 判断。
- 配置：可选参数用函数式选项（`type Option func(*options)` + `WithXxx`），默认值集中在 `newOptions`；结构较多的配置用 `XxxConfig` 结构体 + `NewXxx(cfg) (*Xxx, error)`，在构造函数里校验必填项。
- 对外 API 优先接收 `io.Reader` / `io.Writer` 和 `context.Context`，不向调用方暴露第三方库类型（如不返回 `*excelize.File`），资源在包内部释放。
- 不引入不必要的依赖；新依赖只在确有需要时添加，并执行 `go mod tidy`。

## excel 包设计要点

- `ExportPaged` 的回调只接收页码，每页条数由调用方决定，返回空列表时结束。不要改回"返回条数少于 size 即结束"，业务接口常有每页上限（如 `MaxPageSize = 100`），会导致静默少导数据。
- 出错时不向 `w` 写任何字节，`DownloadWriter` 在首次写入时才设置下载响应头，保证 HTTP 场景失败时仍能返回 JSON。
- 单个 sheet 写满 1048575 行自动续表为 `名称_2`；nil、nil 指针、零值时间输出空单元格。

## scheduler 包设计要点

- 不支持 `@every`：间隔调度从各实例启动时计时，多实例下无法按调度时间认领。
- 防重复执行分两层：运行锁（`<前缀>run:<key>`，`TryWithLock` 续期）保证同一任务不并发；传入 `WithLocker` 时再按调度时间认领（`<前缀>fire:<key>:<时间戳>`，不释放、到期删除），防止时钟偏差导致同一次调度执行两次。内存锁不开启认领（条目不会被清理）。
- `RunJob` 同步获取锁、异步执行：拿到锁时立即返回，拿不到返回 `ErrJobRunning`，不阻塞调用方。
- 日志统一由调度器记录（执行失败 Warn、panic Error），不提供开关；`WithLogger` 只决定输出到哪里。
- `Stop` 先停止调度，再取消任务 ctx，最后等待正在执行的任务结束。

## 文档

新增或修改包时同步更新 `README.md` 的工具列表和使用示例。
