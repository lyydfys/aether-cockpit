package clients

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyCodebuddy(t *testing.T) {
	t.Setenv("CODEBUDDY_CONFIG_DIR", "")
	home := t.TempDir()
	dir := filepath.Join(home, ".codebuddy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	origModels := `{"models":[{"id":"other","name":"Other","vendor":"someone","url":"https://x/v1/chat/completions","apiKey":"k"}],"availableModels":["other"]}`
	origSettings := `{"model":"other","theme":"dark"}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(origModels), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(origSettings), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewWithHome(home)
	if _, err := m.Apply("codebuddy", ApplyParams{
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

	var doc struct {
		Models []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Vendor string `json:"vendor"`
			APIKey string `json:"apiKey"`
			URL    string `json:"url"`
		} `json:"models"`
		AvailableModels []string `json:"availableModels"`
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "models.json"))
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Models) != 3 {
		t.Fatalf("models = %d", len(doc.Models))
	}
	if doc.Models[0].ID != "other" || doc.Models[0].Vendor != "someone" {
		t.Fatal("用户模型被破坏")
	}
	ours := doc.Models[2]
	if ours.Vendor != "aether" || ours.APIKey != "gw-key" || ours.URL != "http://127.0.0.1:7860/v1/chat/completions" {
		t.Fatalf("aether 模型 = %+v", ours)
	}
	if len(doc.AvailableModels) != 3 {
		t.Fatalf("availableModels = %v", doc.AvailableModels)
	}

	var settings map[string]any
	raw, _ = os.ReadFile(filepath.Join(dir, "settings.json"))
	_ = json.Unmarshal(raw, &settings)
	if settings["model"] != "claude-sonnet-4-6" || settings["theme"] != "dark" {
		t.Fatalf("settings = %+v", settings)
	}

	// 重复注入不堆叠。
	if _, err := m.Apply("codebuddy", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "gw-key", Model: "claude-sonnet-4-6"}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "models.json"))
	if c := strings.Count(string(raw), `"vendor": "aether"`); c != 1 {
		t.Fatalf("重复注入后 aether 模型数 = %d", c)
	}

	st := findStatus(t, m.List(), "codebuddy")
	if st.Managed == nil || st.Managed.BaseURL != "http://127.0.0.1:7860/v1" || st.Managed.Model != "claude-sonnet-4-6" {
		t.Fatalf("managed = %+v", st.Managed)
	}

	// 还原语义：用一份全新目录只注入一次再还原，两份文件逐字回到原文。
	home2 := t.TempDir()
	dir2 := filepath.Join(home2, ".codebuddy")
	os.MkdirAll(dir2, 0o755)
	os.WriteFile(filepath.Join(dir2, "models.json"), []byte(origModels), 0o600)
	os.WriteFile(filepath.Join(dir2, "settings.json"), []byte(origSettings), 0o600)
	m2 := NewWithHome(home2)
	if _, err := m2.Apply("codebuddy", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "gw", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := m2.Restore("codebuddy", ""); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir2, "models.json"))
	if string(got) != origModels {
		t.Fatalf("models.json 还原后不一致:\n%s", got)
	}
	got, _ = os.ReadFile(filepath.Join(dir2, "settings.json"))
	if string(got) != origSettings {
		t.Fatalf("settings.json 还原后不一致:\n%s", got)
	}
}

func TestApplyCodebuddyArrayForm(t *testing.T) {
	t.Setenv("CODEBUDDY_CONFIG_DIR", "")
	home := t.TempDir()
	dir := filepath.Join(home, ".codebuddy")
	os.MkdirAll(dir, 0o755)
	array := `[{"id":"mine","name":"Mine","vendor":"x","url":"https://x","apiKey":"k"}]`
	path := filepath.Join(dir, "models.json")
	os.WriteFile(path, []byte(array), 0o600)

	m := NewWithHome(home)
	if _, err := m.Apply("codebuddy", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "gw", Model: "m"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	raw, _ := os.ReadFile(path)
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("数组形态结果不是合法 JSON: %v\n%s", err, raw)
	}
	if len(list) != 2 || list[0]["id"] != "mine" || list[1]["vendor"] != "aether" {
		t.Fatalf("list = %+v", list)
	}
}
