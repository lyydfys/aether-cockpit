package clients

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyClaudeJSONMerge(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)

	// 预置一个已有其它配置的 settings.json。
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{"permissions": {"allow": ["Bash"]}, "theme": "dark"}`
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	bak, err := m.Apply("claude_code", ApplyParams{BaseURL: "http://127.0.0.1:7860/", APIKey: "k1", Model: "claude-sonnet-4-6"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if bak == "" {
		t.Fatal("预期产生备份")
	}

	data, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("改写后不是合法 JSON: %v", err)
	}
	env, _ := root["env"].(map[string]any)
	if env == nil || env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:7860" {
		t.Errorf("BASE_URL 未写入: %v", root)
	}
	if env["ANTHROPIC_MODEL"] != "claude-sonnet-4-6" {
		t.Errorf("MODEL 未写入: %v", env)
	}
	// 用户已有的键必须原样保留。
	if root["theme"] != "dark" {
		t.Errorf("用户配置被破坏: %v", root)
	}
	if _, ok := root["permissions"]; !ok {
		t.Errorf("permissions 丢失: %v", root)
	}

	// 恢复后回到原始内容。
	if err := m.Restore("claude_code", ""); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if strings.TrimSpace(string(after)) != strings.TrimSpace(original) {
		t.Errorf("恢复后内容不一致:\n%s\nwant\n%s", after, original)
	}
}

func TestApplyCodexTOMLIdempotent(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)

	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := `# 用户自己的注释
model = "gpt-5.2"
model_provider = "waw"

[model_providers.waw]
name = "WawAPI"
base_url = "https://relay.example.com/v1"
`
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	p := ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "aether-local", Model: "claude-sonnet-4-6"}
	if _, err := m.Apply("codex_cli", p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	afterFirst, _ := os.ReadFile(filepath.Join(codexDir, "config.toml"))

	// 连续应用两次：托管块与顶层键不得堆积。
	if _, err := m.Apply("codex_cli", p); err != nil {
		t.Fatalf("Apply #2: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(codexDir, "config.toml"))
	text := string(data)
	if got := strings.Count(text, "[model_providers.aether]"); got != 1 {
		t.Errorf("aether 块出现 %d 次，want 1:\n%s", got, text)
	}
	if got := strings.Count(text, `model_provider = "aether"`); got != 1 {
		t.Errorf("model_provider 行出现 %d 次，want 1:\n%s", got, text)
	}
	if got := strings.Count(text, "model_provider ="); got != 1 {
		t.Errorf("旧的 model_provider=%q 未被替换（会产生 TOML 重复键）:\n%s", "waw", text)
	}
	if got := strings.Count(text, "\nmodel ="); got != 1 {
		t.Errorf("model 顶层键出现 %d 次，want 1:\n%s", got, text)
	}
	if !strings.Contains(text, "https://relay.example.com/v1") {
		t.Errorf("用户自己的 provider 被破坏:\n%s", text)
	}
	if !strings.Contains(text, "# 用户自己的注释") {
		t.Errorf("注释被破坏:\n%s", text)
	}
	if !strings.Contains(text, `model = "claude-sonnet-4-6"`) {
		t.Errorf("model 行未写入:\n%s", text)
	}

	// 恢复 = 撤销最近一次应用：回到第一次应用后的内容。
	if err := m.Restore("codex_cli", ""); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(codexDir, "config.toml"))
	if strings.TrimSpace(string(after)) != strings.TrimSpace(string(afterFirst)) {
		t.Errorf("恢复后内容与上次应用前不一致:\n%s", after)
	}
}

func TestApplyGeminiEnvFile(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)

	if _, err := m.Apply("gemini_cli", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "k"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	path := filepath.Join(home, ".gemini", ".env")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("env 文件未创建: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "GOOGLE_GEMINI_BASE_URL=http://127.0.0.1:7860/v1beta") {
		t.Errorf("BASE_URL 未写入:\n%s", text)
	}
	if !strings.Contains(text, "GEMINI_API_KEY=k") {
		t.Errorf("API_KEY 未写入:\n%s", text)
	}

	// 二次应用：键不得重复。
	if _, err := m.Apply("gemini_cli", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "k2"}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if got := strings.Count(string(data), "GEMINI_API_KEY="); got != 1 {
		t.Errorf("GEMINI_API_KEY 出现 %d 次，want 1:\n%s", got, data)
	}
}

func TestApplyOpenCodeJSONMerge(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)

	// 预置一个已有其它配置的 opencode.json：含用户自己的 provider、
	// 一个旧的 aether 条目（应被整体覆盖）和用户默认 model（应被替换）。
	// schema 依据 https://opencode.ai/docs/providers/。
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{
  "$schema": "https://opencode.ai/config.json",
  "theme": "opencode",
  "model": "ollama/llama2",
  "provider": {
    "ollama": {
      "npm": "@ai-sdk/openai-compatible",
      "options": {"baseURL": "http://localhost:11434/v1"}
    },
    "aether": {
      "npm": "@ai-sdk/openai-compatible",
      "options": {"baseURL": "http://old.example.com:1/v1", "apiKey": "stale"}
    }
  }
}`
	path := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	p := ApplyParams{BaseURL: "http://127.0.0.1:7860/", APIKey: "k1", Model: "claude-sonnet-4-6"}
	bak1, err := m.Apply("opencode", p)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if bak1 == "" {
		t.Fatal("预期产生备份")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("改写后不是合法 JSON: %v", err)
	}
	provider, _ := root["provider"].(map[string]any)
	aether, _ := provider["aether"].(map[string]any)
	if aether == nil {
		t.Fatalf("provider.aether 未写入: %v", root)
	}
	if aether["npm"] != "@ai-sdk/openai-compatible" {
		t.Errorf("npm 适配器未写入: %v", aether)
	}
	opts, _ := aether["options"].(map[string]any)
	if opts == nil || opts["baseURL"] != "http://127.0.0.1:7860/v1" {
		t.Errorf("baseURL 未写入（应去末尾斜杠并追加 /v1）: %v", aether)
	}
	if opts == nil || opts["apiKey"] != "k1" {
		t.Errorf("apiKey 未写入: %v", aether)
	}
	if root["model"] != "aether/claude-sonnet-4-6" {
		t.Errorf("默认 model 未写入: %v", root)
	}
	// 用户已有的键与其它 provider 必须原样保留。
	if root["theme"] != "opencode" || root["$schema"] != "https://opencode.ai/config.json" {
		t.Errorf("用户配置被破坏: %v", root)
	}
	if _, ok := provider["ollama"]; !ok {
		t.Errorf("用户自己的 provider 丢失: %v", provider)
	}

	// 状态探测：报告 baseURL 与 model，不回显密钥。
	d, ok := m.def("opencode")
	if !ok {
		t.Fatal("opencode 定义缺失")
	}
	if st := m.statusOf(d); st.Managed == nil ||
		st.Managed.BaseURL != "http://127.0.0.1:7860/v1" ||
		st.Managed.Model != "aether/claude-sonnet-4-6" {
		t.Errorf("Managed 探测异常: %+v", st.Managed)
	}

	// 二次应用幂等：内容逐字节一致。
	afterFirst := append([]byte(nil), data...)
	if _, err := m.Apply("opencode", p); err != nil {
		t.Fatalf("Apply #2: %v", err)
	}
	data2, _ := os.ReadFile(path)
	if string(data2) != string(afterFirst) {
		t.Errorf("二次应用后内容变化:\n%s\nwant\n%s", data2, afterFirst)
	}

	// 恢复 = 撤销最近一次应用：回到第一次应用后的内容。
	// （备份文件名精确到秒，同一秒内的第二次 Apply 会覆盖第一份备份，
	// 因此这里只断言"撤销最近一次"，完整还原到原始内容见空 Model 测试。）
	if err := m.Restore("opencode", ""); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	after, _ := os.ReadFile(path)
	if strings.TrimSpace(string(after)) != strings.TrimSpace(string(afterFirst)) {
		t.Errorf("恢复后内容与上次应用前不一致:\n%s", after)
	}
}

func TestApplyOpenCodeEmptyModelKeepsUserModel(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)

	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{"model": "ollama/llama2"}`
	path := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Apply("opencode", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "k"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(path)
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	// Model 为空时只写 provider，用户的默认 model 保持不动。
	if root["model"] != "ollama/llama2" {
		t.Errorf("Model 为空时不应改写用户默认 model: %v", root)
	}
	provider, _ := root["provider"].(map[string]any)
	if provider == nil || provider["aether"] == nil {
		t.Errorf("provider.aether 未写入: %v", root)
	}

	// 只应用过一次，恢复应回到用户原始内容。
	if err := m.Restore("opencode", ""); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	after, _ := os.ReadFile(path)
	if strings.TrimSpace(string(after)) != strings.TrimSpace(original) {
		t.Errorf("恢复后内容不一致:\n%s\nwant\n%s", after, original)
	}
}

func TestApplyOpenCodePreservesJSONC(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)

	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// opencode.json 官方允许 JSONC（带注释）。以前一律拒写，等于"用户正常的
	// 配置反而接不进网关"；现在走保注释路径，只替换顶层 provider / model。
	jsonc := "// my config\n{\n  // 我的主题\n  \"theme\": \"opencode\",\n  \"provider\": {\"other\": {\"npm\": \"x\"}}\n}\n"
	path := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(path, []byte(jsonc), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Apply("opencode", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "k"}); err != nil {
		t.Fatalf("带注释的 opencode.json 应能改写：%v", err)
	}
	after, _ := os.ReadFile(path)
	out := string(after)
	if !strings.Contains(out, "// my config") || !strings.Contains(out, "// 我的主题") {
		t.Errorf("注释被丢了：\n%s", out)
	}
	if !strings.Contains(out, `"theme"`) || !strings.Contains(out, "other") {
		t.Errorf("用户其它配置被丢了：\n%s", out)
	}
	if !strings.Contains(out, "http://127.0.0.1:7860/v1") {
		t.Errorf("没有写入 aether provider：\n%s", out)
	}
}

func TestBackupsPruned(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)
	claudeDir := filepath.Join(home, ".claude")
	os.MkdirAll(claudeDir, 0o755)
	p := filepath.Join(claudeDir, "settings.json")
	os.WriteFile(p, []byte(`{}`), 0o600)

	for i := 0; i < 8; i++ {
		// 备份文件名精确到秒；用微小内容差异强制每次都新建。
		os.WriteFile(p, []byte(`{"n":`+itoa(i)+`}`), 0o600)
		if _, err := m.Apply("claude_code", ApplyParams{BaseURL: "http://x"}); err != nil {
			t.Fatal(err)
		}
	}
	st := m.statusOf(defs[0])
	if len(st.Backups) > maxBackups {
		t.Errorf("备份未裁剪: %d 份", len(st.Backups))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestPreviewApplyDoesNotWrite(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{"theme":"dark"}`
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	changes, err := m.PreviewApply("claude_code", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "k", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Path != path || changes[0].Created {
		t.Fatalf("changes = %#v", changes)
	}
	found := false
	for _, l := range changes[0].Lines {
		if l.Op == "+" && strings.Contains(l.Text, "ANTHROPIC_BASE_URL") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diff 缺少新增行: %#v", changes[0].Lines)
	}
	after, _ := os.ReadFile(path)
	if strings.TrimSpace(string(after)) != original {
		t.Fatalf("preview 不应改写文件: %s", after)
	}
}

func TestPreviewRestore(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	original := `{"theme":"dark"}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Apply("claude_code", ApplyParams{BaseURL: "http://127.0.0.1:1", APIKey: "k", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	managed, _ := os.ReadFile(path)
	changes, err := m.PreviewRestore("claude_code")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || len(changes[0].Lines) == 0 {
		t.Fatalf("changes = %#v", changes)
	}
	hasDel, hasBack := false, false
	for _, l := range changes[0].Lines {
		if l.Op == "-" {
			hasDel = true
		}
		if l.Op == "+" && strings.Contains(l.Text, "theme") {
			hasBack = true
		}
	}
	if !hasDel || !hasBack {
		t.Fatalf("还原 diff 不完整: %#v", changes[0].Lines)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(managed) {
		t.Fatal("preview 不应改写文件")
	}
}

func TestRestoreDeletesFileCreatedByApply(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)
	path := filepath.Join(home, ".claude", "settings.json")
	if _, err := m.Apply("claude_code", ApplyParams{BaseURL: "http://127.0.0.1:1", APIKey: "k", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("首次接入应创建文件: %v", err)
	}
	changes, err := m.PreviewRestore("claude_code")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || !changes[0].Removed {
		t.Fatalf("preview = %#v", changes)
	}
	if err := m.Restore("claude_code", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("断开首次接入应删除文件: %v", err)
	}
}

func TestApplyClaudeTierModels(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)
	if _, err := m.Apply("claude_code", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "k", Model: "auto/glm-5.2"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
		if root.Env[key] != "auto/glm-5.2" {
			t.Fatalf("%s = %q", key, root.Env[key])
		}
	}
}

func TestApplyClaudeModelPicker(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)
	opts := []ModelOption{
		{Value: "auto/glm-5.2", Label: "GLM-5.2", Description: "路由组"},
		{Value: "mock-native/echo-pro", Label: "Echo Pro", Description: "Mock"},
	}
	if _, err := m.Apply("claude_code", ApplyParams{BaseURL: "http://127.0.0.1:1", APIKey: "k", Model: "auto/glm-5.2", ModelOptions: opts}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	var root struct {
		ModelPicker struct {
			Options []struct {
				Model       string `json:"model"`
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"modelPicker"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	if len(root.ModelPicker.Options) != 2 || root.ModelPicker.Options[0].Model != "auto/glm-5.2" {
		t.Fatalf("modelPicker = %#v", root.ModelPicker)
	}
	if !strings.Contains(root.ModelPicker.Options[0].Description, "via Aether Cockpit") {
		t.Fatalf("description = %q", root.ModelPicker.Options[0].Description)
	}

	// 用户自己写的 modelPicker 不能被覆盖。
	dir := filepath.Join(home, ".claude")
	path := filepath.Join(dir, "settings.json")
	_ = os.WriteFile(path, []byte(`{"modelPicker":{"options":[{"model":"my-model","label":"Mine"}]}}`), 0o600)
	if _, err := m.Apply("claude_code", ApplyParams{BaseURL: "http://127.0.0.1:1", APIKey: "k", Model: "auto/glm-5.2", ModelOptions: opts}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	if !strings.Contains(string(raw), `"my-model"`) {
		t.Fatalf("用户 picker 被覆盖: %s", raw)
	}
}

func TestApplyCodexBearerToken(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := ApplyParams{
		BaseURL: "http://127.0.0.1:7860", APIKey: "aether-local",
		Model: "workbuddy-intl/gpt-5.3-codex", CodexBearerToken: "aether-local",
	}
	if _, err := m.Apply("codex_cli", p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// 重复应用仍然只有一个 provider 块、一行 token。
	if _, err := m.Apply("codex_cli", p); err != nil {
		t.Fatalf("Apply #2: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(codexDir, "config.toml"))
	text := string(data)
	if got := strings.Count(text, "[model_providers.aether]"); got != 1 {
		t.Fatalf("provider 块 = %d: %s", got, text)
	}
	if !strings.Contains(text, `experimental_bearer_token = "aether-local"`) {
		t.Errorf("缺少 bearer token: %s", text)
	}
	if !strings.Contains(text, "requires_openai_auth = false") {
		t.Errorf("缺少 requires_openai_auth: %s", text)
	}
	if strings.Contains(text, `env_key = "AETHER_API_KEY"`) {
		t.Errorf("桌面端不应依赖进程环境变量: %s", text)
	}
	if got := strings.Count(text, "experimental_bearer_token"); got != 1 {
		t.Errorf("token 行 = %d: %s", got, text)
	}
}

func TestCodexCatalogWritten(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("model_provider = \"custom\"\nmodel = \"old\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := []ModelOption{{Value: "mock-native/echo-pro", Label: "Echo Pro", Description: "Mock"}, {Value: "auto/x", Label: "X"}}
	if _, err := m.Apply("codex_cli", ApplyParams{BaseURL: "http://127.0.0.1:1", APIKey: "k", Model: "mock-native/echo-pro", ModelOptions: opts}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(path)
	if !strings.Contains(string(cfg), `model_catalog_json = "aether-models.json"`) {
		t.Fatalf("config = %s", cfg)
	}
	cat, err := os.ReadFile(filepath.Join(dir, "aether-models.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	if err := json.Unmarshal(cat, &out); err != nil || len(out.Models) != 2 || out.Models[0].Slug != "mock-native/echo-pro" {
		t.Fatalf("catalog = %s (%v)", cat, err)
	}

	// 用户自己的 catalog 键不能被覆盖。
	userCfg := "model_provider = \"custom\"\nmodel_catalog_json = \"local-provider-switch-model-catalog.json\"\n"
	if err := os.WriteFile(path, []byte(userCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(dir, "aether-models.json"))
	if _, err := m.Apply("codex_cli", ApplyParams{BaseURL: "http://127.0.0.1:1", APIKey: "k", Model: "mock-native/echo-pro", ModelOptions: opts}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = os.ReadFile(path)
	if !strings.Contains(string(cfg), "local-provider-switch-model-catalog.json") || strings.Contains(string(cfg), "aether-models.json") {
		t.Fatalf("用户 catalog 被改: %s", cfg)
	}
	if _, err := os.Stat(filepath.Join(dir, "aether-models.json")); !os.IsNotExist(err) {
		t.Fatal("不应写自己的 catalog")
	}
}

func TestCodexCatalogRemovedOnRestore(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)
	dir := filepath.Join(home, ".codex")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "config.toml")
	orig := "model_provider = \"custom\"\nmodel = \"old\"\n"
	_ = os.WriteFile(path, []byte(orig), 0o600)
	if _, err := m.Apply("codex_cli", ApplyParams{BaseURL: "http://127.0.0.1:1", APIKey: "k", Model: "m", ModelOptions: []ModelOption{{Value: "m"}}}); err != nil {
		t.Fatal(err)
	}
	catPath := filepath.Join(dir, "aether-models.json")
	if _, err := os.Stat(catPath); err != nil {
		t.Fatal("catalog 未写入")
	}
	changes, err := m.PreviewRestore("codex_cli")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range changes {
		if c.Path == catPath && c.Removed {
			found = true
		}
	}
	if !found {
		t.Fatalf("预览缺少 catalog 删除: %#v", changes)
	}
	if err := m.Restore("codex_cli", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(catPath); !os.IsNotExist(err) {
		t.Fatal("断开后 catalog 未删除")
	}
	got, _ := os.ReadFile(path)
	if string(got) != orig {
		t.Fatalf("config 未还原: %q", got)
	}
}
