package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	// 注册一个 Chat 渠道（claude-sub）和一个非 Chat 渠道（cursor），
	// 用它们验证 chat 字段对 true/false 都显式下发。
	_ "aether-cockpit/internal/providers/claude"
	_ "aether-cockpit/internal/providers/cursor"
)

// TestProvidersResponseIncludesChatFlag 防回归：/api/providers 是手工拼装
// 的响应，chat 若是 omitempty 或漏拼，前端就拿不到"能否反代"的布尔值。
func TestProvidersResponseIncludesChatFlag(t *testing.T) {
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
	})
	rec := httptest.NewRecorder()
	g.handleProviders(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var out struct {
		Providers []map[string]json.RawMessage `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if len(out.Providers) == 0 {
		t.Fatal("响应里没有任何渠道")
	}

	seen := map[string]bool{}
	for _, row := range out.Providers {
		rawID, ok := row["id"]
		if !ok {
			t.Fatal("渠道缺少 id 字段")
		}
		var id string
		if err := json.Unmarshal(rawID, &id); err != nil {
			t.Fatalf("id 不是字符串: %v", err)
		}
		rawChat, ok := row["chat"]
		if !ok {
			t.Fatalf("渠道 %s 缺少 chat 字段", id)
		}
		var chat bool
		if err := json.Unmarshal(rawChat, &chat); err != nil {
			t.Fatalf("渠道 %s 的 chat 不是布尔值: %s", id, string(rawChat))
		}
		seen[id] = true
		if id == "claude-sub" && !chat {
			t.Fatalf("claude-sub 的 chat 应为 true")
		}
		if id == "cursor" && chat {
			t.Fatalf("cursor 的 chat 应为 false")
		}
	}
	for _, required := range []string{"claude-sub", "cursor"} {
		if !seen[required] {
			t.Fatalf("响应里没有渠道 %s", required)
		}
	}
}
