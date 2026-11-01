package gateway

import (
	"testing"

	"aether-cockpit/internal/core/tools"
)

// 工具服务的密钥同样遵守"打码回显 = 保留旧值"的约定。
func TestToolsAPIKeyMaskingAndKeep(t *testing.T) {
	g := &Gateway{settings: runtimeSettings{Tools: &ToolsConfig{
		WebSearch: &tools.SearchConfig{Provider: "tavily", APIKey: "secret"},
		Image:     &tools.ImageConfig{BaseURL: "https://img", APIKey: "img-secret"},
	}}}

	masked := maskSettings(g.settings)
	if masked.Tools.WebSearch.APIKey != webdavPasswordMask {
		t.Fatalf("搜索密钥未打码: %q", masked.Tools.WebSearch.APIKey)
	}
	if masked.Tools.Image.APIKey != webdavPasswordMask {
		t.Fatalf("生图密钥未打码: %q", masked.Tools.Image.APIKey)
	}
	// 打码不应改到真实值。
	if g.settings.Tools.WebSearch.APIKey != "secret" {
		t.Fatal("maskSettings 改动了真实配置")
	}

	g.applyToolsConfig(&ToolsConfig{
		WebSearch: &tools.SearchConfig{Provider: "tavily", APIKey: webdavPasswordMask},
		Image:     &tools.ImageConfig{BaseURL: "https://img", APIKey: webdavPasswordMask},
	})
	if g.settings.Tools.WebSearch.APIKey != "secret" || g.settings.Tools.Image.APIKey != "img-secret" {
		t.Fatalf("打码回传未保留旧密钥: %+v", g.settings.Tools)
	}

	g.applyToolsConfig(&ToolsConfig{WebSearch: &tools.SearchConfig{Provider: "serper", APIKey: "new"}})
	if g.settings.Tools.WebSearch.APIKey != "new" {
		t.Fatal("真实新密钥未生效")
	}
}
