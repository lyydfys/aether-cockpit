package inbound

import (
	"testing"

	"aether-cockpit/internal/core/ir"
)

// Codex 压缩后回传的 compaction 项必须保留密文载荷。
//
// 历史行为：它落进 default 分支，只留文本、**丢掉 encrypted_content**，
// 于是压缩过的长会话再请求上游就被拒（invalid_encrypted_content）。
func TestResponsesKeepsCompactionPayload(t *testing.T) {
	body := []byte(`{
		"model": "gpt-5.4",
		"input": [
			{"type":"message","role":"user","content":"历史"},
			{"type":"compaction","id":"cmp_1","encrypted_content":"SEALED-BLOB"},
			{"type":"message","role":"user","content":"继续"}
		]
	}`)
	req, err := ParseResponses(body, "req-1")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	var got *ir.UnifiedItem
	for i := range req.Items {
		if req.Items[i].Type == ir.ItemTypeCompaction {
			got = &req.Items[i]
		}
	}
	if got == nil {
		t.Fatalf("compaction 项被丢弃了：%+v", req.Items)
	}
	if got.EncryptedContent != "SEALED-BLOB" {
		t.Fatalf("密文载荷丢失：%q", got.EncryptedContent)
	}
	if got.ID != "cmp_1" {
		t.Fatalf("id 丢失：%q", got.ID)
	}
	// 顺序也要保住：压缩项夹在两轮用户消息之间。
	if len(req.Items) != 3 || req.Items[1].Type != ir.ItemTypeCompaction {
		t.Fatalf("顺序错乱：%+v", req.Items)
	}
}

// 没有密文的 compaction 项不该被当成消息塞进上下文（会造成空消息）。
func TestResponsesCompactionWithoutPayload(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":[
		{"type":"message","role":"user","content":"hi"},
		{"type":"compaction"}
	]}`)
	req, err := ParseResponses(body, "req-1")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	for _, it := range req.Items {
		if it.Type == ir.ItemTypeCompaction && it.EncryptedContent == "" {
			// 允许保留结构项，但必须没有文本，避免污染上下文。
			if it.Text != "" {
				t.Fatalf("空压缩项不该带文本：%+v", it)
			}
		}
	}
}
