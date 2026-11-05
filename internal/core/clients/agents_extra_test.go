package clients

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestApplyQwenCodeDropsLegacyEnvKey 验证升级清理：改版前写进 ~/.qwen/settings.json
// 的旧 env 键，以及引用旧键的 modelProviders 条目，都会被顺手清掉。
//
// 旧键不清理的后果：用户环境里长期残留一个与新键不同步的陈旧凭据，
// 而 managed 状态只认新键，用户看不到它、也不会去删。
func TestApplyQwenCodeDropsLegacyEnvKey(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)
	dir := filepath.Join(home, ".qwen")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := legacyQwenEnvKey()
	if legacy == "" || legacy == qwenEnvKey {
		t.Fatalf("legacy key 构造异常: %q", legacy)
	}
	// 模拟改版前的落盘结果：env 里有旧键，modelProviders 里有指向旧键的 aether 条目。
	orig := `{"theme":"dark","env":{"` + legacy + `":"old-secret"},"modelProviders":{"openai":[` +
		`{"id":"mine","name":"Mine","baseUrl":"https://relay.example/v1","envKey":"MY_KEY"},` +
		`{"id":"aether-old","name":"Aether","baseUrl":"http://127.0.0.1:7860/v1","envKey":"` + legacy + `"}]}}`
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Apply("qwen_code", ApplyParams{
		BaseURL: "http://127.0.0.1:7860/",
		APIKey:  "k1",
		Model:   "qwen3-max",
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, _ := os.ReadFile(path)
	var root struct {
		Env       map[string]any `json:"env"`
		Providers struct {
			OpenAI []struct {
				ID     string `json:"id"`
				EnvKey string `json:"envKey"`
			} `json:"openai"`
		} `json:"modelProviders"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("改写后不是合法 JSON: %v", err)
	}
	if _, still := root.Env[legacy]; still {
		t.Fatal("旧 env 键没有被清理，用户环境会残留陈旧凭据")
	}
	if got := root.Env[qwenEnvKey]; got != "k1" {
		t.Fatalf("新键应写入 k1，实际 %v", got)
	}
	// 用户自己的条目必须保留
	var keptMine bool
	for _, p := range root.Providers.OpenAI {
		if p.ID == "mine" {
			keptMine = true
		}
		if p.EnvKey == legacy {
			t.Fatalf("引用旧键的条目 %q 应被清理", p.ID)
		}
	}
	if !keptMine {
		t.Fatal("用户自己的 modelProviders 条目被误删")
	}
}

// TestApplyQwenCode 验证 Qwen Code 注入：用户条目保留、aether 条目写入、
// env/auth/model 三处开关落盘，且状态能被 managed 识别。
func TestApplyQwenCode(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)
	dir := filepath.Join(home, ".qwen")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	orig := `{"theme":"dark","model":{"name":"qwen3-max"},"modelProviders":{"openai":[{"id":"mine","name":"Mine","baseUrl":"https://relay.example/v1","envKey":"MY_KEY"}]}}`
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Apply("qwen_code", ApplyParams{
		BaseURL: "http://127.0.0.1:7860/",
		APIKey:  "k1",
		Model:   "qwen3-max",
		ModelOptions: []ModelOption{
			{Value: "qwen3-max", Label: "Qwen3 Max"},
			{Value: "kimi-k2", Label: "Kimi K2"},
		},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, _ := os.ReadFile(path)
	var root struct {
		Theme string `json:"theme"`
		Env   struct {
			AETHER string `json:"AETHER_QWEN_API_KEY"`
		} `json:"env"`
		Providers struct {
			OpenAI []struct {
				ID     string `json:"id"`
				Base   string `json:"baseUrl"`
				EnvKey string `json:"envKey"`
			} `json:"openai"`
		} `json:"modelProviders"`
		Security struct {
			Auth struct {
				Selected string `json:"selectedType"`
			} `json:"auth"`
		} `json:"security"`
		Model struct {
			Name string `json:"name"`
			Base string `json:"baseUrl"`
		} `json:"model"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("改写后不是合法 JSON: %v", err)
	}
	if root.Theme != "dark" {
		t.Fatal("用户配置被破坏")
	}
	if root.Env.AETHER != "k1" || root.Security.Auth.Selected != "openai" {
		t.Fatalf("env/auth = %+v %+v", root.Env, root.Security.Auth)
	}
	if root.Model.Name != "qwen3-max" || root.Model.Base != "http://127.0.0.1:7860/v1" {
		t.Fatalf("默认模型 = %+v", root.Model)
	}
	var mine, ours int
	for _, e := range root.Providers.OpenAI {
		if e.ID == "mine" && e.EnvKey == "MY_KEY" {
			mine++
		}
		if e.EnvKey == qwenEnvKey {
			ours++
			if e.Base != "http://127.0.0.1:7860/v1" {
				t.Fatalf("aether 条目 base = %q", e.Base)
			}
		}
	}
	if mine != 1 || ours != 2 {
		t.Fatalf("条目数 mine=%d ours=%d", mine, ours)
	}

	// 再注入一次：不应重复堆叠自己。
	if _, err := m.Apply("qwen_code", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "k1", Model: "qwen3-max"}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	_ = json.Unmarshal(data, &root)
	ours = 0
	for _, e := range root.Providers.OpenAI {
		if e.EnvKey == qwenEnvKey {
			ours++
		}
	}
	if ours != 1 {
		t.Fatalf("重复注入后 aether 条目 = %d", ours)
	}

	st := findStatus(t, m.List(), "qwen_code")
	if st.Managed == nil || st.Managed.BaseURL != "http://127.0.0.1:7860/v1" || st.Managed.Model != "qwen3-max" {
		t.Fatalf("managed = %+v", st.Managed)
	}
}

// TestApplyDroid 验证 Droid 注入：customModels 追加 aether 条目、
// 用户条目保留、默认模型指到 aether，managed 可识别。
func TestApplyDroid(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)
	dir := filepath.Join(home, ".factory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	orig := `{"customModels":[{"model":"m1","id":"custom:mine-0","displayName":"Mine","baseUrl":"https://x/v1","apiKey":"old","provider":"generic-chat-completion-api","noImageSupport":true}],"sessionDefaultSettings":{"model":"custom:mine-0"}}`
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Apply("droid", ApplyParams{
		BaseURL: "http://127.0.0.1:7860",
		APIKey:  "k1",
		Model:   "claude-sonnet-4-6",
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, _ := os.ReadFile(path)
	var root struct {
		CustomModels []struct {
			Model    string `json:"model"`
			ID       string `json:"id"`
			Base     string `json:"baseUrl"`
			APIKey   string `json:"apiKey"`
			Provider string `json:"provider"`
		} `json:"customModels"`
		Session struct {
			Model string `json:"model"`
		} `json:"sessionDefaultSettings"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	if len(root.CustomModels) != 2 {
		t.Fatalf("customModels = %+v", root.CustomModels)
	}
	if root.CustomModels[0].ID != "custom:mine-0" {
		t.Fatal("用户条目被破坏")
	}
	ours := root.CustomModels[1]
	if ours.ID != droidModelIDPrefix+"claude-sonnet-4-6" || ours.Model != "claude-sonnet-4-6" ||
		ours.Base != "http://127.0.0.1:7860/v1" || ours.APIKey != "k1" || ours.Provider != "generic-chat-completion-api" {
		t.Fatalf("aether 条目 = %+v", ours)
	}
	if root.Session.Model != droidModelIDPrefix+"claude-sonnet-4-6" {
		t.Fatalf("默认模型 = %q", root.Session.Model)
	}

	st := findStatus(t, m.List(), "droid")
	if st.Managed == nil || st.Managed.Model != "claude-sonnet-4-6" {
		t.Fatalf("managed = %+v", st.Managed)
	}
}

func findStatus(t *testing.T, list []Status, id string) Status {
	t.Helper()
	for _, s := range list {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("没有 %s 条目", id)
	return Status{}
}
