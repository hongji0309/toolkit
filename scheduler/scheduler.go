package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/hongji0309/toolkit/lock"
	"github.com/robfig/cron/v3"
)

const (
	// 运行锁过期时间，执行期间每 1/3 续期一次，进程崩溃时到期自动释放
	runLockTTL = 30 * time.Second
	// 调度认领的保留时间，只需覆盖各实例的时钟偏差，到期自动删除
	claimTTL = 10 * time.Minute
)

// 6 段 cron 表达式（含秒），也支持 @daily、@hourly 等快捷语法
var specParser = cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// Scheduler 定时任务调度器：持有代码中注册的全部任务，按 JobStore 提供的配置自动调度，
// 执行结果交给 Recorder 记录
type Scheduler struct {
	jobs     map[string]Job
	keys     []string // 注册顺序
	store    JobStore
	recorder Recorder
	opts     options
	cron     *cron.Cron

	ctx    context.Context // 停服时取消，正在执行的任务据此退出
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	entries map[string]*entry // 已加入调度的任务，key 为任务标识
}

// entry 已加入调度的任务
type entry struct {
	id   cron.EntryID
	spec string
}

// New 创建调度器，store 提供调度配置，recorder 记录执行结果
func New(store JobStore, recorder Recorder, opts ...Option) *Scheduler {
	o := newOptions(opts)
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		jobs:     make(map[string]Job),
		store:    store,
		recorder: recorder,
		opts:     o,
		cron:     cron.New(cron.WithParser(specParser), cron.WithLocation(o.loc)),
		ctx:      ctx,
		cancel:   cancel,
		entries:  make(map[string]*entry),
	}
}

// Register 注册任务，key 为空或重复时返回 error，在 Start 前调用
func (s *Scheduler) Register(jobs ...Job) error {
	for _, j := range jobs {
		key := j.Key()
		if key == "" {
			return fmt.Errorf("scheduler: empty key for job %q", j.Name())
		}
		if _, ok := s.jobs[key]; ok {
			return fmt.Errorf("scheduler: duplicate job key %q", key)
		}
		s.jobs[key] = j
		s.keys = append(s.keys, key)
	}
	return nil
}

// Jobs 已注册的全部任务，按注册顺序
func (s *Scheduler) Jobs() []Job {
	list := make([]Job, 0, len(s.keys))
	for _, key := range s.keys {
		list = append(list, s.jobs[key])
	}
	return list
}

// GetJob 按 key 查找已注册的任务
func (s *Scheduler) GetJob(key string) (Job, bool) {
	j, ok := s.jobs[key]
	return j, ok
}

// Start 加载任务配置并开始调度，之后按 WithReloadInterval 的间隔定时重新加载
func (s *Scheduler) Start(ctx context.Context) error {
	if err := s.reload(ctx); err != nil {
		return err
	}
	s.cron.Start()
	if s.opts.reloadInterval > 0 {
		s.wg.Go(func() {
			ticker := time.NewTicker(s.opts.reloadInterval)
			defer ticker.Stop()
			for {
				select {
				case <-s.ctx.Done():
					return
				case <-ticker.C:
					s.Refresh(s.ctx)
				}
			}
		})
	}
	return nil
}

// Stop 停止调度，取消正在执行任务的 ctx 并等待其结束
func (s *Scheduler) Stop() {
	stopped := s.cron.Stop()
	s.cancel()
	<-stopped.Done()
	s.wg.Wait()
}

// Refresh 重新加载任务配置并调整调度，修改配置后调用；失败时记录日志，原有调度保持不变
func (s *Scheduler) Refresh(ctx context.Context) {
	if err := s.reload(ctx); err != nil {
		s.opts.logger.Warn("scheduler: reload jobs failed", "error", err)
	}
}

// RunJob 手动执行任务：拿到运行锁后立即返回，任务在后台执行。
// 任务未注册时返回 ErrJobNotFound，正在执行时返回 ErrJobRunning
func (s *Scheduler) RunJob(ctx context.Context, key string) error {
	j, ok := s.jobs[key]
	if !ok {
		return ErrJobNotFound
	}
	started := make(chan struct{})
	// 拿到锁后调用方已返回，不再读取结果，留 1 个缓冲避免任务结束时阻塞
	result := make(chan error, 1)
	s.wg.Go(func() {
		result <- s.runWithLock(j, func(ctx context.Context) {
			close(started)
			s.execute(ctx, j, TriggerManual)
		})
	})
	// 拿到锁时 started 先关闭；拿不到锁时 fn 不执行，TryWithLock 立即返回
	select {
	case <-started:
		return nil
	case err := <-result:
		if errors.Is(err, lock.ErrNotAcquired) {
			return ErrJobRunning
		}
		return err
	}
}

// NextRun 按调度时区计算 cron 表达式的下一次执行时间
func (s *Scheduler) NextRun(spec string) (time.Time, error) {
	schedule, err := parseSpec(spec)
	if err != nil {
		return time.Time{}, err
	}
	return schedule.Next(time.Now().In(s.opts.loc)), nil
}

// ParseSpec 校验 cron 表达式；@every 返回 ErrUnsupportedSpec
func ParseSpec(spec string) error {
	_, err := parseSpec(spec)
	return err
}

// parseSpec 解析 cron 表达式，不支持 @every
func parseSpec(spec string) (cron.Schedule, error) {
	if strings.HasPrefix(strings.TrimSpace(spec), "@every") {
		return nil, ErrUnsupportedSpec
	}
	schedule, err := specParser.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("scheduler: parse spec %q: %w", spec, err)
	}
	return schedule, nil
}

// reload 按配置调整调度：启用且已注册的任务加入调度，表达式变化的重新加入，其余移出
func (s *Scheduler) reload(ctx context.Context) error {
	configs, err := s.store.ListJobs(ctx)
	if err != nil {
		return err
	}
	want := make(map[string]string, len(configs))
	for _, c := range configs {
		if !c.Enabled {
			continue
		}
		if _, ok := s.jobs[c.Key]; !ok {
			continue
		}
		want[c.Key] = c.Spec
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for key, e := range s.entries {
		if spec, ok := want[key]; !ok || spec != e.spec {
			s.cron.Remove(e.id)
			delete(s.entries, key)
		}
	}
	for key, spec := range want {
		if _, ok := s.entries[key]; ok {
			continue
		}
		schedule, err := parseSpec(spec)
		if err != nil {
			// 表达式保存时应已校验，解析失败时跳过该任务
			s.opts.logger.Warn("scheduler: invalid spec, job not scheduled", "job_key", key, "spec", spec, "error", err)
			continue
		}
		id := s.cron.Schedule(schedule, cron.FuncJob(s.autoRun(s.jobs[key], schedule)))
		s.entries[key] = &entry{id: id, spec: spec}
	}
	return nil
}

// autoRun 自动调度时执行：多实例时先认领本次调度，再获取运行锁，上一次未执行完时跳过
func (s *Scheduler) autoRun(j Job, schedule cron.Schedule) func() {
	return func() {
		if s.opts.claim {
			// 本次调度时间：触发时刻略晚于计划时间，从 1 秒前推算即为计划时间，各实例结果一致；
			// 认领的锁不释放，到期自动删除，同一调度时间的其他实例认领失败
			fireAt := schedule.Next(time.Now().In(s.opts.loc).Add(-time.Second))
			key := fmt.Sprintf("%sfire:%s:%d", s.opts.keyPrefix, j.Key(), fireAt.Unix())
			if _, err := s.opts.locker.TryLock(s.ctx, key, claimTTL); err != nil {
				if !errors.Is(err, lock.ErrNotAcquired) {
					s.opts.logger.Warn("scheduler: claim fire failed, skipped", "job_key", j.Key(), "error", err)
				}
				return
			}
		}
		err := s.runWithLock(j, func(ctx context.Context) {
			s.execute(ctx, j, TriggerAuto)
		})
		// 上一次未执行完（lock.ErrNotAcquired）时本次跳过；其余为加锁或释放锁出错
		if err != nil {
			s.opts.logger.Warn("scheduler: acquire or release run lock failed", "job_key", j.Key(), "error", err)
		}
	}
}

// runWithLock 获取任务运行锁后执行 fn，执行期间自动续期，结束后释放；锁被占用时返回 lock.ErrNotAcquired，fn 不执行。
// 执行期间锁丢失时 fn 的 ctx 被取消，只记日志，不返回 error
func (s *Scheduler) runWithLock(j Job, fn func(ctx context.Context)) error {
	key := s.opts.keyPrefix + "run:" + j.Key()
	err := lock.TryWithLock(s.ctx, s.opts.locker, key, runLockTTL, func(ctx context.Context) error {
		fn(ctx)
		return nil
	})
	if errors.Is(err, lock.ErrNotHeld) {
		s.opts.logger.Warn("scheduler: run lock lost during execution", "job_key", j.Key(), "error", err)
		return nil
	}
	return err
}

// execute 执行任务、记录失败日志并交给 Recorder 记录结果：panic 记为失败（Error 级别），实现 Timeouter 的任务超时后取消 ctx
func (s *Scheduler) execute(ctx context.Context, j Job, trigger Trigger) {
	if t, ok := j.(Timeouter); ok && t.Timeout() > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.Timeout())
		defer cancel()
	}

	start := time.Now()
	err := s.safeRun(ctx, j)
	if err != nil {
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			err = fmt.Errorf("%w: %w", ErrJobTimeout, err)
		case errors.Is(context.Cause(ctx), lock.ErrNotHeld):
			err = fmt.Errorf("%w: %w", ErrLockLost, err)
		}
		level := slog.LevelWarn
		if errors.Is(err, ErrJobPanic) {
			level = slog.LevelError
		}
		s.opts.logger.Log(context.Background(), level, "scheduler: job failed", "job_key", j.Key(), "trigger", int(trigger), "error", err)
	}

	rec := &Record{
		JobKey:    j.Key(),
		JobName:   j.Name(),
		Trigger:   trigger,
		StartedAt: start,
		Duration:  time.Since(start),
		Err:       err,
	}
	// 停服时 s.ctx 已取消，记录用独立的 ctx；记录失败时连同执行本身的错误一起记下
	if err := s.recorder.Record(context.Background(), rec); err != nil {
		s.opts.logger.Warn("scheduler: record result failed", "job_key", j.Key(), "error", err, "job_error", rec.Err)
	}
}

// safeRun 执行任务，panic 转为 ErrJobPanic，错误信息带上调用栈
func (s *Scheduler) safeRun(ctx context.Context, j Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v\n%s", ErrJobPanic, r, debug.Stack())
		}
	}()
	return j.Run(ctx)
}
