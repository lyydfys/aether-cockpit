package pipeline

import (
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
)

func TestRepairToolPairsOrphanOutputBecomesUserMessage(t *testing.T) {
	req := &ir.UnifiedChatRequest{Items: []ir.UnifiedItem{
		{Type: ir.ItemTypeMessage, Role: "user", Text: "跑一下"},
		{Type: ir.ItemTypeFunctionCallOutput, CallID: "missing", Output: `{"ok":true}`},
		{Type: ir.ItemTypeMessage, Role: "assistant", Text: "好"},
	}}
	RepairToolPairs(req)

	if len(req.Items) != 3 {
		t.Fatalf("条目数 = %d, want 3", len(req.Items))
	}
	if req.Items[1].Type != ir.ItemTypeMessage || req.Items[1].Role != "user" {
		t.Fatalf("孤儿结果未降级为用户消息: %+v", req.Items[1])
	}
	if !strings.Contains(req.Items[1].Text, `{"ok":true}`) {
		t.Errorf("输出内容丢失: %q", req.Items[1].Text)
	}
}

func TestRepairToolPairsSynthesizesMissingResult(t *testing.T) {
	req := &ir.UnifiedChatRequest{Items: []ir.UnifiedItem{
		{Type: ir.ItemTypeMessage, Role: "user", Text: "查天气"},
		{Type: ir.ItemTypeFunctionCall, CallID: "call_1", Name: "weather", Arguments: "{}"},
		// 客户端没回传结果，就继续下一轮了
		{Type: ir.ItemTypeMessage, Role: "user", Text: "算了，讲个笑话"},
	}}
	RepairToolPairs(req)

	if len(req.Items) != 4 {
		t.Fatalf("应补一条合成结果，条目数 = %d", len(req.Items))
	}
	out := req.Items[2]
	if out.Type != ir.ItemTypeFunctionCallOutput || out.CallID != "call_1" {
		t.Fatalf("合成结果位置/内容不对: %+v", out)
	}
	if !strings.Contains(out.Output, "not executed") {
		t.Errorf("合成结果应说明未执行: %q", out.Output)
	}
}

func TestRepairToolPairsLeavesTrailingCallAlone(t *testing.T) {
	req := &ir.UnifiedChatRequest{Items: []ir.UnifiedItem{
		{Type: ir.ItemTypeMessage, Role: "user", Text: "查天气"},
		{Type: ir.ItemTypeFunctionCall, CallID: "call_1", Name: "weather"},
	}}
	RepairToolPairs(req)
	if len(req.Items) != 2 {
		t.Fatalf("尾部进行中的调用不应被补结果，条目数 = %d", len(req.Items))
	}
}

func TestRepairToolPairsKeepsValidPair(t *testing.T) {
	req := &ir.UnifiedChatRequest{Items: []ir.UnifiedItem{
		{Type: ir.ItemTypeMessage, Role: "user", Text: "查天气"},
		{Type: ir.ItemTypeFunctionCall, CallID: "call_1", Name: "weather"},
		{Type: ir.ItemTypeFunctionCallOutput, CallID: "call_1", Output: `{"t":25}`},
		{Type: ir.ItemTypeMessage, Role: "user", Text: "谢谢"},
	}}
	RepairToolPairs(req)
	if len(req.Items) != 4 {
		t.Fatalf("成对的历史不应被改动，条目数 = %d", len(req.Items))
	}
	for i, it := range req.Items {
		if it.Type != req.Items[i].Type {
			t.Fatal("顺序被打乱")
		}
	}
}

func TestRepairToolPairsDropsEmptyOrphan(t *testing.T) {
	req := &ir.UnifiedChatRequest{Items: []ir.UnifiedItem{
		{Type: ir.ItemTypeFunctionCallOutput, CallID: "missing", Output: "   "},
	}}
	RepairToolPairs(req)
	if len(req.Items) != 0 {
		t.Fatalf("空孤儿结果应被丢弃，条目数 = %d", len(req.Items))
	}
}
