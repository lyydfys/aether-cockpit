package openaicompat

import (
	"testing"

	"aether-cockpit/internal/provider"
)

// newTestEnv 造一个带指定凭证的账号。
func newTestEnv(t *testing.T, creds map[string]string) *provider.AccountEnv {
	t.Helper()
	env := provider.NewAccountEnv("acc-proto", "open-design")
	for k, v := range creds {
		env.SetCredential(k, v)
	}
	return env
}

// 一个渠道可以同时配多个协议地址；provider 必须按 api_protocol 取对应的那个，
// 而不是一律用账号的 base_url。
func TestProtocolEndpointSelectsPerProtocolURL(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"base_url":           "https://amr-link.open-design.ai/v1",
		"chat_base_url":      "https://amr-link.open-design.ai/v1",
		"anthropic_base_url": "https://amr-link.open-design.ai",
		"responses_base_url": "https://amr-link.open-design.ai/v1",
		"gemini_base_url":    "https://amr-link.open-design.ai/gemini",
	})

	cases := []struct {
		protocol string
		want     string
	}{
		{"openai", "https://amr-link.open-design.ai/v1"},
		{"chat", "https://amr-link.open-design.ai/v1"},
		{"anthropic", "https://amr-link.open-design.ai"},
		{"messages", "https://amr-link.open-design.ai"},
		{"responses", "https://amr-link.open-design.ai/v1"},
		{"gemini", "https://amr-link.open-design.ai/gemini"},
		// 未知协议：不返回任何地址，让调用方沿用默认 base_url。
		{"", ""},
		{"unknown", ""},
	}
	for _, c := range cases {
		if got := protocolEndpoint(env, c.protocol); got != c.want {
			t.Errorf("protocol=%q got %q, want %q", c.protocol, got, c.want)
		}
	}
}

// 没配分协议地址的老账号：必须返回空串，让 provider 继续用 base_url 兜底。
// 否则会把已有用户的请求打到空地址。
func TestProtocolEndpointEmptyForLegacyAccount(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"base_url": "https://legacy.example.com/v1",
	})
	for _, protocol := range []string{"openai", "anthropic", "responses", "gemini"} {
		if got := protocolEndpoint(env, protocol); got != "" {
			t.Fatalf("老账号 protocol=%q 应返回空，得到 %q", protocol, got)
		}
	}
}

// 尾部斜杠要归一，避免拼出 //v1/messages 这种地址。
func TestProtocolEndpointTrimsTrailingSlash(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"anthropic_base_url": "https://amr-link.open-design.ai/",
	})
	if got := protocolEndpoint(env, "anthropic"); got != "https://amr-link.open-design.ai" {
		t.Fatalf("got %q", got)
	}
}
