package scheduler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aether-cockpit/internal/core/apppaths"
	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/core/pool"
	"aether-cockpit/internal/provider"
)

// Options 调度器配置。
type Options struct {
	// StatePath 签到状态落盘路径，空表示纯内存。
	StatePath string
	// TickInterval 调度主循环间隔。
	TickInterval time.Duration
	// TaskTimeout 单个任务超时。
	TaskTimeout time.Duration
	// CronJitter 定时触发的随机偏移上限（避免所有账号同一秒打卡）。
	CronJitter time.Duration
	// WakeThreshold 两次 tick 的实际间隔超过该值即判定为"系统刚唤醒"。
	WakeThreshold time.Duration
	// QuotaRefresh 额度自动刷新间隔，0 表示禁用。
	//
	// 为什么必须有它：账号池的加权路由依赖 "剩余额度 × 临期权重"，
	// 而额度只有签到后才更新一次。不刷新的话所有账号的剩余额度都是未知，
	// 加权就退化成纯轮询，"优先消耗即将过期积分"完全失效。
	QuotaRefresh time.Duration
	// QuotaRefreshInitialDelay 启动后首次刷新的延迟，避免与启动时的其他请求撞车。
	QuotaRefreshInitialDelay time.Duration
}

// DefaultOptions 返回保守的默认配置。
func DefaultOptions() Options {
	return Options{
		StatePath:                apppaths.Data("checkin.json"),
		TickInterval:             20 * time.Second,
		TaskTimeout:              90 * time.Second,
		CronJitter:               15 * time.Minute,
		WakeThreshold:            3 * time.Minute,
		QuotaRefresh:             30 * time.Minute,
		QuotaRefreshInitialDelay: 45 * time.Second,
	}
}

// Job 是一个被调度的签到任务。
type Job struct {
	ProviderID string
	TaskID     string
	Title      string
	DependsOn  []string
	Optional   bool

	schedule *Schedule
	nextRun  time.Time
	lastRun  time.Time
	lastRes  string
}

// JobStatus 是任务对外暴露的快照。
type JobStatus struct {
	ProviderID string `json:"provider_id"`
	TaskID     string `json:"task_id"`
	Title      string `json:"title"`
	Cron       string `json:"cron"`
	NextRun    int64  `json:"next_run"`
	LastRun    int64  `json:"last_run"`
	LastResult string `json:"last_result,omitempty"`
	Pending    int    `json:"pending"`
	// TodayDone / TodayTotal 让 UI 能直接显示“今日已完成”，
	// 不必等用户重复点击后才从 last_result 猜。
	TodayDone  int      `json:"today_done"`
	TodayTotal int      `json:"today_total"`
	Optional   bool     `json:"optional,omitempty"`
	DependsOn  []string `json:"depends_on,omitempty"`
}

// Scheduler 是签到调度引擎。
//
// 分两层：
//   - Job 层：per-task 的 cron，只负责"什么时候该看一眼"（唤醒源）；
//   - DAG 层：每个渠道一张依赖图，负责"按什么顺序执行"。
//
// 这么分层的原因：cron 表达能力有限（只表达时间），DAG 表达能力更强（表达依赖与并发），
// 但两者缺一不可——没有 cron 就不知道该在什么时候检查，没有 DAG 就无法保证
// "刷新 Token 之后再签到"。
type Scheduler struct {
	opts Options
	pool *pool.Manager
	st   *State
	logf func(string, ...any)

	mu   sync.Mutex
	jobs []*Job
	// dags 每个渠道一张依赖图。
	dags map[string]*DAG
	// running 标记"某账号正在跑任务图"，避免同一账号被重复入队。
	running sync.Map
	// checkinOn 是渠道级签到开关（设置页的"每日签到"）；nil 表示全部放行。
	checkinOn atomic.Pointer[func(string) bool]

	queues map[string]*providerQueue
	qmu    sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	startedAt time.Time
	lastTick  time.Time
	wakeCount int
	saveStop  func()
}

// New 创建调度器。
func New(opts Options, p *pool.Manager, logf func(string, ...any)) *Scheduler {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if opts.TickInterval <= 0 {
		opts.TickInterval = 20 * time.Second
	}
	if opts.TaskTimeout <= 0 {
		opts.TaskTimeout = 90 * time.Second
	}
	if opts.WakeThreshold <= 0 {
		opts.WakeThreshold = 3 * time.Minute
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		opts:      opts,
		pool:      p,
		st:        NewState(opts.StatePath),
		logf:      logf,
		dags:      make(map[string]*DAG, 8),
		queues:    make(map[string]*providerQueue, 8),
		ctx:       ctx,
		cancel:    cancel,
		startedAt: time.Now(),
	}
}

// State 暴露签到状态（供 UI 查询）。
func (s *Scheduler) State() *State { return s.st }

// SetCheckinOn 注入渠道级签到开关（设置页的"每日签到"）。
// fn 返回 false 时 cron 触发不展开；手动触发仍走 RunProvider/RunAccount。
func (s *Scheduler) SetCheckinOn(fn func(string) bool) {
	if fn == nil {
		s.checkinOn.Store(nil)
		return
	}
	s.checkinOn.Store(&fn)
}

func (s *Scheduler) checkinAllowed(providerID string) bool {
	fn := s.checkinOn.Load()
	return fn == nil || (*fn)(providerID)
}

// Start 构建任务表并启动调度循环。
func (s *Scheduler) Start() {
	s.buildJobs()
	s.lastTick = time.Now()

	s.wg.Add(1)
	go s.loop()

	// 额度刷新循环：让加权路由的"临期优先"真正生效。
	if s.opts.QuotaRefresh > 0 {
		s.wg.Add(1)
		go s.quotaLoop()
	}

	// 状态每 30 秒落盘一次，保证异常退出时最多丢 30 秒记录。
	if s.opts.StatePath != "" {
		stop := make(chan struct{})
		s.saveStop = func() { close(stop) }
		go func() {
			t := time.NewTicker(30 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-stop:
					_ = s.st.Save()
					return
				case <-t.C:
					_ = s.st.Save()
				}
			}
		}()
	}

	s.logf("[SCHED] 已加载 %d 个签到任务", len(s.jobs))
}

// Stop 停止调度并落盘。
func (s *Scheduler) Stop() {
	s.cancel()
	s.qmu.Lock()
	for _, q := range s.queues {
		q.Stop()
	}
	s.qmu.Unlock()
	s.wg.Wait()
	if s.saveStop != nil {
		s.saveStop()
	}
	_ = s.st.Save()
}

// buildJobs 从各渠道的 Manifest 构建任务表。
func (s *Scheduler) buildJobs() {
	now := time.Now()
	var jobs []*Job
	dags := make(map[string]*DAG, 8)

	for _, m := range provider.AllManifests() {
		p, ok := provider.Get(m.ID)
		if !ok {
			continue
		}
		if _, ok := provider.AsCheckIn(p); !ok {
			continue // 没实现签到能力的渠道直接跳过
		}

		dag := NewDAG()

		for _, t := range m.Tasks {
			cron := t.CronDefault
			if cron == "" {
				cron = "0 9 * * *" // 兜底：每天 9 点
			}
			sch, err := ParseCron(cron)
			if err != nil {
				s.logf("[SCHED] %s/%s 的 cron 表达式非法（%q）: %v", m.ID, t.TaskID, cron, err)
				continue
			}
			base := sch.Next(now)
			// 抖动种子用"渠道+任务"，保证重启后偏移稳定可复现。
			next := Jitter(base, s.opts.CronJitter, m.ID+"|"+t.TaskID)

			// Job 层：只负责 cron 唤醒。
			jobs = append(jobs, &Job{
				ProviderID: m.ID,
				TaskID:     t.TaskID,
				Title:      t.Title,
				DependsOn:  t.DependsOn,
				Optional:   t.Optional,
				schedule:   sch,
				nextRun:    next,
			})

			// DAG 层：负责执行顺序。Run 在执行时按账号绑定。
			// NotBefore 携带该任务的计划时刻，避免 9:00 触发时把 10:00 的任务也跑了。
			n := &Node{
				ID:        t.TaskID,
				Title:     t.Title,
				DependsOn: t.DependsOn,
				Optional:  t.Optional,
				NotBefore: hhmmFromCron(cron),
				Timeout:   s.opts.TaskTimeout,
			}
			if err := dag.Add(n); err != nil {
				s.logf("[SCHED] %s 的任务图构建失败: %v", m.ID, err)
				break
			}
		}

		if dag.Size() == 0 {
			continue
		}
		if err := dag.Validate(); err != nil {
			// 图不合法（依赖缺失或有环）时，退化为"每个任务各自独立执行"，
			// 而不是让整个渠道的签到彻底不可用。
			s.logf("[SCHED] %s 的任务图不合法，已退化为无依赖执行: %v", m.ID, err)
			dag = NewDAG()
			for _, t := range m.Tasks {
				_ = dag.Add(&Node{
					ID: t.TaskID, Title: t.Title, Timeout: s.opts.TaskTimeout,
				})
			}
		}
		dags[m.ID] = dag
	}

	s.mu.Lock()
	s.jobs = jobs
	s.dags = dags
	s.mu.Unlock()
}

// hhmmFromCron 从 cron 表达式提取当日的 "HH:MM"。
//
// 只处理"分 时 都是具体数字"的简单表达式；含 * / 区间 / 列表时返回空，
// 表示不做时间限制——宁可多跑一次（节点自己会判断今日是否已完成），
// 也不要因为解析不出来而永不执行。
func hhmmFromCron(expr string) string {
	fields := strings.Fields(expr)
	if len(fields) < 2 {
		return ""
	}
	minStr, hourStr := fields[0], fields[1]
	if strings.ContainsAny(minStr+hourStr, "*/,-") {
		return ""
	}
	var h, m int
	if _, err := fmt.Sscanf(hourStr, "%d", &h); err != nil {
		return ""
	}
	if _, err := fmt.Sscanf(minStr, "%d", &m); err != nil {
		return ""
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", h, m)
}

// dagFor 取某渠道的任务图。
func (s *Scheduler) dagFor(providerID string) *DAG {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dags[providerID]
}

// loop 是调度主循环。
func (s *Scheduler) loop() {
	defer s.wg.Done()
	t := time.NewTicker(s.opts.TickInterval)
	defer t.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-t.C:
			// 唤醒检测：ticker 在系统睡眠期间不会推进，
			// 恢复后第一次 tick 的实际间隔会远大于配置值。
			if gap := now.Sub(s.lastTick); gap > s.opts.WakeThreshold {
				s.mu.Lock()
				s.wakeCount++
				s.mu.Unlock()
				s.logf("[WAKE] 检测到系统从睡眠恢复（间隔 %v），触发补签检查", gap.Round(time.Second))
				s.RunAll("wake")
			}
			s.lastTick = now
			s.tick(now)
		}
	}
}

// tick 检查所有任务是否到点。
func (s *Scheduler) tick(now time.Time) {
	s.mu.Lock()
	type due struct {
		j *Job
	}
	var dues []due
	for _, j := range s.jobs {
		if j.nextRun.IsZero() || now.Before(j.nextRun) {
			continue
		}
		dues = append(dues, due{j: j})
		// 先推进下次时间，避免 dispatch 阻塞期间重复触发。
		if base := j.schedule.Next(now); !base.IsZero() {
			j.nextRun = Jitter(base, s.opts.CronJitter, j.ProviderID+"|"+j.TaskID)
		} else {
			j.nextRun = time.Time{}
		}
	}
	s.mu.Unlock()

	for _, d := range dues {
		s.dispatch(d.j, "cron")
	}
}

// dispatch 把一个 cron 触发事件展开成"对每个可用账号跑一遍任务图"。
//
// 去重是必需的：同一渠道有 3 个任务，它们的 cron 可能在同一分钟内到点，
// 各自调一次 dispatch 就会让同一个账号入队 3 次、把整张图跑 3 遍。
func (s *Scheduler) dispatch(j *Job, reason string) (int, int) {
	if reason != "manual" && !s.checkinAllowed(j.ProviderID) {
		return 0, 0
	}
	p, ok := provider.Get(j.ProviderID)
	if !ok {
		return 0, 0
	}
	if _, ok := provider.AsCheckIn(p); !ok {
		return 0, 0
	}

	envs := s.pool.ListByProvider(j.ProviderID)
	if len(envs) == 0 {
		return 0, 0
	}

	today := DayKey(time.Now())
	queued, skipped := 0, 0
	for _, env := range envs {
		// 失效/冷却中的账号不参与签到，否则只会刷失败并加重风控。
		if !env.Available() {
			continue
		}
		if s.st.IsDone(env.AccountID, j.TaskID, today) {
			skipped++
			continue
		}
		key := runningKey(j.ProviderID, env.AccountID)
		if !s.markRunning(key) {
			continue // 该账号正在跑任务图，跳过
		}
		if s.queueFor(j.ProviderID).push(queuedJob{
			accountID:  env.AccountID,
			providerID: j.ProviderID,
			reason:     reason,
		}) {
			queued++
		} else {
			s.clearRunning(key)
			skipped++
		}
	}

	if queued > 0 || skipped > 0 {
		s.logf("[SCHED] %s 入队 %d 个账号、跳过 %d 个已完成（触发: %s/%s）", j.ProviderID, queued, skipped, j.TaskID, reason)
	}
	return queued, skipped
}

func runningKey(providerID, accountID string) string {
	return providerID + "|" + accountID
}

// markRunning 尝试标记"该账号正在执行"，返回 false 表示已在执行。
func (s *Scheduler) markRunning(key string) bool {
	_, loaded := s.running.LoadOrStore(key, struct{}{})
	return !loaded
}

func (s *Scheduler) clearRunning(key string) {
	s.running.Delete(key)
}

// queueFor 取（或创建）渠道专属队列。
func (s *Scheduler) queueFor(providerID string) *providerQueue {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	if q, ok := s.queues[providerID]; ok {
		return q
	}
	q := newProviderQueue(s.ctx, providerID, s.logf, s.execOne)
	s.queues[providerID] = q
	return q
}

// execOne 让一个账号跑一遍该渠道的任务图。由队列串行调用。
func (s *Scheduler) execOne(ctx context.Context, providerID string, job queuedJob) {
	key := runningKey(providerID, job.accountID)
	defer s.clearRunning(key)

	if ctx.Err() != nil {
		return
	}

	env, ok := s.pool.Get(job.accountID)
	if !ok {
		return
	}

	dag := s.dagFor(providerID)
	if dag == nil {
		return
	}

	// 把图结构绑定到当前账号上：依赖关系共享，执行闭包各自独立。
	bound := dag.Bind(func(nodeID string) func(context.Context) NodeResult {
		return func(nctx context.Context) NodeResult {
			return s.runTask(nctx, providerID, env, nodeID, job.reason)
		}
	})

	rep := bound.Run(ctx, RunOptions{
		// 签到必须串行：同一出口 IP 上并发打卡是最容易被识别的批量特征。
		Concurrency: 1,
		// 节点之间插入 2~6 秒随机间隔。
		Gap: jitterDuration(2*time.Second, 6*time.Second),
		Now: time.Now,
		OnResult: func(r NodeResult) {
			s.recordResult(env.AccountID, providerID, r, job.reason)
		},
	})

	if rep.Success {
		s.logf("[SCHED] %s %s 任务图完成（%d 节点 / %d 跳过 / %dms）",
			providerID, job.accountID, len(rep.Results), rep.Skipped, rep.DurationMS)
	} else {
		s.logf("[SCHED] %s %s 任务图有 %d 个节点失败", providerID, job.accountID, rep.Failed)
	}
}

// runTask 执行单个节点。
//
// "今日是否已完成"的判断从 dispatch 移到了这里：
// 图一旦开始跑，每个节点自己要负责幂等——这样手动触发、唤醒补签、
// cron 三条路径可以共用同一套逻辑，不需要各自去重。
func (s *Scheduler) runTask(ctx context.Context, providerID string, env *provider.AccountEnv, taskID, reason string) NodeResult {
	today := DayKey(time.Now())
	res := NodeResult{NodeID: taskID}

	if s.st.IsDone(env.AccountID, taskID, today) {
		res.Success = true
		res.Skipped = true
		res.SkipWhy = "今日已完成"
		return res
	}

	p, ok := provider.Get(providerID)
	if !ok {
		res.Message = "渠道未注册"
		return res
	}
	cap, ok := provider.AsCheckIn(p)
	if !ok {
		res.Message = "渠道不支持签到"
		return res
	}

	tctx, cancel := context.WithTimeout(ctx, s.opts.TaskTimeout)
	defer cancel()

	start := time.Now()
	report, err := cap.ExecuteTasks(tctx, env, []string{taskID})
	res.DurationMS = time.Since(start).Milliseconds()

	switch {
	case err != nil:
		res.Success = false
		res.Message = err.Error()
		s.logf("[SCHED] %s %s/%s 失败: %v", providerID, env.AccountID, taskID, err)

	case report == nil:
		res.Success = false
		res.Message = "上游未返回结果"

	default:
		res.Success = report.Success
		res.Points = report.EarnedPoints
		res.Message = report.Message
		res.Outcome = report.Outcome
		res.Credit = report.Credit
		res.Streak = report.Streak
		res.Day = report.Day
		// 任务级结果以 report 为准（整批成功但某子任务可能被跳过）。
		for _, r := range report.Results {
			if r.TaskID == taskID && !r.Success && !r.Skipped {
				res.Success = false
				res.Message = r.Message
			}
		}
	}

	return res
}

// recordResult 把节点结果写入状态存储与 Job 展示字段。
func (s *Scheduler) recordResult(accountID, providerID string, r NodeResult, reason string) {
	if r.Skipped {
		return // 跳过不写入，避免把"今日已完成"覆盖成别的状态
	}
	today := DayKey(time.Now())
	s.st.Record(accountID, r.NodeID, Result{
		Success:   r.Success,
		Points:    r.Points,
		Message:   r.Message,
		LatencyMS: r.DurationMS,
		Outcome:   r.Outcome,
		Credit:    r.Credit,
		Streak:    r.Streak,
		Day:       r.Day,
		Reason:    reason,
		At:        time.Now().Unix(),
	}, today)
	s.markJobResult(providerID, r.NodeID, r)
	if !r.Success && r.Outcome == provider.CheckinFailed {
		// 没有结论的失败（网络/上游 5xx）当天稍后重试，而不是等明天的 cron。
		s.retryLater(providerID, r.NodeID, 30*time.Minute)
	}
	eventbus.Emit(eventbus.TopicCheckin+".completed", map[string]any{
		"provider_id": providerID,
		"account_id":  accountID,
		"task_id":     r.NodeID,
		"success":     r.Success,
		"points":      r.Points,
		"message":     r.Message,
		"latency_ms":  r.DurationMS,
		"reason":      reason,
	})
}

// retryLater 把一个任务的下一轮触发提前到 after 之后。
// 只对"没有结论"的失败使用：已签/无活动等结论当天不需要再问。
func (s *Scheduler) retryLater(providerID, taskID string, after time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ProviderID == providerID && j.TaskID == taskID {
			j.nextRun = time.Now().Add(after)
			return
		}
	}
}

// findJob 按渠道+任务 ID 查找任务定义。
func (s *Scheduler) findJob(providerID, taskID string) *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ProviderID == providerID && j.TaskID == taskID {
			return j
		}
	}
	return nil
}

// findAndMarkJob 按渠道+任务 ID 更新展示字段。
func (s *Scheduler) markJobResult(providerID, taskID string, r NodeResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ProviderID == providerID && j.TaskID == taskID {
			j.lastRun = time.Now()
			if r.Success {
				j.lastRes = "成功 +" + itoa(r.Points)
			} else {
				j.lastRes = "失败: " + r.Message
			}
			return
		}
	}
}

// MarkSkipped 记录一次"本轮跳过"（只更新展示，不写状态）。
func (s *Scheduler) markSkipped(providerID, taskID, why string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ProviderID == providerID && j.TaskID == taskID {
			j.lastRes = "跳过: " + why
			return
		}
	}
}

// RunAll 立即触发全部任务（手动 / 唤醒补签）。
func (s *Scheduler) RunAll(reason string) int {
	queued, _ := s.RunAllDetailed(reason)
	return queued
}

// RunAllDetailed 返回入队数与“今日已完成而跳过”的账号数。
func (s *Scheduler) RunAllDetailed(reason string) (queued, skipped int) {
	s.mu.Lock()
	jobs := make([]*Job, len(s.jobs))
	copy(jobs, s.jobs)
	s.mu.Unlock()

	// 按依赖拓扑排序，保证"刷新 Token"类前置任务先进队列。
	// 队列本身是 FIFO 串行的，所以入队顺序即执行顺序。
	jobs = sortByDependency(jobs)

	for _, j := range jobs {
		q, k := s.dispatch(j, reason)
		queued += q
		skipped += k
	}
	return queued, skipped
}

// sortByDependency 对任务做稳定的拓扑排序。
//
// 存在循环依赖时不会死循环：visited 在进入时即置位，回溯会提前返回。
func sortByDependency(jobs []*Job) []*Job {
	byKey := make(map[string]*Job, len(jobs))
	for _, j := range jobs {
		byKey[j.ProviderID+"|"+j.TaskID] = j
	}

	visited := make(map[string]bool, len(jobs))
	out := make([]*Job, 0, len(jobs))

	var visit func(j *Job)
	visit = func(j *Job) {
		key := j.ProviderID + "|" + j.TaskID
		if visited[key] {
			return
		}
		visited[key] = true
		for _, dep := range j.DependsOn {
			if d, ok := byKey[j.ProviderID+"|"+dep]; ok {
				visit(d)
			}
		}
		out = append(out, j)
	}

	for _, j := range jobs {
		visit(j)
	}
	return out
}

// RunProvider 立即触发某渠道的全部任务，返回实际入队数。
func (s *Scheduler) RunProvider(providerID, reason string) int {
	queued, _ := s.RunProviderDetailed(providerID, reason)
	return queued
}

// RunProviderDetailed 返回某渠道的入队数与“今日已完成而跳过”的账号数。
func (s *Scheduler) RunProviderDetailed(providerID, reason string) (queued, skipped int) {
	s.mu.Lock()
	var jobs []*Job
	for _, j := range s.jobs {
		if j.ProviderID == providerID {
			jobs = append(jobs, j)
		}
	}
	s.mu.Unlock()

	jobs = sortByDependency(jobs)
	for _, j := range jobs {
		q, k := s.dispatch(j, reason)
		queued += q
		skipped += k
	}
	return queued, skipped
}

// RunAccount 立即让某账号跑一遍任务图（force 时先清空今日状态）。
func (s *Scheduler) RunAccount(accountID string, force bool) int {
	env, ok := s.pool.Get(accountID)
	if !ok {
		return 0
	}

	if force {
		if dag := s.dagFor(env.ProviderID); dag != nil {
			for _, id := range dag.NodeIDs() {
				s.st.Reset(accountID, id)
			}
		}
	}

	key := runningKey(env.ProviderID, accountID)
	if !s.markRunning(key) {
		return 0 // 已在执行中
	}
	if s.queueFor(env.ProviderID).push(queuedJob{
		accountID:  accountID,
		providerID: env.ProviderID,
		reason:     "manual",
	}) {
		return 1
	}
	s.clearRunning(key)
	return 0
}

func (s *Scheduler) jobsForProvider(providerID string) []*Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Job
	for _, j := range s.jobs {
		if j.ProviderID == providerID {
			out = append(out, j)
		}
	}
	return out
}

// Status 返回任务快照。
func (s *Scheduler) Status() []JobStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.qmu.Lock()
	pending := make(map[string]int, len(s.queues))
	for k, q := range s.queues {
		pending[k] = q.Pending()
	}
	s.qmu.Unlock()

	today := DayKey(time.Now())
	out := make([]JobStatus, 0, len(s.jobs))
	for _, j := range s.jobs {
		envs := s.pool.ListByProvider(j.ProviderID)
		done := 0
		for _, env := range envs {
			if s.st.IsDone(env.AccountID, j.TaskID, today) {
				done++
			}
		}
		st := JobStatus{
			ProviderID: j.ProviderID,
			TaskID:     j.TaskID,
			Title:      j.Title,
			Cron:       j.schedule.String(),
			LastResult: j.lastRes,
			Pending:    pending[j.ProviderID],
			TodayDone:  done,
			TodayTotal: len(envs),
			Optional:   j.Optional,
			DependsOn:  j.DependsOn,
		}
		if !j.nextRun.IsZero() {
			st.NextRun = j.nextRun.Unix()
		}
		if !j.lastRun.IsZero() {
			st.LastRun = j.lastRun.Unix()
		}
		out = append(out, st)
	}
	return out
}

// RuntimeInfo 返回调度器运行信息。
type RuntimeInfo struct {
	Jobs       int    `json:"jobs"`
	Completed  int    `json:"completed_today"`
	UptimeSec  int64  `json:"uptime_sec"`
	WakeCount  int    `json:"wake_count"`
	StatePath  string `json:"state_path,omitempty"`
	NextFireAt int64  `json:"next_fire_at,omitempty"`
}

// Info 返回运行信息。
func (s *Scheduler) Info() RuntimeInfo {
	s.mu.Lock()
	var next time.Time
	for _, j := range s.jobs {
		if !j.nextRun.IsZero() && (next.IsZero() || j.nextRun.Before(next)) {
			next = j.nextRun
		}
	}
	n := len(s.jobs)
	wakes := s.wakeCount
	s.mu.Unlock()

	info := RuntimeInfo{
		Jobs:      n,
		Completed: s.st.CompletedToday(),
		UptimeSec: int64(time.Since(s.startedAt).Seconds()),
		WakeCount: wakes,
		StatePath: s.opts.StatePath,
	}
	if !next.IsZero() {
		info.NextFireAt = next.Unix()
	}
	return info
}

// 小工具：避免引入 strconv 只为一个 itoa。
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
