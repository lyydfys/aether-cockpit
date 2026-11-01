package gateway

import (
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/core/tools"
)

// applyToolsConfig 的多 Key 迁移语义：填写 api_keys 后旧的单 Key 必须清掉，
// 否则 SearchKeys 会把两张旧票混用；打码占位符则沿用旧值。
func TestApplyToolsSearchKeys(t *testing.T) {
	g := New(Config{
		MaxAttempts:      1,
		FirstByteTimeout: time.Second,
		StateTTL:         time.Minute,
		MaxRecords:       10,
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
	})
	g.settings.Tools = &ToolsConfig{WebSearch: &tools.SearchConfig{Provider: "tavily", APIKey: "old-key"}}

	g.applyToolsConfig(&ToolsConfig{WebSearch: &tools.SearchConfig{Provider: "tavily", APIKeys: []string{"new-1", "new-2"}}})
	ws := g.settings.Tools.WebSearch
	if ws.APIKey != "" || len(ws.APIKeys) != 2 || ws.APIKeys[0] != "new-1" {
		t.Fatalf("迁移到多 Key 后单 Key 应清空: %+v", ws)
	}

	// 打码占位符 → 沿用旧多 Key
	g.applyToolsConfig(&ToolsConfig{WebSearch: &tools.SearchConfig{Provider: "tavily", APIKeys: []string{"******"}}})
	ws = g.settings.Tools.WebSearch
	if len(ws.APIKeys) != 2 {
		t.Fatalf("打码占位符应保留旧 Key: %+v", ws)
	}

	// 单 Key 打码 → 沿用旧单 Key
	g.settings.Tools.WebSearch = &tools.SearchConfig{Provider: "tavily", APIKey: "old-single"}
	g.applyToolsConfig(&ToolsConfig{WebSearch: &tools.SearchConfig{Provider: "tavily", APIKey: "******"}})
	if got := g.settings.Tools.WebSearch.APIKey; got != "old-single" {
		t.Fatalf("打码单 Key 应保留旧值，得到 %q", got)
	}
}
