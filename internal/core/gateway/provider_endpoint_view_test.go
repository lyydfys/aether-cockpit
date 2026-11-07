package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/channels"
	"aether-cockpit/internal/provider"
)

// 渠道编辑器读的是端点地址，能力判断读的是 chat 布尔。
// 过去两者共用 `chat` 一个 key：编辑器把 true 当 URL，回显空白、
// 保存时又把 true 原样发回去，后端判定 Base URL 非法 → 用户看到"保存失败"。
func TestProviderViewExposesEndpointsAndKeepsChatBool(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})

	rec := httptest.NewRecorder()
	g.handleProviders(rec, httptest.NewRequest(http.MethodGet, "/api/providers?include_empty=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("providers: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	// chat 必须还是布尔（Android 端按 Boolean 解析，组件也用它判能力）。
	if !strings.Contains(body, `"chat":true`) && !strings.Contains(body, `"chat":false`) {
		t.Fatalf("chat 应保持布尔形态，响应片段里找不到：%s", clip(body, 600))
	}
	// endpoints 必须是对象，且键齐全。
	for _, k := range []string{`"endpoints"`, `"chat":`, `"responses":`, `"anthropic":`, `"gemini":`} {
		if !strings.Contains(body, k) {
			t.Fatalf("响应里缺少 %s：%s", k, clip(body, 600))
		}
	}
}

// 内置渠道的 chat 端点要回显清单里的默认地址（DeepSeek = /v1），
// 其余协议没有默认值时必须留空，不能把 OpenAI 地址填进 Anthropic 框。
func TestEndpointViewPerProtocol(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	if got := g.providerEndpointView("deepseek", "chat"); !strings.Contains(got, "deepseek.com") {
		t.Fatalf("deepseek chat 端点 = %q", got)
	}
	for _, kind := range []string{"anthropic", "gemini", "responses"} {
		if got := g.providerEndpointView("deepseek", kind); got != "" {
			t.Fatalf("deepseek %s 不该有默认端点，得到 %q", kind, got)
		}
	}
}

// 自定义渠道按协议回显自己的地址：OpenAI 渠道在 Anthropic 框里必须为空。
func TestEndpointViewCustomChannelMatchesProtocol(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	g.settingsMu.Lock()
	g.settings.CustomChannels = append(g.settings.CustomChannels, CustomChannel{
		ID: "relay-x", Name: "Relay X", BaseURL: "https://relay.example/v1", Protocol: "openai",
	})
	g.settingsMu.Unlock()

	if got := g.providerEndpointView("custom-relay-x", "chat"); got != "https://relay.example/v1" {
		t.Fatalf("custom chat 端点 = %q", got)
	}
	if got := g.providerEndpointView("custom-relay-x", "anthropic"); got != "" {
		t.Fatalf("openai 协议的自定义渠道不该在 anthropic 框里有值，得到 %q", got)
	}
}

// clip 截断响应片段，便于失败时看清上下文。
func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

var _ = channels.Vendors // 保留包引用（本文件的断言依赖渠道清单的默认地址）
var _ = provider.Get
