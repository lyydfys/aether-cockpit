package clients

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestApplyGrok(t *testing.T) {
	t.Setenv("GROK_HOME", "")
	home := t.TempDir()
	dir := filepath.Join(home, ".grok")
	os.MkdirAll(dir, 0o755)
	original := `# grok 配置
[models]
default = "user-model"
fast = "grok-4-fast"

[features]
campaigns = true
other = 1

[model."user-model"]
model = "user-model"
base_url = "https://api.x.ai/v1"
api_key = "xai-key"
`
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte(original), 0o600)

	m := NewWithHome(home)
	if _, err := m.Apply("grok", ApplyParams{
		BaseURL: "http://127.0.0.1:7860", APIKey: "gw-key", Model: "claude-sonnet-4-6",
		ModelOptions: []ModelOption{{Value: "claude-sonnet-4-6", Label: "Sonnet"}, {Value: "kimi-k2", Label: "Kimi"}},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	raw, _ := os.ReadFile(path)
	text := string(raw)
	if !strings.Contains(text, "# grok 配置") {
		t.Fatal("注释丢失")
	}
	if !strings.Contains(text, `fast = "grok-4-fast"`) || !strings.Contains(text, "other = 1") {
		t.Fatalf("[models]/[features] 用户键被删:\n%s", text)
	}
	if !strings.Contains(text, `[model."user-model"]`) {
		t.Fatal("用户模型表被删")
	}
	if !strings.Contains(text, `default = "aether/claude-sonnet-4-6"`) {
		t.Fatalf("default 未更新:\n%s", text)
	}
	if !strings.Contains(text, "campaigns = false") {
		t.Fatalf("campaigns 未关闭:\n%s", text)
	}
	if !strings.Contains(text, `[model."aether/claude-sonnet-4-6"]`) || !strings.Contains(text, `base_url = "http://127.0.0.1:7860/v1"`) {
		t.Fatalf("aether 模型表缺失:\n%s", text)
	}
	var parsed map[string]any
	if err := toml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("不是合法 TOML: %v\n%s", err, text)
	}
	models, _ := parsed["model"].(map[string]any)
	if _, ok := models["aether/kimi-k2"]; !ok {
		t.Fatalf("模型表缺失: %v", models)
	}

	// 重复注入不堆叠。
	if _, err := m.Apply("grok", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "gw-key", Model: "claude-sonnet-4-6"}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	text = string(raw)
	if c := strings.Count(text, "[model.\"aether/"); c != 1 {
		t.Fatalf("重复注入后 aether 模型表 = %d", c)
	}
	if c := strings.Count(text, "campaigns ="); c != 1 {
		t.Fatalf("campaigns 行数 = %d", c)
	}

	st := findStatus(t, m.List(), "grok")
	if st.Managed == nil || st.Managed.BaseURL != "http://127.0.0.1:7860/v1" || st.Managed.Model != "aether/claude-sonnet-4-6" {
		t.Fatalf("managed = %+v", st.Managed)
	}

	// 还原（全新目录、单次注入）。
	home2 := t.TempDir()
	dir2 := filepath.Join(home2, ".grok")
	os.MkdirAll(dir2, 0o755)
	path2 := filepath.Join(dir2, "config.toml")
	os.WriteFile(path2, []byte(original), 0o600)
	m2 := NewWithHome(home2)
	if _, err := m2.Apply("grok", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "k", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := m2.Restore("grok", ""); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, _ := os.ReadFile(path2)
	if string(got) != original {
		t.Fatalf("还原后不一致:\n%s", got)
	}
}
