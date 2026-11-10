package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// Open Design 这类中转聚合渠道**同时支持**多种上游协议：
// 同一个 Key 既能走 OpenAI Chat，也能走 Anthropic Messages 与 Responses。
//
// 因此三个 Base URL 必须能各填各的、各自生效，不能因为"渠道类型"就否掉某个协议。
// （曾经的错误做法：按渠道 id/清单猜它"只支持某一种协议"，把另外两栏判为非法。）
func TestMultiProtocolProviderAcceptsAllEndpoints(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	srv := httptest.NewServer(g)
	defer srv.Close()

	body := `{"id":"open-design","name":"Open Design",
		"chat":"https://amr-link.open-design.ai/v1",
		"anthropic":"https://amr-link.open-design.ai",
		"responses":"https://amr-link.open-design.ai/v1"}`
	resp, err := http.Post(srv.URL+"/api/provider/save", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf := make([]byte, 512)
		n, _ := resp.Body.Read(buf)
		t.Fatalf("三种协议同时填写应保存成功，得到 %d: %s", resp.StatusCode, string(buf[:n]))
	}

	// 三栏都必须各自回显自己的地址，不能互相覆盖。
	for _, kind := range []string{"chat", "anthropic", "responses"} {
		if got := g.providerEndpointView("open-design", kind); got != "" {
			continue
		}
		t.Fatalf("%s 端点没有回显（三协议互相覆盖了）", kind)
	}
	if got := g.providerEndpointView("open-design", "chat"); got != "https://amr-link.open-design.ai/v1" {
		t.Fatalf("chat = %q", got)
	}
	if got := g.providerEndpointView("open-design", "anthropic"); got != "https://amr-link.open-design.ai" {
		t.Fatalf("anthropic = %q（不应与 chat 混在一起）", got)
	}
	if got := g.providerEndpointView("open-design", "responses"); got != "https://amr-link.open-design.ai/v1" {
		t.Fatalf("responses = %q", got)
	}
}

// 三个协议必须各自落成独立凭证，provider 才能按 api_protocol 取到对应地址。
func TestMultiProtocolEndpointsBecomeSeparateCredentials(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	srv := httptest.NewServer(g)
	defer srv.Close()

	body := `{"id":"open-design","name":"Open Design","key":"sk-test-xyz",
		"chat":"https://amr-link.open-design.ai/v1",
		"anthropic":"https://amr-link.open-design.ai",
		"responses":"https://amr-link.open-design.ai/v1"}`
	resp, err := http.Post(srv.URL+"/api/provider/save", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_ = resp.Body.Close()

	envs := g.pool.ListByProvider("open-design")
	if len(envs) == 0 {
		t.Fatal("账号没有创建")
	}
	env := envs[0]
	for cred, want := range map[string]string{
		"chat_base_url":      "https://amr-link.open-design.ai/v1",
		"anthropic_base_url": "https://amr-link.open-design.ai",
		"responses_base_url": "https://amr-link.open-design.ai/v1",
	} {
		if got := strings.TrimRight(env.GetCredential(cred), "/"); got != want {
			t.Fatalf("%s = %q，want %q", cred, got, want)
		}
	}
}

// 只填 anthropic 也必须生效（不能被当成"填错栏"拒绝）。
func TestAnthropicOnlyEndpointWorksForMultiProtocolProvider(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	srv := httptest.NewServer(g)
	defer srv.Close()

	body := `{"id":"open-design","name":"Open Design","anthropic":"https://amr-link.open-design.ai"}`
	resp, err := http.Post(srv.URL+"/api/provider/save", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf := make([]byte, 512)
		n, _ := resp.Body.Read(buf)
		t.Fatalf("只填 anthropic 应成功，得到 %d: %s", resp.StatusCode, string(buf[:n]))
	}
	if got := g.providerEndpointView("open-design", "anthropic"); got != "https://amr-link.open-design.ai" {
		t.Fatalf("anthropic = %q", got)
	}
}

// 端点覆盖不能污染"能力位"：chat 仍是布尔，Android 按 Boolean 解析。
func TestEndpointOverrideKeepsChatCapabilityBoolean(t *testing.T) {
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
	if got := g.providerEndpointView("open-design", "chat"); got == "true" || got == "false" {
		t.Fatalf("端点视图返回了布尔字面量：%q", got)
	}
}
