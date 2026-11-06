package githubcopilot

import (
	"testing"

	"aether-cockpit/internal/core/ir"
)

// 用户提问 → user；工具续轮/助手消息 → agent。
// 写死任何一边都会和请求体自相矛盾。
func TestCopilotInitiator(t *testing.T) {
	user := &ir.UnifiedChatRequest{Items: []ir.UnifiedItem{
		{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"},
	}}
	if got := copilotInitiator(user); got != "user" {
		t.Fatalf("用户消息应判 user，得到 %q", got)
	}
	toolOut := &ir.UnifiedChatRequest{Items: []ir.UnifiedItem{
		{Type: ir.ItemTypeMessage, Role: "user", Text: "run it"},
		{Type: ir.ItemTypeFunctionCall, Name: "read_file"},
		{Type: ir.ItemTypeFunctionCallOutput, Text: "ok"},
	}}
	if got := copilotInitiator(toolOut); got != "agent" {
		t.Fatalf("工具续轮应判 agent，得到 %q", got)
	}
	assistant := &ir.UnifiedChatRequest{Items: []ir.UnifiedItem{
		{Type: ir.ItemTypeMessage, Role: "assistant", Text: "done"},
	}}
	if got := copilotInitiator(assistant); got != "agent" {
		t.Fatalf("助手收尾应判 agent，得到 %q", got)
	}
	if got := copilotInitiator(nil); got != "user" {
		t.Fatalf("空请求应兜底 user，得到 %q", got)
	}
}

// 带图请求要补 Copilot-Vision-Request 头；纯文本不加。
func TestCopilotHasImages(t *testing.T) {
	plain := &ir.UnifiedChatRequest{Items: []ir.UnifiedItem{
		{Type: ir.ItemTypeMessage, Role: "user", Text: "describe"},
	}}
	if copilotHasImages(plain) {
		t.Fatal("纯文本不该被判为带图")
	}
	withImage := &ir.UnifiedChatRequest{Items: []ir.UnifiedItem{
		{Type: ir.ItemTypeMessage, Role: "user", Text: "看图",
			Attachments: []ir.MediaAttachment{{Kind: "image", MIME: "image/png"}}},
	}}
	if !copilotHasImages(withImage) {
		t.Fatal("带图请求应判为 true")
	}
	if copilotHasImages(nil) {
		t.Fatal("nil 不该 panic / 判 true")
	}
}
