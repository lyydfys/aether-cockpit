package ir

import (
	"encoding/json"
	"strings"
	"testing"
)

// 估算器必须把工具定义算进去：agent 请求里 tools 往往占输入的一大半，
// 漏算会让估算偏低一半以上（进而让"上游截断"判定和成本核算全部失真）。
func TestEstimateTokensCountsTools(t *testing.T) {
	base := &UnifiedChatRequest{
		Instructions: "你是助手",
		Items:        []UnifiedItem{{Type: ItemTypeMessage, Role: "user", Text: "你好"}},
	}
	withTools := &UnifiedChatRequest{
		Instructions: base.Instructions,
		Items:        base.Items,
		Tools: []UnifiedToolSpec{{
			Name:        "spawn_teammate",
			Description: strings.Repeat("创建一个子代理并分配任务。", 40),
			Parameters:  json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"}}}`),
		}},
	}
	a := EstimateTokens(base)
	b := EstimateTokens(withTools)
	if b <= a {
		t.Fatalf("带工具定义的估算应更大：无工具=%d 有工具=%d", a, b)
	}
	// 工具描述有 40*13=520 个汉字，至少要涨 400 token。
	if b-a < 400 {
		t.Fatalf("工具定义没被算进去（只涨了 %d）", b-a)
	}
}

// 中文按 1 token/字，英文按 4 字符/token。
func TestEstimateTokensCJKVsASCII(t *testing.T) {
	cjk := &UnifiedChatRequest{Items: []UnifiedItem{{Type: ItemTypeMessage, Role: "user", Text: strings.Repeat("字", 100)}}}
	ascii := &UnifiedChatRequest{Items: []UnifiedItem{{Type: ItemTypeMessage, Role: "user", Text: strings.Repeat("a", 400)}}}

	// 两者正文应大致等价（100 汉字 ≈ 400 英文字符），扣除每条消息固定开销后比较。
	cjkN, asciiN := EstimateTokens(cjk), EstimateTokens(ascii)
	diff := cjkN - asciiN
	if diff < -10 || diff > 10 {
		t.Fatalf("中文 100 字(%d) 与英文 400 字符(%d) 应接近", cjkN, asciiN)
	}
}

// 附件按固定视觉 token 折算，不能当 0。
func TestEstimateTokensCountsAttachments(t *testing.T) {
	with := &UnifiedChatRequest{Items: []UnifiedItem{{
		Type: ItemTypeMessage, Role: "user", Text: "看图",
		Attachments: []MediaAttachment{{}, {}},
	}}}
	without := &UnifiedChatRequest{Items: []UnifiedItem{{
		Type: ItemTypeMessage, Role: "user", Text: "看图",
	}}}
	if EstimateTokens(with) <= EstimateTokens(without) {
		t.Fatal("附件应计入估算")
	}
}

// 空请求返回 0 以外的最小值不应该 panic。
func TestEstimateTokensNilAndEmpty(t *testing.T) {
	if got := EstimateTokens(nil); got != 0 {
		t.Fatalf("nil 应返回 0，实际 %d", got)
	}
	if got := EstimateTokens(&UnifiedChatRequest{}); got < 0 {
		t.Fatalf("空请求不应返回负数：%d", got)
	}
}
