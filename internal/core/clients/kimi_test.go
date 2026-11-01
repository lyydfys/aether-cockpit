package clients

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// TestApplyKimiCode 验证 Kimi Code 的 TOML 注入：
// 保留用户注释与其它表、写入 provider/模型表、替换 default_model、可还原。
func TestApplyKimiCode(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)
	dir := filepath.Join(home, ".kimi-code")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := `# 我的 Kimi 配置
default_model = "moonshot/kimi-k2"

[providers.moonshot]
type = "kimi"
base_url = "https://api.moonshot.cn/v1"
api_key = "sk-moon"

[models."moonshot/kimi-k2"]
provider = "moonshot"
model = "kimi-k2"
max_context_size = 200000
`
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Apply("kimi_code", ApplyParams{
		BaseURL: "http://127.0.0.1:7860",
		APIKey:  "gw-key",
		Model:   "claude-sonnet-4-6",
		ModelOptions: []ModelOption{
			{Value: "claude-sonnet-4-6", Label: "Sonnet"},
			{Value: "kimi-k2", Label: "Kimi K2"},
		},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	if !strings.Contains(text, "# 我的 Kimi 配置") {
		t.Fatal("注释被丢弃")
	}
	if !strings.Contains(text, "[providers.moonshot]") || !strings.Contains(text, `[models."moonshot/kimi-k2"]`) {
		t.Fatal("用户的 provider/model 表被破坏")
	}
	if !strings.Contains(text, `default_model = "aether/claude-sonnet-4-6"`) {
		t.Fatalf("default_model 未替换:\n%s", text)
	}
	if !strings.Contains(text, "[providers.aether]") || !strings.Contains(text, `base_url = "http://127.0.0.1:7860"`) {
		t.Fatal("aether provider 表缺失")
	}
	if !strings.Contains(text, `[models."aether/claude-sonnet-4-6"]`) || !strings.Contains(text, `[models."aether/kimi-k2"]`) {
		t.Fatal("aether 模型表缺失")
	}

	// 结果必须是合法 TOML，且 default_model 指向存在的模型表。
	var parsed map[string]any
	if err := toml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("不是合法 TOML: %v\n%s", err, text)
	}
	models, _ := parsed["models"].(map[string]any)
	if _, ok := models["aether/claude-sonnet-4-6"]; !ok {
		t.Fatalf("模型表缺失: %v", models)
	}

	// 重复注入不堆叠。
	if _, err := m.Apply("kimi_code", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "gw-key", Model: "claude-sonnet-4-6"}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if c := strings.Count(string(data), "[providers.aether]"); c != 1 {
		t.Fatalf("重复注入后 provider 表数 = %d", c)
	}
	if c := strings.Count(string(data), "[models.\"aether/"); c != 1 {
		t.Fatalf("只带 Model 的再次注入应只保留一个模型表，实际 %d", c)
	}

	st := findStatus(t, m.List(), "kimi_code")
	if st.Managed == nil || st.Managed.BaseURL != "http://127.0.0.1:7860" || st.Managed.Model != "aether/claude-sonnet-4-6" {
		t.Fatalf("managed = %+v", st.Managed)
	}

	// 还原语义：用一份全新文件只注入一次再还原，必须逐字回到原文（含注释）。
	home2 := t.TempDir()
	m2 := NewWithHome(home2)
	dir2 := filepath.Join(home2, ".kimi-code")
	if err := os.MkdirAll(dir2, 0o755); err != nil {
		t.Fatal(err)
	}
	path2 := filepath.Join(dir2, "config.toml")
	if err := os.WriteFile(path2, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m2.Apply("kimi_code", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "k", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := m2.Restore("kimi_code", ""); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	after, _ := os.ReadFile(path2)
	if strings.TrimSpace(string(after)) != strings.TrimSpace(original) {
		t.Fatalf("还原后不一致:\n%s", after)
	}
}
