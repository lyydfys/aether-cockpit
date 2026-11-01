package pipeline

import (
	"strings"

	"aether-cockpit/internal/core/ir"
)

// RepairToolPairs 修复工具调用与结果的历史配对。
//
// 上游对配对的要求比客户端更严（Anthropic 的 tool_use 后面必须跟 tool_result），
// 而客户端经常留下两类脏数据：
//  1. 孤儿结果：tool_result 的 call_id 找不到对应调用（常见于历史被裁剪过）。
//     直接透传会被上游 400；这里把它降级成一条用户消息，信息不丢。
//  2. 未应答调用：历史里有 tool_use 但没有结果，而后面已经有新的用户消息——
//     说明这一轮永远不会再有结果了。补一条合成的错误结果，让上游看到完整配对。
//
// 刻意不做"按调用顺序重排结果"：各渠道自己会把 tool_result 归组（claude 已做），
// 在 IR 层重排反而会打乱思考块与调用的相对位置。
//
// 尾部正在进行的调用（后面还没有用户消息）保持原样：客户端可能马上回传结果。
func RepairToolPairs(req *ir.UnifiedChatRequest) {
	if req == nil || len(req.Items) == 0 {
		return
	}

	calls := make(map[string]bool, 8)
	for _, it := range req.Items {
		if it.Type == ir.ItemTypeFunctionCall && it.CallID != "" {
			calls[it.CallID] = true
		}
	}

	// 第一遍：孤儿结果转用户消息。
	out := make([]ir.UnifiedItem, 0, len(req.Items))
	for _, it := range req.Items {
		if it.Type == ir.ItemTypeFunctionCallOutput && !calls[it.CallID] {
			text := strings.TrimSpace(it.Output)
			if text == "" {
				continue // 空结果没有信息量，直接丢弃
			}
			out = append(out, ir.UnifiedItem{
				Type: ir.ItemTypeMessage,
				Role: "user",
				Text: "（工具执行结果）" + text,
			})
			continue
		}
		out = append(out, it)
	}

	lastUser := -1
	for i, it := range out {
		if it.IsUserTurn() {
			lastUser = i
		}
	}
	if lastUser < 0 {
		req.Items = out
		return
	}

	// 第二遍：已经不可能有结果的调用补合成错误结果。
	answered := make(map[string]bool, 8)
	for _, it := range out {
		if it.Type == ir.ItemTypeFunctionCallOutput {
			answered[it.CallID] = true
		}
	}
	fixed := make([]ir.UnifiedItem, 0, len(out)+4)
	for i, it := range out {
		fixed = append(fixed, it)
		if it.Type != ir.ItemTypeFunctionCall || it.CallID == "" || answered[it.CallID] {
			continue
		}
		if i > lastUser {
			continue // 尾部进行中的调用，不插手
		}
		fixed = append(fixed, ir.UnifiedItem{
			Type:   ir.ItemTypeFunctionCallOutput,
			CallID: it.CallID,
			Output: `{"error":"tool call was not executed (no result was recorded in history)"}`,
		})
		answered[it.CallID] = true
	}
	req.Items = fixed
}
