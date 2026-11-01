package clients

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestApplyZed 验证 Zed 的 settings 注入与“Key 先写凭据再改文件”。
func TestApplyZed(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, "zedcfg")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZED_CONFIG_DIR", cfgDir)
	settingsPath := filepath.Join(cfgDir, "settings.json")
	original := `{"theme":"One Dark","ui_font_size":15,"language_models":{"openai_compatible":{"other":{"api_url":"https://x/v1"}}}}`
	if err := os.WriteFile(settingsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	var wroteURL, wroteKey, deletedURL string
	prevW, prevD := zedWriteCredential, zedDeleteCredential
	zedWriteCredential = func(url, key string) error { wroteURL, wroteKey = url, key; return nil }
	zedDeleteCredential = func(url string) error { deletedURL = url; return nil }
	t.Cleanup(func() { zedWriteCredential, zedDeleteCredential = prevW, prevD })

	m := NewWithHome(home)
	if _, err := m.Apply("zed", ApplyParams{
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
	if wroteURL != "http://127.0.0.1:7860/v1" || wroteKey != "gw-key" {
		t.Fatalf("凭据 = %q %q", wroteURL, wroteKey)
	}

	var doc struct {
		Theme     string `json:"theme"`
		UIFont    int    `json:"ui_font_size"`
		LanguageM struct {
			OpenAICompatible map[string]struct {
				APIURL string `json:"api_url"`
				Models []struct {
					Name string `json:"name"`
				} `json:"available_models"`
			} `json:"openai_compatible"`
		} `json:"language_models"`
		Agent struct {
			DefaultModel struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
			} `json:"default_model"`
		} `json:"agent"`
	}
	raw, _ := os.ReadFile(settingsPath)
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Theme != "One Dark" || doc.UIFont != 15 {
		t.Fatalf("用户设置被破坏: %+v", doc)
	}
	if _, ok := doc.LanguageM.OpenAICompatible["other"]; !ok {
		t.Fatal("其它 provider 被删除")
	}
	aether := doc.LanguageM.OpenAICompatible["aether"]
	if aether.APIURL != "http://127.0.0.1:7860/v1" || len(aether.Models) != 2 {
		t.Fatalf("aether provider = %+v", aether)
	}
	if doc.Agent.DefaultModel.Provider != "aether" || doc.Agent.DefaultModel.Model != "claude-sonnet-4-6" {
		t.Fatalf("default_model = %+v", doc.Agent.DefaultModel)
	}

	if st := findStatus(t, m.List(), "zed"); st.Managed == nil || st.Managed.BaseURL != "http://127.0.0.1:7860/v1" {
		t.Fatalf("managed = %+v", st.Managed)
	}

	// 还原：settings 回原文，并删除该 URL 的系统凭据。
	if err := m.Restore("zed", ""); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, _ := os.ReadFile(settingsPath)
	if string(got) != original {
		t.Fatalf("settings 还原后不一致:\n%s", got)
	}
	if deletedURL != "http://127.0.0.1:7860/v1" {
		t.Fatalf("还原时未删除凭据: %q", deletedURL)
	}
}

// TestApplyZedCredentialFailureLeavesFile 验证凭据写失败时不改设置（不做假接入）。
func TestApplyZedCredentialFailureLeavesFile(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, "zedcfg")
	os.MkdirAll(cfgDir, 0o755)
	t.Setenv("ZED_CONFIG_DIR", cfgDir)
	settingsPath := filepath.Join(cfgDir, "settings.json")
	original := `{"theme":"x"}`
	os.WriteFile(settingsPath, []byte(original), 0o600)

	prevW := zedWriteCredential
	zedWriteCredential = func(url, key string) error { return os.ErrPermission }
	t.Cleanup(func() { zedWriteCredential = prevW })

	m := NewWithHome(home)
	if _, err := m.Apply("zed", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "k", Model: "m"}); err == nil {
		t.Fatal("凭据写失败时 Apply 应返回错误")
	}
	got, _ := os.ReadFile(settingsPath)
	if string(got) != original {
		t.Fatalf("凭据失败后 settings 不应被改写:\n%s", got)
	}
}
