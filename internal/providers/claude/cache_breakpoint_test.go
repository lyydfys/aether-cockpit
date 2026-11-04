package claude

import (
	"encoding/json"
	"testing"

	"aether-cockpit/internal/core/ir"
)

func cacheReq() *ir.UnifiedChatRequest {
	temp := 0.7
	return &ir.UnifiedChatRequest{
		Model:        "claude-tool/claude-sonnet-4-6",
		Instructions: "你是助手",
		Temperature:  &temp,
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "第一问"},
			{Type: ir.ItemTypeMessage, Role: "assistant", Text: "第一答"},
			{Type: ir.ItemTypeMessage, Role: "user", Text: "第二问"},
		},
	}
}

// 客户端没标缓存断点时，我们要自己补：system 末尾 + 最后一条 user 消息。
// 不补就等于每轮全价重算 prompt —— 长上下文下这是实打实的成本。
func TestEnsureCacheBreakpointsAddsWhenAbsent(t *testing.T) {
	body := buildRequestBody(cacheReq(), "claude-sonnet-4-6", false)

	if !hasAnyCacheControl(body) {
		t.Fatal("应自动补上缓存断点")
	}
	sys, _ := body["system"].([]any)
	if len(sys) == 0 {
		t.Fatal("缺少 system")
	}
	if block, _ := sys[len(sys)-1].(map[string]any); block["cache_control"] == nil {
		t.Fatal("system 末尾应有 cache_control")
	}
	msgs, _ := body["messages"].([]any)
	last, _ := msgs[len(msgs)-1].(map[string]any)
	blocks, _ := last["content"].([]any)
	if block, _ := blocks[len(blocks)-1].(map[string]any); block["cache_control"] == nil {
		t.Fatal("最后一条 user 消息应有 cache_control")
	}
	// 断点数量要守住 Anthropic 的 4 个上限（我们只补 2 个）。
	if n := countCacheControls(body); n > 4 {
		t.Fatalf("断点数量超限：%d", n)
	}
}

// 客户端自己标了断点就一个都不动：越俎代庖会挤掉它想要的缓存位置。
func TestEnsureCacheBreakpointsRespectsClient(t *testing.T) {
	req := cacheReq()
	req.SystemCacheControl = json.RawMessage(`{"type":"ephemeral","ttl":"1h"}`)
	body := buildRequestBody(req, "claude-sonnet-4-6", false)

	if n := countCacheControls(body); n != 1 {
		t.Fatalf("客户端标了 1 个断点时不该再加，得到 %d", n)
	}
	sys, _ := body["system"].([]any)
	block, _ := sys[len(sys)-1].(map[string]any)
	cc, _ := block["cache_control"].(map[string]any)
	if cc["ttl"] != "1h" {
		t.Fatalf("应保留客户端的 ttl，得到 %v", cc)
	}
}

// 没有 system 时只补消息断点，不 panic。
func TestEnsureCacheBreakpointsWithoutSystem(t *testing.T) {
	req := cacheReq()
	req.Instructions = ""
	body := buildRequestBody(req, "claude-sonnet-4-6", false)
	if _, ok := body["system"]; ok {
		t.Fatal("没有指令时不该凭空造 system")
	}
	if !hasAnyCacheControl(body) {
		t.Fatal("仍应给最后一条 user 消息补断点")
	}
}

// 工具调用轮（最后一条是 tool 结果）也要能补到断点。
func TestEnsureCacheBreakpointsWithToolResult(t *testing.T) {
	req := cacheReq()
	req.Items = append(req.Items,
		ir.UnifiedItem{Type: ir.ItemTypeFunctionCall, CallID: "c1", Name: "read", Arguments: `{}`},
		ir.UnifiedItem{Type: ir.ItemTypeFunctionCallOutput, CallID: "c1", Output: "内容"},
	)
	body := buildRequestBody(req, "claude-sonnet-4-6", false)
	if !hasAnyCacheControl(body) {
		t.Fatal("工具轮也应补上断点")
	}
	if n := countCacheControls(body); n > 4 {
		t.Fatalf("断点数量超限：%d", n)
	}
}

func countCacheControls(body map[string]any) int {
	n := 0
	if sys, ok := body["system"].([]any); ok {
		for _, b := range sys {
			if block, ok := b.(map[string]any); ok && block["cache_control"] != nil {
				n++
			}
		}
	}
	if msgs, ok := body["messages"].([]any); ok {
		for _, m := range msgs {
			msg, ok := m.(map[string]any)
			if !ok {
				continue
			}
			if blocks, ok := msg["content"].([]any); ok {
				for _, b := range blocks {
					if block, ok := b.(map[string]any); ok && block["cache_control"] != nil {
						n++
					}
				}
			}
		}
	}
	return n
}
