package gateway

import (
	"path/filepath"
	"testing"

	"aether-cockpit/internal/core/tools"
)

func TestMaskToolsHidesMultipleKeys(t *testing.T) {
	in := runtimeSettings{Tools: &ToolsConfig{WebSearch: &tools.SearchConfig{
		Provider: "tavily", APIKeys: []string{"k1", "k2"},
	}}}
	out := maskTools(in)
	ws := out.Tools.WebSearch
	if len(ws.APIKeys) != 0 {
		t.Fatalf("多 Key 不应下发: %v", ws.APIKeys)
	}
	if ws.KeyCount != 2 {
		t.Fatalf("key_count = %d", ws.KeyCount)
	}
	if in.Tools.WebSearch.APIKeys[0] != "k1" {
		t.Fatalf("原配置被改写: %v", in.Tools.WebSearch.APIKeys)
	}
}

func TestApplyToolsConfigKeepsKeys(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	g.settingsMu.Lock()
	g.settings.Tools = &ToolsConfig{WebSearch: &tools.SearchConfig{
		Provider: "tavily", APIKey: "single", APIKeys: []string{"k1", "k2"},
	}}
	g.settingsMu.Unlock()

	// 编辑其它字段、不带 Key：旧的单 Key 与多 Key 都保留。
	g.settingsMu.Lock()
	g.applyToolsConfig(&ToolsConfig{WebSearch: &tools.SearchConfig{Provider: "exa", Endpoint: "https://api.exa.ai"}})
	g.settingsMu.Unlock()
	ws := g.settings.Tools.WebSearch
	if ws.APIKey != "single" || len(ws.APIKeys) != 2 {
		t.Fatalf("未带 Key 时应保留: %+v", ws)
	}

	// 带了新的多 Key：替换列表，并清掉旧的单 Key。
	// 不清的话 SearchKeys 会把旧单 Key 与新列表混用，用户无法从界面清掉它。
	g.settingsMu.Lock()
	g.applyToolsConfig(&ToolsConfig{WebSearch: &tools.SearchConfig{APIKeys: []string{"n1"}}})
	g.settingsMu.Unlock()
	ws = g.settings.Tools.WebSearch
	if len(ws.APIKeys) != 1 || ws.APIKeys[0] != "n1" || ws.APIKey != "" {
		t.Fatalf("新列表应替换并清掉旧单 Key: %+v", ws)
	}

	// 打码占位符列表：沿用旧多 Key。
	g.settingsMu.Lock()
	g.applyToolsConfig(&ToolsConfig{WebSearch: &tools.SearchConfig{APIKeys: []string{webdavPasswordMask}}})
	g.settingsMu.Unlock()
	ws = g.settings.Tools.WebSearch
	if len(ws.APIKeys) != 1 || ws.APIKeys[0] != "n1" {
		t.Fatalf("占位符应沿用旧列表: %+v", ws)
	}

	// 没有任何旧配置时，打码占位符不能被当成真实 Key 存下来。
	g2 := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	g2.settingsMu.Lock()
	g2.applyToolsConfig(&ToolsConfig{WebSearch: &tools.SearchConfig{Provider: "tavily", APIKey: webdavPasswordMask, APIKeys: []string{webdavPasswordMask}}})
	g2.settingsMu.Unlock()
	ws2 := g2.settings.Tools.WebSearch
	if ws2.APIKey != "" || len(ws2.APIKeys) != 0 {
		t.Fatalf("占位符被存成 Key: %+v", ws2)
	}
}
