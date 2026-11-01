package scheduler

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrJobNotFound 任务未注册
	ErrJobNotFound = errors.New("scheduler: job not found")
	// ErrJobRunning 任务正在执行（本实例或其他实例持有运行锁）
	ErrJobRunning = errors.New("scheduler: job is running")
	// ErrUnsupportedSpec 不支持的表达式：@every 间隔调度从各实例启动时开始计时，多实例下无法对齐调度时间
	ErrUnsupportedSpec = errors.New("scheduler: @every is not supported")
	// ErrJobTimeout 任务执行超过 Timeouter 声明的时长，出现在 Record.Err 中
	ErrJobTimeout = errors.New("scheduler: job timed out")
	// ErrLockLost 任务执行期间运行锁丢失，出现在 Record.Err 中
	ErrLockLost = errors.New("scheduler: run lock lost")
	// ErrJobPanic 任务执行时 panic，出现在 Record.Err 中
	ErrJobPanic = errors.New("scheduler: job panicked")
)

// Job 定时任务，执行时间与启停由 JobStore 提供的配置决定
type Job interface {
	// Key 任务唯一标识，上线后不可修改
	Key() string
	// Name 任务名称
	Name() string
	// Run 执行任务，停服、超时或运行锁丢失时 ctx 被取消
	Run(ctx context.Context) error
}

// Timeouter 任务需要超时控制时实现，执行超过该时长后取消 ctx
type Timeouter interface {
	Timeout() time.Duration
}

// JobConfig 任务的调度配置
type JobConfig struct {
	Key     string // 任务标识，对应 Job.Key()
	Spec    string // cron 表达式，6 段含秒
	Enabled bool   // 是否启用
}

// JobStore 提供任务的调度配置，调度器启动时、Refresh 时及每隔 WithReloadInterval 设置的间隔读取一次
type JobStore interface {
	ListJobs(ctx context.Context) ([]JobConfig, error)
}

// Trigger 触发方式
type Trigger int

const (
	TriggerAuto   Trigger = 1 // 自动调度
	TriggerManual Trigger = 2 // 手动执行
)

// Record 一次任务执行的结果
type Record struct {
	JobKey    string        // 任务标识
	JobName   string        // 执行时的任务名称
	Trigger   Trigger       // 触发方式
	StartedAt time.Time     // 开始时间
	Duration  time.Duration // 执行耗时
	// Err 失败原因，nil 表示成功；超时、运行锁丢失、panic 时可用 errors.Is 判断 ErrJobTimeout、ErrLockLost、ErrJobPanic
	Err error
}

// Recorder 记录任务执行结果，每次执行结束后调用一次；返回的 error 只记日志，不影响调度
type Recorder interface {
	Record(ctx context.Context, r *Record) error
}
