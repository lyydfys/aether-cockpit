package gateway

import (
	"fmt"
	"net/http"
	"strings"
)

// injectTarget 是一份可直接复制到客户端的配置片段。
//
// 刻意只做"生成 + 复制"而不自动改写用户文件：
// 不同客户端的配置文件格式和版本差异很大，静默改写一旦写坏，
// 用户很难定位。复制片段的成本只多一步，但可逆、可控。
type injectTarget struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Kind    string         `json:"kind"` // env | toml | json
	Path    string         `json:"path,omitempty"`
	Comment string         `json:"comment,omitempty"`
	Snippet string         `json:"snippet"`
	JSON    map[string]any `json:"json,omitempty"`
}

// handleInject 返回接入各客户端的配置片段。
func (g *Gateway) handleInject(w http.ResponseWriter, r *http.Request) {
	origin := requestOrigin(r, g.PublicAddr())
	apiKey := strings.TrimSpace(g.GatewayKey())
	if apiKey == "" {
		// 本地免鉴权时给一个非空占位值：多数客户端会拒绝空 Authorization。
		apiKey = "aether-local"
	}

	targets := []injectTarget{
		{
			ID:      "claude_code",
			Name:    "Claude Code",
			Kind:    "json",
			Path:    "~/.claude/settings.json",
			Comment: "把 env 合并进现有 settings.json；也可以把两行 export 加到 shell 配置里。",
			Snippet: fmt.Sprintf("export ANTHROPIC_BASE_URL=%s\nexport ANTHROPIC_AUTH_TOKEN=%s", origin, apiKey),
			JSON: map[string]any{
				"env": map[string]any{
					"ANTHROPIC_BASE_URL":   origin,
					"ANTHROPIC_AUTH_TOKEN": apiKey,
				},
			},
		},
		{
			ID:      "codex_cli",
			Name:    "Codex CLI",
			Kind:    "toml",
			Path:    "~/.codex/config.toml",
			Comment: "Codex 走 Responses 协议，网关会自动走 Fast-Path 或 IR 路径。",
			Snippet: fmt.Sprintf(`# ~/.codex/config.toml
model_provider = "aether"

[model_providers.aether]
name = "Aether Cockpit"
base_url = "%s/v1"
env_key = "AETHER_API_KEY"
wire_api = "responses"

# shell
export AETHER_API_KEY=%s`, origin, apiKey),
		},
		{
			ID:      "openai_env",
			Name:    "OpenAI 兼容客户端",
			Kind:    "env",
			Comment: "任意 OpenAI 兼容客户端都适用（桌面客户端、IDE 插件、CLI 均可）。",
			Snippet: fmt.Sprintf("export OPENAI_BASE_URL=%s/v1\nexport OPENAI_API_KEY=%s", origin, apiKey),
		},
		{
			ID:      "anthropic_env",
			Name:    "Anthropic 兼容客户端",
			Kind:    "env",
			Comment: "适用于直接读环境变量的 Anthropic SDK 客户端。",
			Snippet: fmt.Sprintf("export ANTHROPIC_BASE_URL=%s\nexport ANTHROPIC_AUTH_TOKEN=%s", origin, apiKey),
		},
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"origin":  origin,
		"targets": targets,
		"note":    "网关只生成片段，不自动改写客户端配置文件；复制后请自行合并并备份原文件。",
	})
}

// requestOrigin 推导出浏览器/客户端实际访问网关的 origin。
//
// 直接用 r.Host 而不是 cfg.Addr：桌面端端口冲突时会自动错开，
// cfg.Addr 仍是首选端口，r.Host 才是真实地址。
func requestOrigin(r *http.Request, fallbackAddr string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = strings.TrimSpace(p)
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = fallbackAddr
	}
	return scheme + "://" + host
}
