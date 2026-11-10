package openaicompat

import (
	"testing"

	"aether-cockpit/internal/provider"
)

// Open Design 是中转聚合渠道：同一个 Key 同时提供 OpenAI Chat、
// Anthropic Messages 与 OpenAI Responses 三种端点。
// 三者都必须在清单里声明，否则账号选不到对应协议。
func TestOpenDesignDeclaresAllThreeProtocols(t *testing.T) {
	p, ok := provider.Get("open-design")
	if !ok {
		t.Fatal("open-design 未注册")
	}
	oc, ok := p.(*Provider)
	if !ok {
		t.Fatal("open-design 不是 openai-compat 渠道")
	}
	cfg := oc.Config()

	if !cfg.AllowAnthropicUpstream {
		t.Error("Open Design 应支持 Anthropic Messages 协议（AllowAnthropicUpstream=false）")
	}
	if !cfg.AllowResponsesUpstream {
		t.Error("Open Design 应支持 Responses 协议（AllowResponsesUpstream=false）")
	}
	hint := cfg.AnthropicBaseURL
	if hint == "" {
		// 提示地址不是必需的，但 Open Design 有默认值，缺了会让报错文案不完整。
		t.Log("AnthropicBaseURL 为空（只影响报错文案）")
	}
}

// 账号在三种 api_protocol 下都要能取到地址（缺一个就等于那个协议不可用）。
func TestOpenDesignAllProtocolsResolveBaseURL(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"api_key":            "sk-test",
		"base_url":           "https://amr-link.open-design.ai/v1",
		"chat_base_url":      "https://amr-link.open-design.ai/v1",
		"anthropic_base_url": "https://amr-link.open-design.ai",
		"responses_base_url": "https://amr-link.open-design.ai/v1",
	})
	for _, c := range []struct{ protocol, want string }{
		{"openai", "https://amr-link.open-design.ai/v1"},
		{"anthropic", "https://amr-link.open-design.ai"},
		{"responses", "https://amr-link.open-design.ai/v1"},
	} {
		if got := protocolEndpoint(env, c.protocol); got != c.want {
			t.Errorf("protocol=%s got %q want %q", c.protocol, got, c.want)
		}
	}
}
