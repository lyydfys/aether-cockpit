// Package pipeline 是可组合补偿中间件链。
//
// 洋葱模型：每个中间件都可以在调用上游前改造请求、在流式返回中改造事件。
// 这样"上游缺什么能力"就变成一个可以挂载/卸载的中间件，
// 而不是散落在 N 个渠道里的 N 份 if-else。
package pipeline

import (
	"context"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/mw"
	"aether-cockpit/internal/core/redact"
	"aether-cockpit/internal/core/state"
	"aether-cockpit/internal/provider"
)

// StreamHandler 是中间件与渠道插件共同的调用形态。
type StreamHandler func(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error)

// Middleware 是标准的洋葱中间件。
type Middleware func(next StreamHandler) StreamHandler

// Deps 是中间件可用的共享依赖。
type Deps struct {
	State *state.Store
	// Redact 是可选的脱敏引擎。为 nil 或未启用时中间件直接透传。
	Redact *redact.Engine
	// Rewrite 是可选的请求改写引擎（参数覆盖 / 模型映射 / 关键词拦截 / 系统提示）。
	Rewrite *mw.Engine
	// Logger 可选，签名保持简单。
	Logf func(format string, args ...any)
}

// Factory 按名字构造中间件，名字即 Manifest.RequiredMiddlewares 里的标识。
type Factory func(deps Deps) Middleware

var registry = map[string]Factory{}

// Register 注册一个中间件工厂。由各中间件文件的 init() 调用。
func Register(name string, f Factory) {
	if f == nil {
		panic("pipeline: Register(nil) for " + name)
	}
	registry[name] = f
}

// Available 返回已注册的中间件名（供校验 Manifest 是否拼写错误）。
func Available() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}

// Build 按名字列表组装管道。
//
// 组装顺序：列表里越靠前的中间件越外层（越早看到请求、越晚看到响应）。
// 未知名字会被安全忽略并在返回的 missing 中列出，便于启动时告警。
func Build(base StreamHandler, deps Deps, names []string) (StreamHandler, []string) {
	h := base
	var missing []string
	// 倒序包裹，使 names[0] 成为最外层。
	for i := len(names) - 1; i >= 0; i-- {
		f, ok := registry[names[i]]
		if !ok {
			missing = append(missing, names[i])
			continue
		}
		h = f(deps)(h)
	}
	return h, missing
}

// BuildBase 把渠道的 ChatStream 包装成 StreamHandler。
func BuildBase(cap provider.ChatCapability) StreamHandler {
	return func(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
		// 所有渠道的统一入口：先把工具调用/结果配齐，再交给渠道。
		// 放在最内层，保证看到的是中间件处理后的最终请求。
		RepairToolPairs(req)
		return cap.ChatStream(ctx, env, req)
	}
}

// forward 把一个 channel 的事件原样转发（用于需要旁路观察的中间件）。
func forward(ctx context.Context, in <-chan ir.UnifiedStreamEvent, buffer int) chan ir.UnifiedStreamEvent {
	out := make(chan ir.UnifiedStreamEvent, buffer)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-in:
				if !ok {
					return
				}
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}
