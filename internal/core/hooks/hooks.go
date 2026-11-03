package hooks

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 进程内插件钩子。
//
// 定位说明（写在这里，避免后来者误以为"没做"）：aether 用 Bun 子进程跑 JS
// 插件，换来的是"任意代码扩展"，代价是必须带一个运行时、一个进程模型和一套
// 权限边界。本项目的定位是单二进制、零外挂进程，所以这里给的是**进程内、
// Go 接口级**的扩展点：
//
//   - 第三方/内部代码可以注册 Hook 实现（编译进来，或由 spec 声明内置实现）；
//   - 内置实现覆盖最常见的六类需求，用户改配置就能用，不需要写代码；
//   - 所有钩子都是 fail-open：钩子 panic/超时只跳过该钩子，绝不拖垮请求。
//
// 六类钩子与 aether 的对应关系：
//
//	OnRequest  → onRequest   请求进入管线后、发往上游前
//	OnProvider → onProvider  每次"准备尝试某个渠道"时（可跳过渠道）
//	OnAccount  → onAccount   选定账号后（可换账号/跳过）
//	OnEvent    → onEvent     每个流事件（可丢弃/改写）
//	OnResponse → onResponse  整包响应（非流式）或流结束后
//	OnUsage    → onUsage     一次请求的用量落账后

// Hook 是一个插件钩子。所有方法的返回值都遵循"零值 = 不改变"的约定，
// 这样实现者只需要关心自己要改的那一段。
type Hook interface {
	// Name 是钩子的稳定标识（日志与去重都用它）。
	Name() string
	// OnRequest 可以改写请求；返回 error 表示拒绝该请求。
	OnRequest(ctx context.Context, req *ir.UnifiedChatRequest) error
	// OnProvider 在尝试某渠道前调用；返回 skip=true 表示跳过这个渠道。
	OnProvider(ctx context.Context, req *ir.UnifiedChatRequest, providerID string) (skip bool)
	// OnAccount 在选定账号后调用；返回 skip=true 表示换一个账号。
	OnAccount(ctx context.Context, req *ir.UnifiedChatRequest, env *provider.AccountEnv) (skip bool)
	// OnEvent 可以丢弃（drop=true）或改写一个流事件。
	OnEvent(ctx context.Context, ev *ir.UnifiedStreamEvent) (drop bool)
	// OnResponse 在整个响应完成后调用（流式时为流结束）。
	OnResponse(ctx context.Context, req *ir.UnifiedChatRequest, summary UsageSummary)
	// OnUsage 在用量落账后调用。
	OnUsage(ctx context.Context, summary UsageSummary)
}

// UsageSummary 是钩子能看到的用量摘要（不含请求正文，避免插件滥用）。
type UsageSummary struct {
	ProviderID string
	AccountID  string
	Model      string
	In         int64
	Out        int64
	CacheRead  int64
	CacheWrite int64
	Cost       float64
	Status     int
	DurationMS int64
}

// Base 是所有方法都空实现的基类：实现者只覆盖关心的那几个。
type Base struct{}

func (Base) Name() string { return "base" }
func (Base) OnRequest(context.Context, *ir.UnifiedChatRequest) error {
	return nil
}
func (Base) OnProvider(context.Context, *ir.UnifiedChatRequest, string) bool {
	return false
}
func (Base) OnAccount(context.Context, *ir.UnifiedChatRequest, *provider.AccountEnv) bool {
	return false
}
func (Base) OnEvent(context.Context, *ir.UnifiedStreamEvent) bool             { return false }
func (Base) OnResponse(context.Context, *ir.UnifiedChatRequest, UsageSummary) {}
func (Base) OnUsage(context.Context, UsageSummary)                            {}

// hookTimeout 是单个钩子的执行上限。钩子跑在请求路径上，任何实现都不该
// 让一个请求卡住；超时就当它没说什么。
const hookTimeout = 200 * time.Millisecond

// Registry 是钩子注册表（按注册顺序执行）。
type Registry struct {
	mu    sync.RWMutex
	hooks []Hook
	names map[string]bool
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{names: map[string]bool{}}
}

// Register 注册一个钩子；同名（按 Name()）重复注册会被忽略。
func (r *Registry) Register(h Hook) bool {
	if h == nil {
		return false
	}
	name := h.Name()
	if name == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.names[name] {
		return false
	}
	r.names[name] = true
	r.hooks = append(r.hooks, h)
	sort.SliceStable(r.hooks, func(i, j int) bool { return false }) // 保持注册顺序
	return true
}

// Unregister 移除一个钩子。
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.names[name] {
		return false
	}
	delete(r.names, name)
	out := r.hooks[:0]
	for _, h := range r.hooks {
		if h.Name() != name {
			out = append(out, h)
		}
	}
	r.hooks = out
	return true
}

// Names 返回已注册钩子的名字（按执行顺序）。
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.hooks))
	for _, h := range r.hooks {
		out = append(out, h.Name())
	}
	return out
}

func (r *Registry) snapshot() []Hook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.hooks) == 0 {
		return nil
	}
	return append([]Hook(nil), r.hooks...)
}

// guard 用 recover + 超时包住一次钩子调用。
//
// 为什么两层都要：panic 会打断整个请求 goroutine（网关不能因为一个坏钩子
// 挂掉）；超时则防"钩子自己阻塞"（例如做了网络调用）。超时时直接返回，
// 不等它——钩子的输出是可选的。
func guard(name string, fn func()) (ran bool) {
	done := make(chan struct{})
	go func() {
		defer func() {
			if p := recover(); p != nil {
				log.Printf("[WARN] 钩子 %q panic: %v", name, p)
				close(done)
			}
		}()
		fn()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(hookTimeout):
		log.Printf("[WARN] 钩子 %q 超时（%v），已跳过", name, hookTimeout)
		return false
	}
}

// RunRequest 依次执行 OnRequest；任一钩子返回 error 则拒绝该请求。
func (r *Registry) RunRequest(ctx context.Context, req *ir.UnifiedChatRequest) error {
	for _, h := range r.snapshot() {
		var err error
		guard(h.Name()+".OnRequest", func() { err = h.OnRequest(ctx, req) })
		if err != nil {
			return err
		}
	}
	return nil
}

// RunProvider 报告是否有任一钩子要求跳过该渠道。
func (r *Registry) RunProvider(ctx context.Context, req *ir.UnifiedChatRequest, providerID string) bool {
	for _, h := range r.snapshot() {
		skip := false
		guard(h.Name()+".OnProvider", func() { skip = h.OnProvider(ctx, req, providerID) })
		if skip {
			return true
		}
	}
	return false
}

// RunAccount 报告是否有任一钩子要求换账号。
func (r *Registry) RunAccount(ctx context.Context, req *ir.UnifiedChatRequest, env *provider.AccountEnv) bool {
	for _, h := range r.snapshot() {
		skip := false
		guard(h.Name()+".OnAccount", func() { skip = h.OnAccount(ctx, req, env) })
		if skip {
			return true
		}
	}
	return false
}

// RunEvent 依次执行 OnEvent；任一处要求 drop 就丢弃该事件。
func (r *Registry) RunEvent(ctx context.Context, ev *ir.UnifiedStreamEvent) bool {
	for _, h := range r.snapshot() {
		drop := false
		guard(h.Name()+".OnEvent", func() { drop = h.OnEvent(ctx, ev) })
		if drop {
			return true
		}
	}
	return false
}

// RunResponse / RunUsage 是"事后通知"型钩子，没有返回值。
func (r *Registry) RunResponse(ctx context.Context, req *ir.UnifiedChatRequest, s UsageSummary) {
	for _, h := range r.snapshot() {
		hh := h
		guard(hh.Name()+".OnResponse", func() { hh.OnResponse(ctx, req, s) })
	}
}

func (r *Registry) RunUsage(ctx context.Context, s UsageSummary) {
	for _, h := range r.snapshot() {
		hh := h
		guard(hh.Name()+".OnUsage", func() { hh.OnUsage(ctx, s) })
	}
}
