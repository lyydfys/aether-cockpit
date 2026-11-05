package openaicompat

import (
	"context"
	"strings"

	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/relaybilling"
)

// QueryQuota 读取中继面板的剩余余额。
//
// 这个渠道大多数是 通用中转面板 一类聚合中转，它们保留 OpenAI 的
// 账单接口；读不到时如实报错，不影响对话路由。
func (p *Provider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	switch p.cfg.QuotaKind {
	case "cline":
		return queryClineQuota(ctx, env, p.cfg.BaseURL, p.cfg.ID)
	case "commandcode":
		return queryCommandCodeQuota(ctx, env, p.cfg.BaseURL, p.cfg.ID)
	case "deepseek":
		return queryDeepSeekQuota(ctx, env, p.cfg.BaseURL, p.cfg.ID)
	case "moonshot":
		return queryMoonshotQuota(ctx, env, p.cfg.BaseURL, p.cfg.ID)
	case "openrouter":
		return queryOpenRouterQuota(ctx, env, p.cfg.BaseURL, p.cfg.ID)
	case "siliconflow":
		return querySiliconFlowQuota(ctx, env, p.cfg.BaseURL, p.cfg.ID)
	}
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		if sp := spec.Load(p.cfg.ID); sp != nil {
			base = strings.TrimRight(strings.TrimSpace(sp.BaseURL), "/")
		}
	}
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(p.cfg.BaseURL), "/")
	}
	return relaybilling.Query(ctx, env, base)
}

// 编译期确认实现的能力。
var _ provider.QuotaCapability = (*Provider)(nil)
