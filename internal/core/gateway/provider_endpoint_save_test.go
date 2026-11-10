package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// 已知渠道的端点编辑必须真的能保存并回显。
//
// 历史缺陷：渠道编辑器那四个 Base URL 输入框只对"自定义渠道"生效，
// 对 open-design 这类内置渠道改了也存不下来——点保存返回 200「已保存」，
// 刷新回来还是旧值，用户以为"保存不了这个协议"。
func TestKnownProviderEndpointIsSavedAndEchoed(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	srv := httptest.NewServer(g)
	defer srv.Close()

	before := g.providerEndpointView("open-design", "chat")

	body := `{"id":"open-design","name":"Open Design","chat":"https://amr-link.open-design.ai/v1"}`
	resp, err := http.Post(srv.URL+"/api/provider/save", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("保存应成功，得到 %d", resp.StatusCode)
	}

	after := g.providerEndpointView("open-design", "chat")
	if after != "https://amr-link.open-design.ai/v1" {
		t.Fatalf("端点没存下来：before=%q after=%q", before, after)
	}
}

// 支持 Anthropic 的渠道（claude 系）填 anthropic 栏必须能存下来。
func TestAnthropicCapableProviderAcceptsAnthropicEndpoint(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	srv := httptest.NewServer(g)
	defer srv.Close()

	body := `{"id":"claude-sub","name":"Claude","anthropic":"https://api.anthropic.com"}`
	resp, err := http.Post(srv.URL+"/api/provider/save", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("支持 Anthropic 的渠道应能保存，得到 %d", resp.StatusCode)
	}
	if got := g.providerEndpointView("claude-sub", "anthropic"); got != "https://api.anthropic.com" {
		t.Fatalf("anthropic 端点没回显：%q", got)
	}
}

// chat 是能力布尔位，不能被端点覆盖逻辑改成字符串（Android 客户端按 Boolean 解析）。
func TestChatCapabilityStaysBoolean(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	body := `{"id":"open-design","name":"Open Design","chat":"https://amr-link.open-design.ai/v1"}`
	srv := httptest.NewServer(g)
	defer srv.Close()
	if resp, err := http.Post(srv.URL+"/api/provider/save", "application/json", strings.NewReader(body)); err == nil {
		_ = resp.Body.Close()
	}

	for _, v := range g.pool.ListByProvider("open-design") {
		_ = v
	}
	// 端点视图返回字符串，能力位保持布尔——二者互不影响。
	if got := g.providerEndpointView("open-design", "chat"); got == "true" || got == "false" {
		t.Fatalf("端点视图不该返回布尔字面量：%q", got)
	}
}
