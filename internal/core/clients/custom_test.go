package clients

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCustomFileAndApplyEnv(t *testing.T) {
	home := t.TempDir()
	defs := `{"clients":[{"id":"qwen","name":"Qwen Code","kind":"envfile","path":"~/.qwen/.env",
		"keys":{"base_url":"OPENAI_BASE_URL","api_key":"OPENAI_API_KEY","model":"OPENAI_MODEL"}}]}`
	path := filepath.Join(home, "clients.json")
	if err := os.WriteFile(path, []byte(defs), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewWithHome(home)
	if err := m.LoadCustomFile(path); err != nil {
		t.Fatalf("LoadCustomFile: %v", err)
	}
	if _, ok := m.def("qwen"); !ok {
		t.Fatal("自定义客户端未注册")
	}

	if _, err := m.Apply("qwen", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "aether-local", Model: "qoder-cn/auto"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".qwen", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"OPENAI_BASE_URL=http://127.0.0.1:7860", "OPENAI_API_KEY=aether-local", "OPENAI_MODEL=qoder-cn/auto"} {
		if !strings.Contains(text, want) {
			t.Fatalf("缺少 %q:\n%s", want, text)
		}
	}

	// managedOf 要能识别自定义键名（不然后台会显示"未由网关管理"）。
	st, ok := m.Lookup("qwen")
	if !ok || st.Managed == nil || st.Managed.BaseURL == "" {
		t.Fatalf("托管状态未识别: %+v", st.Managed)
	}
}

func TestLoadCustomFileRejectsBadDefs(t *testing.T) {
	home := t.TempDir()
	cases := []string{
		`{"clients":[{"id":"","name":"x","path":"~/.x/.env","keys":{"base_url":"A","api_key":"B"}}]}`,
		`{"clients":[{"id":"x","name":"x","kind":"toml","path":"~/.x","keys":{"base_url":"A","api_key":"B"}}]}`,
		`{"clients":[{"id":"claude_code","name":"dup","path":"~/.x/.env","keys":{"base_url":"A","api_key":"B"}}]}`,
		`{"clients":[{"id":"x","name":"x","path":"relative/.env","keys":{"base_url":"A","api_key":"B"}}]}`,
	}
	for i, def := range cases {
		p := filepath.Join(home, "c"+string(rune('a'+i))+".json")
		if err := os.WriteFile(p, []byte(def), 0o600); err != nil {
			t.Fatal(err)
		}
		m := NewWithHome(home)
		if err := m.LoadCustomFile(p); err == nil {
			t.Fatalf("非法定义 #%d 应报错", i)
		}
	}
}

// MiMo Code 与 opencode 同源：写入 provider.aether 时必须保留用户自己的 provider，
// 且用对 npm 包名（它自带的是 @ai-sdk/openai）。
func TestApplyMimocodeKeepsOtherProviders(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "mimocode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 结构与真实文件一致（密钥已替换为占位符，测试不接触真实凭据）。
	orig := `{
  "$schema": "https://mimo.xiaomi.com/mimocode/config.json",
  "provider": {
    "xiaomi": {
      "npm": "@ai-sdk/openai",
      "models": { "mimo-v2.6-pro": {} },
      "options": { "apiKey": "redacted-in-test" }
    }
  }
}
`
	path := filepath.Join(dir, "mimocode.jsonc")
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewWithHome(home)
	if _, err := m.Apply("mimocode", ApplyParams{
		BaseURL: "http://127.0.0.1:7860", APIKey: "aether-local", Model: "qoder-cn/auto",
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	raw, _ := os.ReadFile(path)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("结果不是合法 JSON: %v", err)
	}
	prov, _ := got["provider"].(map[string]any)
	if _, ok := prov["xiaomi"]; !ok {
		t.Fatal("用户原有 provider 被丢掉")
	}
	aether, ok := prov["aether"].(map[string]any)
	if !ok {
		t.Fatal("provider.aether 未写入")
	}
	if aether["npm"] != "@ai-sdk/openai" {
		t.Fatalf("npm = %v（应为 mimocode 自带的包名）", aether["npm"])
	}
	opts, _ := aether["options"].(map[string]any)
	if opts["baseURL"] != "http://127.0.0.1:7860/v1" {
		t.Fatalf("baseURL = %v", opts["baseURL"])
	}
	if got["model"] != "aether/qoder-cn/auto" {
		t.Fatalf("model = %v", got["model"])
	}

	st, ok := m.Lookup("mimocode")
	if !ok || st.Managed == nil || st.Managed.BaseURL == "" {
		t.Fatalf("托管状态未识别: %+v", st.Managed)
	}

	// 带注释的 JSONC 仍拒绝（无法安全合并）。
	if err := os.WriteFile(path, []byte("// comment\n"+orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Apply("mimocode", ApplyParams{BaseURL: "http://x", APIKey: "k"}); err == nil {
		t.Fatal("含注释的 JSONC 应被拒绝改写")
	}
}
