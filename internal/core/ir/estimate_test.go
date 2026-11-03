package ir

import (
	"strings"
	"testing"
)

func TestEstimateTokensRoughScale(t *testing.T) {
	req := &UnifiedChatRequest{
		Instructions: "你是助手",
		Items: []UnifiedItem{
			{Type: ItemTypeMessage, Role: "user", Text: strings.Repeat("a", 4000)}, // ≈1000
			{Type: ItemTypeFunctionCallOutput, Output: strings.Repeat("中", 500)},  // 500
		},
	}
	got := EstimateTokens(req)
	// 4 个中文指令 + 4000 ASCII/4 + 500 中文
	if want := int64(4 + 1000 + 500); got != want {
		t.Fatalf("EstimateTokens = %d, want %d", got, want)
	}
	if EstimateTokens(nil) != 0 {
		t.Fatal("nil 请求应返回 0")
	}
}
