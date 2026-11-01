package claude

import (
	"testing"

	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

func withSpec(t *testing.T, sp *spec.Spec) {
	t.Helper()
	old := specLoader
	specLoader = func(string) *spec.Spec { return sp }
	t.Cleanup(func() { specLoader = old })
}

func TestResolveUserAgentPrecedence(t *testing.T) {
	withSpec(t, nil)
	env := provider.NewAccountEnv("a1", channelID)
	if got := resolveUserAgent(env); got != defaultUserAgent {
		t.Fatalf("无 spec 无凭证应为内置地板，got %q", got)
	}

	withSpec(t, &spec.Spec{ClientVersion: "9.9.9"})
	if got := resolveUserAgent(env); got != "claude-cli/9.9.9 (external, cli)" {
		t.Fatalf("spec.client_version 未生效: %q", got)
	}

	// 单账号凭证优先级最高。
	env.Credentials["user_agent"] = "my-ua/1.0"
	if got := resolveUserAgent(env); got != "my-ua/1.0" {
		t.Fatalf("凭证覆盖未生效: %q", got)
	}
}

func TestResolveBetaHeaderFromSpec(t *testing.T) {
	withSpec(t, nil)
	env := provider.NewAccountEnv("a1", channelID)
	if got := resolveBetaHeader(env); got != defaultBetaHeader {
		t.Fatal("无 spec 时应使用默认 beta 串")
	}

	withSpec(t, &spec.Spec{HeadersTemplate: map[string]string{"Anthropic-Beta": "x-2026-01-01,y-2025-01-01"}})
	if got := resolveBetaHeader(env); got != "x-2026-01-01,y-2025-01-01" {
		t.Fatalf("spec beta 头未生效: %q", got)
	}

	env.Credentials["anthropic_beta"] = "z"
	if got := resolveBetaHeader(env); got != "z" {
		t.Fatalf("凭证应覆盖 spec: %q", got)
	}
}
