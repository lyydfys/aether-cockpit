package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/library"
)

// TestLibraryEndpointMasksMCPConfigSecrets 是本轮要修的那个「已知服务端缺口」的回归测试。
//
// 现状问题：`redactMCPEntries` 只清 OAuth 的 clientSecret，MCP 的完整 `config`
// （`env` 里往往就是 API_KEY，`args` 里可能有 `--token=…`）原样出现在 `GET /api/library`
// 的响应里。任何拿到管理会话的客户端（含手机端）都能读到明文。
//
// 期望行为：
//   - 响应里**不能**出现任何真实凭据值；
//   - 但键名、命令、参数名必须保留——页面要用它们显示"这个 server 需要哪些 env/key"；
//   - **存储里必须保留真值**：下发给 Agent 时用的是真 key，掩码只作用于外发视图。
func TestLibraryEndpointMasksMCPConfigSecrets(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	g.library = library.NewStore(filepath.Join(t.TempDir(), "library.json"))

	cfg := `{"command":"uvx","args":["mcp-server-fetch","--token=tok-abc123"],` +
		`"env":{"API_KEY":"sk-live-1234567890","DEBUG":"1"},` +
		`"headers":{"Authorization":"Bearer hdr-secret-xyz"}}`
	if _, err := g.library.SaveMCP(library.MCPEntry{Name: "fetch", Config: json.RawMessage(cfg)}); err != nil {
		t.Fatalf("保存 MCP 条目失败: %v", err)
	}

	rec := httptest.NewRecorder()
	g.handleLibrary(rec, httptest.NewRequest(http.MethodGet, "/api/library", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()

	for _, secret := range []string{"sk-live-1234567890", "tok-abc123", "hdr-secret-xyz"} {
		if strings.Contains(out, secret) {
			t.Fatalf("/api/library 泄露了 MCP config 里的凭据 %q\nbody=%s", secret, out)
		}
	}
	for _, keep := range []string{"API_KEY", "DEBUG", "uvx", "mcp-server-fetch", "Authorization", "--token="} {
		if !strings.Contains(out, keep) {
			t.Fatalf("掩码不该抹掉 %q（页面要靠它显示结构）\nbody=%s", keep, out)
		}
	}

	// 存储必须仍是真值，否则真正下发给 Agent 时会拿到掩码串。
	for _, m := range g.library.List().MCPServers {
		if m.Name != "fetch" {
			continue
		}
		if !strings.Contains(string(m.Config), "sk-live-1234567890") {
			t.Fatalf("存储被误写成掩码值，下发到 Agent 会失效: %s", m.Config)
		}
		if !strings.Contains(string(m.Config), "tok-abc123") {
			t.Fatalf("存储里的 args 被误写成掩码值: %s", m.Config)
		}
	}
}

// TestMaskMCPConfigHandlesOddShapes 保证掩码函数对畸形/非对象 config 不做破坏性处理：
// 解不开就原样保留？——不行，那是泄露路径。这里要求：**解不开就整体置空**。
func TestMaskMCPConfigHandlesOddShapes(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"not-json", `command=uvx env=API_KEY`},
		{"json-array", `["a","b"]`},
		{"empty", ``},
	}
	for _, c := range cases {
		got := maskMCPConfig(json.RawMessage(c.in))
		if len(got) != 0 {
			t.Fatalf("%s: 预期置空，实际 %q", c.name, string(got))
		}
	}
}
