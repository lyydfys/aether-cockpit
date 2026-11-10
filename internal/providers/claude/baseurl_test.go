package claude

import (
	"testing"

	"aether-cockpit/internal/provider"
)

// Anthropic 专用端点必须优先于通用 base_url。
//
// 事故背景：用户在渠道编辑器里把 anthropic 端点填对（不带 /v1），
// chat 端点填的是 <host>/v1。若这里读 base_url，就会拼出
// <host>/v1/v1/messages —— 对端没有这个路由，返回 "404 page not found"，
// 而报错只是含糊的"Claude 拒绝请求"，很难定位。
func TestBaseURLPrefersAnthropicEndpoint(t *testing.T) {
	env := provider.NewAccountEnv("acc-anth", "open-design")
	env.SetCredential("base_url", "https://relay.example.com/v1")
	env.SetCredential("anthropic_base_url", "https://relay.example.com")

	if got := baseURL(env); got != "https://relay.example.com" {
		t.Fatalf("baseURL = %q，应优先用 anthropic_base_url", got)
	}
}

// 没配 anthropic 端点时，继续用 base_url（老账号行为不变）。
func TestBaseURLFallsBackToBaseURL(t *testing.T) {
	env := provider.NewAccountEnv("acc-old", "claude-sub")
	env.SetCredential("base_url", "https://relay.example.com")

	if got := baseURL(env); got != "https://relay.example.com" {
		t.Fatalf("baseURL = %q，应回退到 base_url", got)
	}
}

// 两个都没配时用官方默认地址。
func TestBaseURLFallsBackToDefault(t *testing.T) {
	env := provider.NewAccountEnv("acc-empty", "claude-sub")
	if got := baseURL(env); got != defaultAPIBaseURL {
		t.Fatalf("baseURL = %q，应回退到默认地址", got)
	}
}

// 尾部斜杠要归一，避免拼出 //v1/messages。
func TestBaseURLTrimsTrailingSlash(t *testing.T) {
	env := provider.NewAccountEnv("acc-slash", "open-design")
	env.SetCredential("anthropic_base_url", "https://relay.example.com/")
	if got := baseURL(env); got != "https://relay.example.com" {
		t.Fatalf("baseURL = %q，尾斜杠未归一", got)
	}
}
