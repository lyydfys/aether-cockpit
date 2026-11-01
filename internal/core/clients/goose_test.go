package clients

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyGoose(t *testing.T) {
	t.Setenv("GOOSE_CONFIG_DIR", "")
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "goose")
	if err := os.MkdirAll(filepath.Join(dir, "custom_providers"), 0o755); err != nil {
		t.Fatal(err)
	}
	origCfg := `# goose 配置
GOOSE_PROVIDER: openai
active_provider: openai
providers:
  openai:
    enabled: true
    model: gpt-4o
    configured: true
`
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(origCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	otherProvider := filepath.Join(dir, "custom_providers", "openai.json")
	if err := os.WriteFile(otherProvider, []byte(`{"name":"openai"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewWithHome(home)
	if _, err := m.Apply("goose", ApplyParams{
		BaseURL: "http://127.0.0.1:7860",
		APIKey:  "gw-key",
		Model:   "claude-sonnet-4-6",
		ModelOptions: []ModelOption{
			{Value: "claude-sonnet-4-6"},
			{Value: "kimi-k2"},
		},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var doc struct {
		Name    string `json:"name"`
		Engine  string `json:"engine"`
		BaseURL string `json:"base_url"`
		Headers struct {
			Authorization string `json:"Authorization"`
		} `json:"headers"`
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "custom_providers", "aether.json"))
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("provider json: %v", err)
	}
	if doc.Name != "aether" || doc.Engine != "openai" || doc.BaseURL != "http://127.0.0.1:7860/v1" ||
		doc.Headers.Authorization != "Bearer gw-key" || len(doc.Models) != 2 {
		t.Fatalf("provider = %+v", doc)
	}

	cfg, _ := os.ReadFile(cfgPath)
	text := string(cfg)
	if !strings.Contains(text, "# goose 配置") {
		t.Fatal("注释丢失")
	}
	if !strings.Contains(text, "active_provider: aether") {
		t.Fatalf("active_provider 未更新:\n%s", text)
	}
	if !strings.Contains(text, "  openai:\n") {
		t.Fatal("其它 provider 被破坏")
	}
	if !strings.Contains(text, "  aether:\n") || !strings.Contains(text, "    model: claude-sonnet-4-6") {
		t.Fatalf("aether 子块缺失:\n%s", text)
	}

	// 重复注入不堆叠。
	if _, err := m.Apply("goose", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "gw-key", Model: "claude-sonnet-4-6"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = os.ReadFile(cfgPath)
	if c := strings.Count(string(cfg), "  aether:"); c != 1 {
		t.Fatalf("重复注入后 providers.aether 子块出现 %d 次:\n%s", c, cfg)
	}
	if c := strings.Count(string(cfg), "active_provider: aether"); c != 1 {
		t.Fatalf("重复注入后 active_provider 出现 %d 次:\n%s", c, cfg)
	}

	// 多次 Apply 后 Restore 只回退一步（这是既定语义）；
	// “单次 Apply→Restore 删除新建文件”由 TestApplyGooseCreatesConfig 覆盖。

}

func TestApplyGooseCreatesConfig(t *testing.T) {
	t.Setenv("GOOSE_CONFIG_DIR", "")
	home := t.TempDir()
	m := NewWithHome(home)
	if _, err := m.Apply("goose", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "k", Model: "m"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	cfg, err := os.ReadFile(filepath.Join(home, ".config", "goose", "config.yaml"))
	if err != nil {
		t.Fatalf("应创建 config.yaml: %v", err)
	}
	text := string(cfg)
	if !strings.Contains(text, "active_provider: aether") || !strings.Contains(text, "  aether:") {
		t.Fatalf("新建 config.yaml 内容不符:\n%s", text)
	}

	// 应用时新建的两份文件，还原后都应消失（主文件 -created 分支不能提前
	// return，否则附属 config.yaml 会残留）。
	if err := m.Restore("goose", ""); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "goose", "config.yaml")); err == nil {
		t.Fatal("还原后 config.yaml 应被删除")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "goose", "custom_providers", "aether.json")); err == nil {
		t.Fatal("还原后 aether.json 应被删除")
	}
}
