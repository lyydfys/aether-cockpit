package provider

import (
	"math"
	"strings"
	"time"
)

// 模型级冷却：只让"这个模型"在这个账号上休息，而不是把整个账号按住。
//
// 为什么需要这个粒度：一个账号常常同时服务多个模型，而上游的限流往往是
// 按模型（或按模型池）计的。以前任何一次 429 都会 MarkCoolingDown 整个账号，
// 结果是"一个模型被限流 → 所有模型都不可用"，这是最常见的误伤。
//
// 与账号级冷却的分工（判定函数见 ModelScopedFailure）：
//   - 模型级：429、上游明说"该模型不存在/无权限/暂不可用"；
//   - 账号级：鉴权失败、额度耗尽、连续失败熔断——这些换模型也没用。
const (
	// modelRestBase 是第一次模型级失败的冷却时长，之后按指数增长。
	modelRestBase = time.Minute
	// modelRestMax 是单次模型级冷却的上限：再久就应该换渠道而不是干等。
	modelRestMax = 10 * time.Minute
	// modelRestKeep 是冷却表的容量上限，防止长期运行后无界增长。
	modelRestKeep = 64
)

// modelRest 是一个模型在某个账号上的冷却记录。
type modelRest struct {
	until  time.Time
	reason string
	// fails 是连续失败次数，用来算指数退避；成功一次清零。
	fails int
}

// ModelRest 是冷却状态的对外快照（供落盘与调试）。
type ModelRest struct {
	Model  string `json:"model"`
	Until  int64  `json:"until"`
	Reason string `json:"reason,omitempty"`
}

// ModelRestBackoff 返回第 n 次连续失败应冷却多久：1min, 2min, 4min … 上限 10min。
//
// 写成纯函数便于单测；调用方传进来的是"已经包含本次失败"的次数。
func ModelRestBackoff(fails int) time.Duration {
	if fails <= 1 {
		return modelRestBase
	}
	d := modelRestBase << min(fails-1, 8)
	if d > modelRestMax {
		return modelRestMax
	}
	return d
}

// ModelScopedFailure 判断一个错误是否只该让"这个模型"休息。
//
// 注意这里刻意**不**包含鉴权失败与额度耗尽：那两类换模型也解决不了，
// 必须走账号级冷却（MarkInvalid / MarkDepleted）。
func ModelScopedFailure(code string) bool {
	switch code {
	case "upstream_rate_limited", "rate_limited":
		return true
	}
	return false
}

// MarkModelCoolingDown 记录一次模型级冷却。
//
// model 为空时退化为账号级（调用方没拿到模型名就别假装精确）。
func (e *AccountEnv) MarkModelCoolingDown(model string, d time.Duration, reason string) {
	model = strings.TrimSpace(model)
	if model == "" {
		e.MarkCoolingDown(d, reason)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.modelRests == nil {
		e.modelRests = make(map[string]modelRest, 4)
	}
	r := e.modelRests[model]
	r.fails++
	r.until = time.Now().Add(d)
	r.reason = reason
	e.modelRests[model] = r
	e.pruneModelRestsLocked()
}

// ModelCoolingDown 查询某个模型是否正在休息。
func (e *AccountEnv) ModelCoolingDown(model string) (bool, time.Time) {
	model = strings.TrimSpace(model)
	if model == "" {
		return false, time.Time{}
	}
	e.mu.RLock()
	r, ok := e.modelRests[model]
	e.mu.RUnlock()
	if !ok {
		return false, time.Time{}
	}
	if time.Now().After(r.until) {
		return false, r.until
	}
	return true, r.until
}

// ClearModelRest 在某个模型成功一次后清掉它的冷却计数。
func (e *AccountEnv) ClearModelRest(model string) {
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.modelRests[model]; ok {
		delete(e.modelRests, model)
	}
}

// pruneModelRestsLocked 清掉已过期的条目，并在超容量时丢掉最早到期的几条
// （调用方持有写锁）。过期条目留着只会让下次失败从更高的退避档开始。
func (e *AccountEnv) pruneModelRestsLocked() {
	now := time.Now()
	for k, v := range e.modelRests {
		if now.After(v.until) {
			delete(e.modelRests, k)
		}
	}
	if len(e.modelRests) <= modelRestKeep {
		return
	}
	// 超容量：反复找最早到期的一条删掉，直到回到容量内。
	// 容量是 64，O(n²) 在这里完全可以接受，换来的是没有额外索引要维护。
	for len(e.modelRests) > modelRestKeep {
		oldestKey := ""
		var oldestAt time.Time
		for k, v := range e.modelRests {
			if oldestKey == "" || v.until.Before(oldestAt) {
				oldestKey, oldestAt = k, v.until
			}
		}
		if oldestKey == "" {
			return
		}
		delete(e.modelRests, oldestKey)
	}
}

// ModelRests 返回当前所有未过期的模型级冷却（供落盘与 UI）。
func (e *AccountEnv) ModelRests() []ModelRest {
	e.mu.RLock()
	defer e.mu.RUnlock()
	now := time.Now()
	out := make([]ModelRest, 0, len(e.modelRests))
	for m, r := range e.modelRests {
		if now.After(r.until) {
			continue
		}
		out = append(out, ModelRest{Model: m, Until: r.until.Unix(), Reason: r.reason})
	}
	return out
}

// RestoreModelRests 从落盘快照恢复冷却（启动时调用）。
//
// 只恢复未过期的条目，并且不恢复 fails 计数：重启后从 1 档重新开始，
// 避免"重启一次就永久高退避"。
func (e *AccountEnv) RestoreModelRests(list []ModelRest) {
	if len(list) == 0 {
		return
	}
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.modelRests == nil {
		e.modelRests = make(map[string]modelRest, len(list))
	}
	for _, r := range list {
		m := strings.TrimSpace(r.Model)
		if m == "" {
			continue
		}
		until := time.Unix(r.Until, 0)
		if !until.After(now) {
			continue
		}
		e.modelRests[m] = modelRest{until: until, reason: r.Reason, fails: 1}
	}
	e.pruneModelRestsLocked()
}

// failureBackoff 是账号级失败熔断的冷却时长（替换原来的固定 5 分钟）。
//
// 连续失败越多等越久，但有上限：再久就应该由用户介入（换号/重新登录），
// 而不是让网关一直闷着。
func failureBackoff(consecutive int) time.Duration {
	if consecutive <= 0 {
		return 0
	}
	d := time.Minute << min(consecutive-1, 4)
	if d > 10*time.Minute {
		return 10 * time.Minute
	}
	return d
}

// jitter 给冷却时长加上 ±10% 抖动，避免同批账号在同一秒一起恢复、
// 又一起被打满（惊群）。
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	// 用到期纳秒做伪随机源，不引 math/rand 的全局锁。
	seed := time.Now().UnixNano()
	frac := float64(seed%2000-1000) / 10000.0 // -0.1 .. +0.1
	mult := 1 + frac
	return time.Duration(float64(d) * math.Max(0.5, mult))
}
