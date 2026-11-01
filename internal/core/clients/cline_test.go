package clients

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestApplyClineMultiFile 验证 Cline 的多文件写入：providers.json 必写，
// globalState/secrets 存在才写；三份文件各自留备份并可一并还原。
func TestApplyClineMultiFile(t *testing.T) {
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_DATA_DIR", "")
	home := t.TempDir()
	data := filepath.Join(home, ".cline", "data")
	if err := os.MkdirAll(filepath.Join(data, "settings"), 0o755); err != nil {
		t.Fatal(err)
	}
	providersPath := filepath.Join(data, "settings", "providers.json")
	statePath := filepath.Join(data, "globalState.json")
	secretsPath := filepath.Join(data, "secrets.json")

	origProviders := `{"providers":{"other":{"settings":{"apiKey":"sk-other"}}},"lastUsedProvider":"other"}`
	origState := `{"theme":"dark","actModeOpenAiModelInfo":{"old":true}}`
	origSecrets := `{"otherKey":"keep"}`
	for path, content := range map[string]string{providersPath: origProviders, statePath: origState, secretsPath: origSecrets} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	m := NewWithHome(home)
	if _, err := m.Apply("cline", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "gw-key", Model: "claude-sonnet-4-6"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var providers map[string]any
	raw, _ := os.ReadFile(providersPath)
	if err := json.Unmarshal(raw, &providers); err != nil {
		t.Fatal(err)
	}
	if providers["lastUsedProvider"] != "openai-compatible" {
		t.Fatalf("lastUsedProvider = %v", providers["lastUsedProvider"])
	}
	all := providers["providers"].(map[string]any)
	if _, ok := all["other"]; !ok {
		t.Fatal("用户既有 provider 被删")
	}
	slot := all["openai-compatible"].(map[string]any)["settings"].(map[string]any)
	if slot["apiKey"] != "gw-key" || slot["baseUrl"] != "http://127.0.0.1:7860/v1" || slot["model"] != "claude-sonnet-4-6" {
		t.Fatalf("aether 槽位 = %+v", slot)
	}

	var state map[string]any
	raw, _ = os.ReadFile(statePath)
	_ = json.Unmarshal(raw, &state)
	if state["actModeApiProvider"] != "openai" || state["openAiBaseUrl"] != "http://127.0.0.1:7860/v1" {
		t.Fatalf("state = %+v", state)
	}
	if state["theme"] != "dark" {
		t.Fatal("state 用户键被破坏")
	}
	if _, ok := state["actModeOpenAiModelInfo"]; ok {
		t.Fatal("旧的 modelInfo 未清理")
	}

	var secrets map[string]any
	raw, _ = os.ReadFile(secretsPath)
	_ = json.Unmarshal(raw, &secrets)
	if secrets["openAiApiKey"] != "gw-key" || secrets["otherKey"] != "keep" {
		t.Fatalf("secrets = %+v", secrets)
	}

	st := findStatus(t, m.List(), "cline")
	if st.Managed == nil || st.Managed.BaseURL != "http://127.0.0.1:7860/v1" || st.Managed.Model != "claude-sonnet-4-6" {
		t.Fatalf("managed = %+v", st.Managed)
	}

	// 一次注入 → 一次还原：三份文件都回到原始内容。
	if err := m.Restore("cline", ""); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	for path, want := range map[string]string{providersPath: origProviders, statePath: origState, secretsPath: origSecrets} {
		got, _ := os.ReadFile(path)
		if string(got) != want {
			t.Fatalf("%s 还原后不一致:\n%s", filepath.Base(path), got)
		}
	}
}

func TestApplyClineOnlyProviders(t *testing.T) {
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_DATA_DIR", "")
	home := t.TempDir()
	m := NewWithHome(home)
	if _, err := m.Apply("cline", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "k"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".cline", "data", "globalState.json")); err == nil {
		t.Fatal("globalState.json 原本不存在，不应被创建")
	}
	if _, err := os.Stat(filepath.Join(home, ".cline", "data", "secrets.json")); err == nil {
		t.Fatal("secrets.json 原本不存在，不应被创建")
	}
	if _, err := os.Stat(filepath.Join(home, ".cline", "data", "settings", "providers.json")); err != nil {
		t.Fatal("providers.json 应被创建")
	}
}
