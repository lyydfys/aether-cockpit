package state

import (
	"path/filepath"
	"testing"
)

func TestResponseOutputPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s := New(Options{MaxRecords: 10, SQLitePath: path})
	out := []any{map[string]any{
		"type": "message", "role": "assistant",
		"content": []any{map[string]any{"type": "output_text", "text": "hello"}},
	}}
	s.PutResponse(&ResponseRecord{ResponseID: "resp_1", ProviderID: "codex", Model: "gpt", Output: out})
	if err := s.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}

	got, ok := s.GetResponse("resp_1")
	if !ok || len(got.Output) != 1 {
		t.Fatalf("内存快照 output = %#v ok=%v", got, ok)
	}

	s2 := New(Options{MaxRecords: 10, SQLitePath: path})
	defer s2.Close()
	got, ok = s2.GetResponse("resp_1")
	if !ok {
		t.Fatal("重启后快照丢失")
	}
	if len(got.Output) != 1 {
		t.Fatalf("重启后 output = %#v", got.Output)
	}
	msg, _ := got.Output[0].(map[string]any)
	if msg["role"] != "assistant" {
		t.Fatalf("output 结构变了: %#v", got.Output[0])
	}
}
