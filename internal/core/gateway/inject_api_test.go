package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleInject(t *testing.T) {
	g := New(Config{Addr: "127.0.0.1:7860", APIKey: "sk-test"})

	req := httptest.NewRequest(http.MethodGet, "/api/inject", nil)
	req.Host = "127.0.0.1:7860"
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()

	g.handleInject(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("状态 = %d", rec.Code)
	}
	var body struct {
		Origin  string `json:"origin"`
		Targets []struct {
			ID      string `json:"id"`
			Snippet string `json:"snippet"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Origin != "http://127.0.0.1:7860" {
		t.Fatalf("origin = %q", body.Origin)
	}
	if len(body.Targets) < 3 {
		t.Fatalf("targets = %d", len(body.Targets))
	}

	var foundClaude, foundCodex bool
	for _, target := range body.Targets {
		if target.ID == "claude_code" {
			foundClaude = strings.Contains(target.Snippet, "ANTHROPIC_BASE_URL") &&
				strings.Contains(target.Snippet, "sk-test")
		}
		if target.ID == "codex_cli" {
			foundCodex = strings.Contains(target.Snippet, "wire_api") &&
				strings.Contains(target.Snippet, "/v1")
		}
	}
	if !foundClaude || !foundCodex {
		t.Fatalf("片段不完整: claude=%v codex=%v", foundClaude, foundCodex)
	}
}

func TestAdminAllowed(t *testing.T) {
	g := New(Config{Addr: "127.0.0.1:7860"})

	loopback := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	loopback.RemoteAddr = "127.0.0.1:2222"
	if !g.adminAllowed(loopback) {
		t.Fatal("本机请求应放行")
	}

	inProcess := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	inProcess.RemoteAddr = ""
	if !g.adminAllowed(inProcess) {
		t.Fatal("Wails 进程内请求应放行")
	}

	remote := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	remote.RemoteAddr = "10.0.0.9:3333"
	if g.adminAllowed(remote) {
		t.Fatal("未配置 API Key 时远程管理请求不应放行")
	}

	g.cfg.APIKey = "sk-test"
	remote.Header.Set("Authorization", "Bearer sk-test")
	if !g.adminAllowed(remote) {
		t.Fatal("带正确 API Key 的远程请求应放行")
	}
	remote.Header.Set("Authorization", "Bearer wrong")
	if g.adminAllowed(remote) {
		t.Fatal("错误 API Key 不应放行")
	}

	// Wails v2.17 为进程内合成请求填的 RemoteAddr（RFC 5737 文档地址）。
	// 回归：曾经被当成远程来源拒掉，导致桌面端整个管理界面 403（实测踩过）。
	wails := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	wails.RemoteAddr = "192.0.2.1:1234"
	if !g.adminAllowed(wails) {
		t.Fatal("Wails 合成地址 192.0.2.1:1234 应按本机放行")
	}
}
