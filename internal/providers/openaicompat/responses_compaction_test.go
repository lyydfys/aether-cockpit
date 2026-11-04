package openaicompat

import (
	"testing"

	"aether-cockpit/internal/core/ir"
)

// 压缩载荷要原样出现在上游 Responses 的 input 里。
//
// 不带回去 = 压缩过的会话续不上（上游报 invalid_encrypted_content）。
func TestBuildResponsesInputKeepsCompaction(t *testing.T) {
	req := &ir.UnifiedChatRequest{
		Model: "gpt-5.4",
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "历史"},
			{Type: ir.ItemTypeCompaction, ID: "cmp_1", EncryptedContent: "SEALED"},
			{Type: ir.ItemTypeMessage, Role: "user", Text: "继续"},
		},
	}
	input := buildResponsesInput(req)
	if len(input) != 3 {
		t.Fatalf("压缩项应保留：%+v", input)
	}
	item, ok := input[1].(map[string]any)
	if !ok {
		t.Fatalf("第二项类型异常：%T", input[1])
	}
	if item["type"] != "compaction" {
		t.Fatalf("类型 = %v", item["type"])
	}
	if item["encrypted_content"] != "SEALED" {
		t.Fatalf("密文 = %v", item["encrypted_content"])
	}
	if item["id"] != "cmp_1" {
		t.Fatalf("id = %v", item["id"])
	}
}

// 没有密文的压缩项不该被发上游（空载荷等于没带，还会占位）。
func TestBuildResponsesInputSkipsEmptyCompaction(t *testing.T) {
	req := &ir.UnifiedChatRequest{
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeCompaction},
			{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"},
		},
	}
	input := buildResponsesInput(req)
	if len(input) != 1 {
		t.Fatalf("空压缩项应被跳过：%+v", input)
	}
}
