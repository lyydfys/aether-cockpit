package openaicompat

import (
	"testing"

	"aether-cockpit/internal/core/ir"
)

// 网关自己猜出来的推理档位**不能**下发上游。
//
// 事故背景：客户端设了 max_tokens=4096 并期望模型输出一个完整工具调用，
// 直连上游正常；走网关后工具调用 JSON 在 305 字符处被截断，客户端报
// "Unterminated string in JSON"。原因就是网关按"这是代码任务"自动注入了
// reasoning_effort=medium，推理模型的 reasoning token 计入 max_tokens，
// 留给可见输出的额度不够了。
func TestAutoEffortIsNotSentUpstream(t *testing.T) {
	req := &ir.UnifiedChatRequest{
		Model:           "gpt-4o",
		ReasoningEffort: "medium",
		EffortAuto:      true, // 网关猜的
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"},
		},
	}
	payload := buildPayload(req, "gpt-4o", "max_tokens")
	if v, ok := payload["reasoning_effort"]; ok {
		t.Fatalf("自动推导的档位不应下发，实际下发 %v", v)
	}
}

// 客户端或用户规则明确指定的档位必须原样下发（不能被上一处修复误伤）。
func TestExplicitEffortIsSentUpstream(t *testing.T) {
	req := &ir.UnifiedChatRequest{
		Model:           "gpt-4o",
		ReasoningEffort: "high",
		EffortAuto:      false, // 客户端显式要求
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"},
		},
	}
	payload := buildPayload(req, "gpt-4o", "max_tokens")
	if got := payload["reasoning_effort"]; got != "high" {
		t.Fatalf("显式档位应下发 high，实际 %v", got)
	}
}

// 没设档位时当然也不该凭空出现。
func TestNoEffortNothingSent(t *testing.T) {
	req := &ir.UnifiedChatRequest{
		Model: "gpt-4o",
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"},
		},
	}
	payload := buildPayload(req, "gpt-4o", "max_tokens")
	if _, ok := payload["reasoning_effort"]; ok {
		t.Fatal("没有档位时不应出现 reasoning_effort")
	}
}
