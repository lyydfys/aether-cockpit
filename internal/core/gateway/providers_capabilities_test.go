package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	_ "aether-cockpit/internal/providers/claude"
)

// TestProvidersResponseIncludesCapabilities 防回归：/api/providers 是手工
// 拼装的响应，漏掉 capabilities 会让「写入本机 IDE」「官方授权绑定」等
// 能力入口在前端永远不出现（曾发生过一次）。
func TestProvidersResponseIncludesCapabilities(t *testing.T) {
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
		Providers []struct {
			ID           string         `json:"id"`
			Capabilities map[string]any `json:"capabilities"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	found := false
	for _, p := range out.Providers {
		if p.ID != "claude-sub" {
			continue
		}
		found = true
		if p.Capabilities == nil {
			t.Fatal("claude-sub 缺少 capabilities")
		}
		if p.Capabilities["chat"] != true || p.Capabilities["local_import"] != true {
			t.Fatalf("capabilities = %+v", p.Capabilities)
		}
	}
	if !found {
		t.Fatal("响应里没有 claude-sub 渠道")
	}
}
