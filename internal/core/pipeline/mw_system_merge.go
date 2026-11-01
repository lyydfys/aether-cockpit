package pipeline

import (
	"context"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func init() {
	Register("system_role_merge", func(deps Deps) Middleware { return SystemRoleMerge() })
}

// SystemRoleMerge 处理不支持 system 角色、也不接受连续同角色消息的上游。
//
// 变换：
//  1. Instructions 折进首条 user 消息顶部，并清空 Instructions；
//  2. 合并相邻同角色消息；
//  3. 若首条不是 user（例如以 assistant 开场），在前面补一条 user 承接指令。
func SystemRoleMerge() Middleware {
	return func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
			work := req.Clone()
			applySystemMerge(work)
			return next(ctx, env, work)
		}
	}
}

func applySystemMerge(req *ir.UnifiedChatRequest) {
	instr := strings.TrimSpace(req.Instructions)
	req.Instructions = ""

	// 先合并相邻同角色（tool 相关项不参与合并，否则会切断调用链）。
	req.Items = mergeAdjacentItems(req.Items)

	if instr == "" {
		return
	}

	for i := range req.Items {
		if req.Items[i].Type == ir.ItemTypeMessage && req.Items[i].Role == "user" {
			req.Items[i].Text = instr + "\n\n" + req.Items[i].Text
			return
		}
	}

	// 没有 user 消息（极少见），补一条前置指令消息。
	req.Items = append([]ir.UnifiedItem{{
		Type: ir.ItemTypeMessage,
		Role: "user",
		Text: instr,
	}}, req.Items...)
}

// mergeAdjacentItems 合并相邻的纯文本同角色消息。
func mergeAdjacentItems(items []ir.UnifiedItem) []ir.UnifiedItem {
	if len(items) < 2 {
		return items
	}
	out := make([]ir.UnifiedItem, 0, len(items))
	for _, it := range items {
		n := len(out)
		if n > 0 &&
			out[n-1].Type == ir.ItemTypeMessage &&
			it.Type == ir.ItemTypeMessage &&
			out[n-1].Role == it.Role &&
			len(out[n-1].Attachments) == 0 &&
			len(it.Attachments) == 0 {
			out[n-1].Text = strings.TrimRight(out[n-1].Text, "\n") + "\n\n" + it.Text
			continue
		}
		out = append(out, it)
	}
	return out
}
