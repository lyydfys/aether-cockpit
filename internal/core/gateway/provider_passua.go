package gateway

import "context"

// 渠道级"把 Agent 自己的 User-Agent 发给上游"（渠道编辑器 高级 →
// Send the agent's own User-Agent）。
//
// 一些上游按 UA 限流/放行（中转站、厂商灰名单）；此前 `passUserAgent` 只在
// 前端 draft 里，providerDraft 没有字段，保存被静默丢弃。现在：
//   - 存进 settings.ProviderPassUA；
//   - 下游请求的 UA 在 handleChat 里放进 ctx，transport 包装层按渠道开关覆盖
//     发给上游的 User-Agent（覆盖渠道代码自己设的 UA）。
//
// 没有下游 UA（例如网关自调用）或开关关闭时，保持渠道原来的 UA 不动。

type downstreamUAKey struct{}

// withDownstreamUA 把下游 User-Agent 放进 ctx。
func withDownstreamUA(ctx context.Context, ua string) context.Context {
	if ua == "" {
		return ctx
	}
	return context.WithValue(ctx, downstreamUAKey{}, ua)
}

// downstreamUAFrom 取 ctx 里的下游 UA。
func downstreamUAFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	ua, _ := ctx.Value(downstreamUAKey{}).(string)
	return ua
}

// providerPassUA 报告渠道是否把 Agent 的 UA 透传给上游。
func (g *Gateway) providerPassUA(providerID string) bool {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	return g.settings.ProviderPassUA[providerID]
}

// setProviderPassUA 保存该开关；false 删除条目。
func (g *Gateway) setProviderPassUA(providerID string, pass bool) error {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.ProviderPassUA == nil {
		g.settings.ProviderPassUA = map[string]bool{}
	}
	if pass {
		g.settings.ProviderPassUA[providerID] = true
	} else {
		delete(g.settings.ProviderPassUA, providerID)
	}
	if len(g.settings.ProviderPassUA) == 0 {
		g.settings.ProviderPassUA = nil
	}
	return g.saveSettings()
}
