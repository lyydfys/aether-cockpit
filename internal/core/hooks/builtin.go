package hooks

import (
	"context"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 内置钩子：把最常见的六类需求做成"改配置就能用"，不需要写代码。
//
// 这些实现刻意做得很小：每一类只做一件事，名字即语义，便于组合与排查。

// DropReasoning 丢弃推理增量（部分客户端会把 reasoning 当成正文显示）。
type DropReasoning struct{ Base }

func (DropReasoning) Name() string { return "drop_reasoning" }

func (DropReasoning) OnEvent(_ context.Context, ev *ir.UnifiedStreamEvent) bool {
	return ev != nil && ev.Type == ir.EventReasoningDelta
}

// ClampOutput 把 max_tokens 压到上限以内。
//
// 典型用途：客户端不设上限、上游按最大上下文扣费，一次就跑掉整天额度。
type ClampOutput struct {
	Base
	Max int
}

func (ClampOutput) Name() string { return "clamp_output" }

func (c ClampOutput) OnRequest(_ context.Context, req *ir.UnifiedChatRequest) error {
	if req == nil || c.Max <= 0 {
		return nil
	}
	if req.MaxOutputTokens == nil || *req.MaxOutputTokens <= 0 || *req.MaxOutputTokens > c.Max {
		v := c.Max
		req.MaxOutputTokens = &v
	}
	return nil
}

// BlockModels 拦掉指定模型（前缀匹配，`*` 结尾表示前缀）。
type BlockModels struct {
	Base
	Models []string
}

func (BlockModels) Name() string { return "block_models" }

func (b BlockModels) OnRequest(_ context.Context, req *ir.UnifiedChatRequest) error {
	if req == nil {
		return nil
	}
	for _, pat := range b.Models {
		if matchModelPattern(pat, req.Model) {
			return &BlockedError{Pattern: pat, Model: req.Model}
		}
	}
	return nil
}

// BlockedError 表示请求被钩子拒绝。
type BlockedError struct {
	Pattern string
	Model   string
}

func (e *BlockedError) Error() string {
	return "模型 " + e.Model + " 被钩子规则拦截（pattern=" + e.Pattern + "）"
}

func matchModelPattern(pattern, model string) bool {
	p := strings.TrimSpace(pattern)
	if p == "" {
		return false
	}
	if strings.HasSuffix(p, "*") {
		return strings.HasPrefix(model, strings.TrimSuffix(p, "*"))
	}
	return p == model
}

// GuardProvider 让指定渠道不参与本次请求（例如成本太高、临时限流）。
type GuardProvider struct {
	Base
	Skip []string
}

func (GuardProvider) Name() string { return "guard_provider" }

func (g GuardProvider) OnProvider(_ context.Context, _ *ir.UnifiedChatRequest, providerID string) bool {
	for _, id := range g.Skip {
		if strings.EqualFold(strings.TrimSpace(id), providerID) {
			return true
		}
	}
	return false
}

// GuardAccount 跳过明确标记的账号（例如被封禁、余额为 0）。
type GuardAccount struct {
	Base
	SkipAccounts []string
}

func (GuardAccount) Name() string { return "guard_account" }

func (g GuardAccount) OnAccount(_ context.Context, _ *ir.UnifiedChatRequest, env *provider.AccountEnv) bool {
	if env == nil {
		return false
	}
	for _, id := range g.SkipAccounts {
		if strings.EqualFold(strings.TrimSpace(id), env.AccountID) {
			return true
		}
	}
	return false
}

// UsageCallback 把每次用量交给一个回调（落库、告警、上报都可以接在这）。
type UsageCallback struct {
	Base
	Fn func(UsageSummary)
}

func (UsageCallback) Name() string { return "usage_callback" }

func (u UsageCallback) OnUsage(_ context.Context, s UsageSummary) {
	if u.Fn != nil {
		u.Fn(s)
	}
}
