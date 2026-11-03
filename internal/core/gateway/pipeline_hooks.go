package gateway

import (
	"context"
	"net/http"

	"aether-cockpit/internal/core/hooks"
	"aether-cockpit/internal/core/ir"
)

// 网关请求管线上的插件钩子（与 library 的 agent 生命周期 hook 不是一回事：
// 那个写在客户端配置里由 agent 自己触发，这里跑在网关的请求路径上）。

// hookRegistry 返回进程内钩子注册表（惰性创建）。
func (g *Gateway) hookRegistry() *hooks.Registry {
	g.hooksOnce.Do(func() {
		if g.hooks == nil {
			g.hooks = hooks.NewRegistry()
		}
	})
	return g.hooks
}

// syncHooks 把设置里的钩子配置装进注册表。
//
// 配置形态（settings.ui.hooks），全部可选，键名即开关：
//
//	{
//	  "drop_reasoning": true,
//	  "clamp_output": 4096,
//	  "block_models": ["gpt-4*"],
//	  "guard_providers": ["expensive"],
//	  "guard_accounts": ["acc-1"]
//	}
//
// 每次装载都先清空再按配置重建：钩子是纯配置驱动的，这样不会留下
// "关掉开关但钩子还在跑"的幽灵。
func (g *Gateway) syncHooks() {
	reg := g.hookRegistry()
	for _, name := range reg.Names() {
		reg.Unregister(name)
	}
	g.settingsMu.Lock()
	raw, _ := g.settings.UI["hooks"].(map[string]any)
	g.settingsMu.Unlock()
	if len(raw) == 0 {
		return
	}
	if on, _ := raw["drop_reasoning"].(bool); on {
		reg.Register(hooks.DropReasoning{})
	}
	if n := intOfAny(raw["clamp_output"]); n > 0 {
		reg.Register(hooks.ClampOutput{Max: n})
	}
	if list := stringListOf(raw["block_models"]); len(list) > 0 {
		reg.Register(hooks.BlockModels{Models: list})
	}
	if list := stringListOf(raw["guard_providers"]); len(list) > 0 {
		reg.Register(hooks.GuardProvider{Skip: list})
	}
	if list := stringListOf(raw["guard_accounts"]); len(list) > 0 {
		reg.Register(hooks.GuardAccount{SkipAccounts: list})
	}
}

// withEventHooks 给事件流套一层 OnEvent 钩子（可丢弃/改写事件）。
//
// 钩子是 fail-open 的：钩子出错/超时只是"这次不处理"，不会中断流。
func (g *Gateway) withEventHooks(ctx context.Context, in <-chan ir.UnifiedStreamEvent) <-chan ir.UnifiedStreamEvent {
	reg := g.hookRegistry()
	if len(reg.Names()) == 0 {
		return in
	}
	out := make(chan ir.UnifiedStreamEvent, 8)
	go func() {
		defer close(out)
		for ev := range in {
			if reg.RunEvent(ctx, &ev) {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case out <- ev:
			}
		}
	}()
	return out
}

// handlePipelineHooks GET /api/pipeline-hooks —— 当前生效的钩子清单。
//
// 让用户能确认"我配的钩子真的装上了"，而不是只能从行为上猜。
func (g *Gateway) handlePipelineHooks(w http.ResponseWriter, r *http.Request) {
	names := g.hookRegistry().Names()
	out := make([]map[string]any, 0, len(names))
	for _, n := range names {
		out = append(out, map[string]any{"name": n})
	}
	writeJSON(w, http.StatusOK, map[string]any{"hooks": out})
}

// intOfAny 把 JSON 数字（float64）或整型转成 int。
func intOfAny(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

// stringListOf 把 []any / []string 统一成 []string。
func stringListOf(v any) []string {
	switch list := v.(type) {
	case []string:
		out := make([]string, 0, len(list))
		for _, s := range list {
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
