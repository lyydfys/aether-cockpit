package gateway

import (
	"encoding/json"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

func TestRenderMCPConfig(t *testing.T) {
	cfgs := map[string]json.RawMessage{
		"cli": json.RawMessage(`{"command":"npx","args":["-y","x"],"env":{"K":"v"}}`),
		"web": json.RawMessage(`{"type":"http","url":"https://e/mcp","headers":{"A":"b"}}`),
		"sse": json.RawMessage(`{"type":"sse","url":"https://e/sse"}`),
	}

	text, file, skipped, err := renderMCPConfig("claude_code", []string{"cli", "web"}, cfgs)
	if err != nil || file != ".mcp.json" || len(skipped) != 0 {
		t.Fatalf("claude: file=%s skipped=%v err=%v", file, skipped, err)
	}
	var claude map[string]any
	if err := json.Unmarshal([]byte(text), &claude); err != nil {
		t.Fatal(err)
	}
	servers := claude["mcpServers"].(map[string]any)
	if servers["cli"].(map[string]any)["command"] != "npx" {
		t.Fatalf("claude = %v", servers)
	}

	text, file, _, err = renderMCPConfig("gemini_cli", []string{"web"}, cfgs)
	if err != nil || file != ".gemini/settings.json" {
		t.Fatalf("gemini: file=%s err=%v", file, err)
	}
	var gem map[string]any
	_ = json.Unmarshal([]byte(text), &gem)
	web := gem["mcpServers"].(map[string]any)["web"].(map[string]any)
	if web["httpUrl"] != "https://e/mcp" {
		t.Fatalf("gemini web = %v", web)
	}

	text, file, skipped, err = renderMCPConfig("codex_cli", []string{"cli", "web", "sse"}, cfgs)
	if err != nil || file != ".codex/config.toml" {
		t.Fatalf("codex: file=%s err=%v", file, err)
	}
	if len(skipped) != 1 || skipped[0] != "sse" {
		t.Fatalf("codex skipped = %v", skipped)
	}
	var codex map[string]any
	if err := toml.Unmarshal([]byte(text), &codex); err != nil {
		t.Fatalf("codex TOML 无效: %v\n%s", err, text)
	}
	ms := codex["mcp_servers"].(map[string]any)
	if ms["cli"].(map[string]any)["command"] != "npx" || ms["web"].(map[string]any)["url"] != "https://e/mcp" {
		t.Fatalf("codex servers = %v", ms)
	}

	text, file, _, err = renderMCPConfig("opencode", []string{"web"}, cfgs)
	if err != nil || file != "opencode.json" {
		t.Fatalf("opencode: file=%s err=%v", file, err)
	}
	var oc map[string]any
	_ = json.Unmarshal([]byte(text), &oc)
	if oc["mcp"].(map[string]any)["web"].(map[string]any)["type"] != "remote" {
		t.Fatalf("opencode = %v", oc)
	}

	if _, _, _, err := renderMCPConfig("unknown", nil, cfgs); err == nil {
		t.Fatal("未知 Agent 应报错")
	}
}
