package openaicompat

import (
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

// DeepSeek 官方同时提供 Anthropic Messages 端点，账号应能选到 anthropic 协议，
// 并且表单要直接告诉用户 Base URL 该填什么（否则会拼到 /v1/chat/completions）。
func TestDeepSeekOffersAnthropicUpstream(t *testing.T) {
	p, ok := provider.Get("deepseek")
	if !ok {
		t.Fatal("deepseek 渠道未注册")
	}
	m := p.Manifest()

	var proto *provider.FormFieldSchema
	var keyAuth *provider.FormFieldSchema
	for i := range m.AuthFormSchema {
		switch m.AuthFormSchema[i].Key {
		case "api_protocol":
			proto = &m.AuthFormSchema[i]
		case "api_key_auth":
			keyAuth = &m.AuthFormSchema[i]
		}
	}
	if proto == nil {
		t.Fatal("缺少 api_protocol 选择项（说明未声明 AllowAnthropicUpstream）")
	}
	joined := strings.Join(proto.Options, ",")
	if !strings.Contains(joined, "chat") || !strings.Contains(joined, "anthropic") {
		t.Fatalf("协议选项 = %v，want 含 chat 与 anthropic", proto.Options)
	}
	if !strings.Contains(proto.Help, "https://api.deepseek.com/anthropic") {
		t.Fatalf("提示里应给出 Anthropic 端点：%q", proto.Help)
	}
	if keyAuth == nil {
		t.Fatal("选了 anthropic 协议时应同时提供鉴权头选择")
	}
}
