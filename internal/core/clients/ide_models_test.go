package clients

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyVSCode(t *testing.T) {
	t.Setenv("VSCODE_USER_DIR", "")
	home := t.TempDir()
	userDir := filepath.Join(home, "Code", "User")
	os.MkdirAll(userDir, 0o755)
	t.Setenv("VSCODE_USER_DIR", userDir)
	path := filepath.Join(userDir, "chatLanguageModels.json")
	original := `// VS Code 自己写的 JSONC
[
  {"name": "other", "vendor": "customendpoint", "models": [{"id": "x", "url": "https://x"}]}
]
`
	os.WriteFile(path, []byte(original), 0o600)

	m := NewWithHome(home)
	if _, err := m.Apply("vscode", ApplyParams{
		BaseURL: "http://127.0.0.1:7860", APIKey: "gw-key", Model: "claude-sonnet-4-6",
		ModelOptions: []ModelOption{{Value: "claude-sonnet-4-6", Label: "Sonnet"}, {Value: "kimi-k2", Label: "Kimi"}},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var groups []struct {
		Name   string `json:"name"`
		Vendor string `json:"vendor"`
		Models []struct {
			ID     string            `json:"id"`
			URL    string            `json:"url"`
			Header map[string]string `json:"requestHeaders"`
		} `json:"models"`
	}
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &groups); err != nil {
		t.Fatalf("写后不是合法 JSON: %v\n%s", err, raw)
	}
	if len(groups) != 2 || groups[0].Name != "other" {
		t.Fatalf("组 = %+v", groups)
	}
	g := groups[1]
	if g.Name != "aether" || g.Vendor != "customendpoint" || len(g.Models) != 2 {
		t.Fatalf("aether 组 = %+v", g)
	}
	if g.Models[0].URL != "http://127.0.0.1:7860/v1/chat/completions" || g.Models[0].Header["Authorization"] != "Bearer gw-key" {
		t.Fatalf("模型条目 = %+v", g.Models[0])
	}
	if st := findStatus(t, m.List(), "vscode"); st.Managed == nil || st.Managed.Model != "claude-sonnet-4-6" {
		t.Fatalf("managed = %+v", st.Managed)
	}
	// 重复注入不堆叠。
	if _, err := m.Apply("vscode", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "k", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	if c := strings.Count(string(raw), `"name": "aether"`); c != 1 {
		t.Fatalf("重复注入后 aether 组 = %d", c)
	}
	// 还原（全新单次注入）逐字回原文（含注释）。
	home2 := t.TempDir()
	userDir2 := filepath.Join(home2, "Code", "User")
	os.MkdirAll(userDir2, 0o755)
	t.Setenv("VSCODE_USER_DIR", userDir2)
	path2 := filepath.Join(userDir2, "chatLanguageModels.json")
	os.WriteFile(path2, []byte(original), 0o600)
	m2 := NewWithHome(home2)
	if _, err := m2.Apply("vscode", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "k", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := m2.Restore("vscode", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path2)
	if string(got) != original {
		t.Fatalf("还原后不一致:\n%s", got)
	}
}

func TestApplyCopilotJetBrains(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	cfg := filepath.Join(home, "xdg")
	os.MkdirAll(filepath.Join(cfg, "github-copilot"), 0o755)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	path := filepath.Join(cfg, "github-copilot", "byok.json")
	original := `{"other-provider-config":{"groupName":"other","apiType":"chatCompletions"},"other-api-key":"k1"}`
	os.WriteFile(path, []byte(original), 0o600)

	m := NewWithHome(home)
	if _, err := m.Apply("copilot-jetbrains", ApplyParams{
		BaseURL: "http://127.0.0.1:7860", APIKey: "gw-key", Model: "claude-sonnet-4-6",
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var root map[string]any
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	if _, ok := root["other-api-key"]; !ok {
		t.Fatal("其它 provider 被删")
	}
	cfgEntry := root["aether-provider-config"].(map[string]any)
	if cfgEntry["groupName"] != "aether" || cfgEntry["apiType"] != "chatCompletions" {
		t.Fatalf("provider-config = %+v", cfgEntry)
	}
	if root["aether-api-key"] != "gw-key" {
		t.Fatalf("api-key = %v", root["aether-api-key"])
	}
	models := root["aether-models-config"].(map[string]any)
	mc := models["claude-sonnet-4-6"].(map[string]any)
	if mc["deploymentUrl"] != "http://127.0.0.1:7860/v1/chat/completions" || mc["isRegistered"] != true {
		t.Fatalf("model = %+v", mc)
	}
	caps := mc["modelCapabilities"].(map[string]any)
	if caps["toolCalling"] != true || caps["name"] != "claude-sonnet-4-6" {
		t.Fatalf("caps = %+v", caps)
	}
	if st := findStatus(t, m.List(), "copilot-jetbrains"); st.Managed == nil || st.Managed.Model != "claude-sonnet-4-6" {
		t.Fatalf("managed = %+v", st.Managed)
	}
	if err := m.Restore("copilot-jetbrains", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != original {
		t.Fatalf("还原后不一致:\n%s", got)
	}
}
