package clients

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wslHome 造一个模拟的 WSL 家目录（普通临时目录即可：路径处理逻辑相同）。
func wslHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

func TestTargetsExpandDefs(t *testing.T) {
	m := NewWithHome(t.TempDir())
	home := wslHome(t)
	m.SetTargets([]Target{{ID: "wsl-ubuntu-22-04", Label: "WSL Ubuntu-22.04", Home: home, WSL: "Ubuntu-22.04"}})

	var target, builtin *Status
	list := m.List()
	for i := range list {
		switch list[i].ID {
		case "wsl-ubuntu-22-04--claude_code":
			target = &list[i]
		case "claude_code":
			builtin = &list[i]
		}
	}
	if target == nil {
		t.Fatalf("List() 里没有 WSL 条目：%+v", list)
	}
	if builtin == nil {
		t.Fatal("WSL 目标不应挤掉本机条目")
	}
	if target.WSL != "Ubuntu-22.04" || target.Target != "wsl-ubuntu-22-04" {
		t.Errorf("WSL 标记错误：%+v", target)
	}
	want := filepath.Join(home, ".claude", "settings.json")
	if target.Path != want {
		t.Errorf("Path = %q，want %q", target.Path, want)
	}
	if target.Detected {
		t.Error("还没写文件，Detected 应为 false")
	}
	if !strings.Contains(target.Name, "WSL Ubuntu-22.04") {
		t.Errorf("Name 应带目标名：%q", target.Name)
	}
}

func TestWSLTargetApplyAndRestore(t *testing.T) {
	m := NewWithHome(t.TempDir())
	home := wslHome(t)
	m.SetTargets([]Target{{ID: "wsl-ubuntu", Label: "WSL Ubuntu", Home: home, WSL: "Ubuntu"}})
	id := "wsl-ubuntu--claude_code"

	if _, err := m.Apply(id, ApplyParams{BaseURL: "http://172.27.16.1:7860", APIKey: "sk-wsl"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("读取写入结果: %v", err)
	}
	if !strings.Contains(string(raw), "http://172.27.16.1:7860") {
		t.Fatalf("settings.json 未写入 WSL 入口地址：%s", raw)
	}

	// 写入后状态必须立刻可见（缓存要失效，而不是等 TTL）。
	st, ok := m.Lookup(id)
	if !ok || !st.Detected {
		t.Fatalf("Lookup(%s) detected=%v ok=%v", id, st.Detected, ok)
	}
	if st.Managed == nil || st.Managed.BaseURL != "http://172.27.16.1:7860" {
		t.Errorf("Managed = %+v", st.Managed)
	}

	if err := m.Restore(id, ""); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("首次接入后断开应删除文件，stat err = %v", err)
	}
}

func TestApplyToleratesBOM(t *testing.T) {
	m := NewWithHome(t.TempDir())
	path := filepath.Join(m.home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"env":{"KEEP":"me"}}`)...)
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Apply("claude_code", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "k"}); err != nil {
		t.Fatalf("带 BOM 的 settings.json 应能改写：%v", err)
	}
	raw, _ := os.ReadFile(path)
	// 解析时剥 BOM；但写完要把 BOM 还回去，否则用户用记事本再打开是乱码。
	if !bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		t.Error("原文件有 BOM，写回时应该保留")
	}
	var root map[string]any
	if err := json.Unmarshal(bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}), &root); err != nil {
		t.Fatalf("写入结果不是合法 JSON: %v\n%s", err, raw)
	}
	env, _ := root["env"].(map[string]any)
	if env["KEEP"] != "me" {
		t.Errorf("用户字段 KEEP 丢了：%+v", env)
	}
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:7860" {
		t.Errorf("base_url = %v", env["ANTHROPIC_BASE_URL"])
	}
}

// 无 BOM 的文件不能被凭空加上 BOM：那会让 Node 的 JSON.parse / 多数 TOML 库报错。
func TestApplyDoesNotAddBOM(t *testing.T) {
	m := NewWithHome(t.TempDir())
	path := filepath.Join(m.home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"env":{"KEEP":"me"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Apply("claude_code", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "k"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		t.Error("原文件没有 BOM，不该凭空加一个")
	}
}

// 带注释的 JSONC 不再被拒绝：保注释写盘路径只替换顶层成员。
func TestApplyPreservesJSONCComments(t *testing.T) {
	m := NewWithHome(t.TempDir())
	path := filepath.Join(m.home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "// 我的说明\n{\n  // 保留我自己的键\n  \"env\": {\"KEEP\": \"me\"},\n  \"customTool\": {\"a\": 1}\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Apply("claude_code", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "k"}); err != nil {
		t.Fatalf("带注释的 JSONC 应能改写：%v", err)
	}
	raw, _ := os.ReadFile(path)
	out := string(raw)
	if !strings.Contains(out, "// 我的说明") {
		t.Errorf("顶层注释被丢了：\n%s", out)
	}
	if !strings.Contains(out, "customTool") {
		t.Errorf("用户自定义的顶层键被丢了：\n%s", out)
	}
	if !strings.Contains(out, "http://127.0.0.1:7860") {
		t.Errorf("没有写入 base_url：\n%s", out)
	}
	if !strings.Contains(out, `"KEEP"`) {
		t.Errorf("env 里的用户键被丢了：\n%s", out)
	}
}

func TestTargetsDoNotDuplicateAbsoluteCustom(t *testing.T) {
	m := NewWithHome(t.TempDir())
	abs := filepath.Join(t.TempDir(), "custom.env")
	m.custom = []clientDef{{
		id: "mine", name: "Mine", kind: KindEnvFile, absPath: abs, custom: true,
		env: &envKeys{baseURL: "BASE", apiKey: "KEY"},
	}}
	m.SetTargets([]Target{{ID: "wsl-x", Label: "WSL X", Home: t.TempDir(), WSL: "X"}})
	count := 0
	for _, d := range m.allDefs() {
		if strings.Contains(d.id, "mine") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("绝对路径的自定义客户端被复制了 %d 份，want 1", count)
	}
}
