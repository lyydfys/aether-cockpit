package scheduler

import (
	"context"
	"math/rand"
	"sync"
	"time"
)

// queuedJob 是一次待执行的签到，粒度是"一个账号跑一遍该渠道的任务图"。
//
// 不再是"一个账号一个任务"：依赖顺序由 DAG 负责，队列只保证同渠道账号之间串行。
type queuedJob struct {
	accountID  string
	providerID string
	reason     string
}

// providerQueue 是渠道专属的串行抖动队列。
//
// 为什么必须串行：同一渠道的多个账号通常共享出口 IP。
// 如果并发打卡，上游看到的是"一个 IP 在几百毫秒内给 N 个账号签到"，
// 这是最典型的批量操作特征。串行 + 每个账号之间插入 2~6 秒随机延迟，
// 才能让流量看起来像"不同的人在不同时间打开客户端"。
type providerQueue struct {
	providerID string
	in         chan queuedJob
	logf       func(string, ...any)
	exec       func(ctx context.Context, providerID string, job queuedJob)

	// minJitter / maxJitter 控制账号之间的随机延迟。
	// 单独抽出来是为了让测试能把抖动压到毫秒级，否则一个用例要跑十几秒。
	minJitter time.Duration
	maxJitter time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	pending int
}

func newProviderQueue(parent context.Context, providerID string, logf func(string, ...any), exec func(context.Context, string, queuedJob)) *providerQueue {
	ctx, cancel := context.WithCancel(parent)
	q := &providerQueue{
		providerID: providerID,
		in:         make(chan queuedJob, 64),
		logf:       logf,
		exec:       exec,
		minJitter:  2 * time.Second,
		maxJitter:  6 * time.Second,
		ctx:        ctx,
		cancel:     cancel,
	}
	q.wg.Add(1)
	go q.loop()
	return q
}

func (q *providerQueue) loop() {
	defer q.wg.Done()
	for {
		select {
		case <-q.ctx.Done():
			return
		case job := <-q.in:
			q.exec(q.ctx, q.providerID, job)

			q.mu.Lock()
			q.pending--
			q.mu.Unlock()

			// 账号之间的随机延迟。用正态分布裁剪到 [2s, 6s]，
			// 比均匀分布更接近真人操作的间隔分布。
			if !q.sleepJitter() {
				return
			}
		}
	}
}

// sleepJitter 睡眠一段随机时间，返回 false 表示被取消。
func (q *providerQueue) sleepJitter() bool {
	d := jitterDuration(q.minJitter, q.maxJitter)
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-q.ctx.Done():
		return false
	}
}

func (q *providerQueue) push(job queuedJob) bool {
	q.mu.Lock()
	q.pending++
	q.mu.Unlock()

	select {
	case q.in <- job:
		return true
	case <-q.ctx.Done():
		q.mu.Lock()
		q.pending--
		q.mu.Unlock()
		return false
	default:
		// 队列满：说明积压严重，丢弃而不是阻塞调度主循环。
		q.mu.Lock()
		q.pending--
		q.mu.Unlock()
		q.logf("[WARN] %s 队列已满，丢弃任务 %s", q.providerID, job.accountID)
		return false
	}
}

func (q *providerQueue) Pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.pending
}

func (q *providerQueue) Stop() {
	q.cancel()
	q.wg.Wait()
}

// jitterDuration 生成一个裁剪到 [lo, hi] 的正态分布时长。
func jitterDuration(lo, hi time.Duration) time.Duration {
	if hi <= lo {
		return lo
	}
	mid := lo + (hi-lo)/2
	sigma := float64(hi-lo) / 4.0 // 3σ 覆盖整个区间

	v := mid + time.Duration(rand.NormFloat64()*sigma)
	if v < lo {
		v = lo
	}
	if v > hi {
		v = hi
	}
	return v
}
