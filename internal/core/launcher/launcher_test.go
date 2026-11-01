package launcher

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBuildEnvWithModel(t *testing.T) {
	// origin 带末尾斜杠应被修剪，OPENAI_BASE_URL 追加 /v1。
	env := BuildEnv("http://127.0.0.1:7860/", "sk-test", "claude-sonnet-4-6")
	want := map[string]string{
		"ANTHROPIC_BASE_URL":   "http://127.0.0.1:7860",
		"ANTHROPIC_AUTH_TOKEN": "sk-test",
		"ANTHROPIC_API_KEY":    "sk-test",
		"OPENAI_BASE_URL":      "http://127.0.0.1:7860/v1",
		"OPENAI_API_KEY":       "sk-test",
		"ANTHROPIC_MODEL":      "claude-sonnet-4-6",
		"OPENAI_MODEL":         "claude-sonnet-4-6",
	}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("BuildEnv() = %v, want %v", env, want)
	}
}

func TestBuildEnvWithoutModel(t *testing.T) {
	env := BuildEnv("http://127.0.0.1:7860", "aether-local", "")
	if _, ok := env["ANTHROPIC_MODEL"]; ok {
		t.Fatalf("model 为空时不应注入 ANTHROPIC_MODEL")
	}
	if _, ok := env["OPENAI_MODEL"]; ok {
		t.Fatalf("model 为空时不应注入 OPENAI_MODEL")
	}
	// ANTHROPIC_DEFAULT_* 系列由 Claude Code 自管理，绝不注入。
	for k := range env {
		if strings.Contains(k, "DEFAULT_") {
			t.Fatalf("不应注入 %s", k)
		}
	}
	for _, k := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "OPENAI_BASE_URL", "OPENAI_API_KEY"} {
		if env[k] == "" {
			t.Fatalf("缺少 %s", k)
		}
	}
}

func TestExpandCommand(t *testing.T) {
	got := expandCommand("{ORIGIN}/v1 --key {API_KEY} --model {MODEL}", "m1", "http://127.0.0.1:7860", "k1")
	want := "http://127.0.0.1:7860/v1 --key k1 --model m1"
	if got != want {
		t.Fatalf("expandCommand() = %q, want %q", got, want)
	}
	// model 为空时占位符展开成空串。
	if got := expandCommand("claude --model {MODEL}", "", "http://o", "k"); got != "claude --model " {
		t.Fatalf("model 为空时应展开为空串: %q", got)
	}
	// 同一占位符多次出现全部替换。
	if got := expandCommand("{API_KEY}x{API_KEY}", "m", "o", "kk"); got != "kkxkk" {
		t.Fatalf("应替换全部占位符: %q", got)
	}
}

func TestMergeEnvOverridesParent(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "http://parent:1")
	t.Setenv("AETHER_KEEP", "yes")
	env := mergeEnv(map[string]string{"ANTHROPIC_BASE_URL": "http://child:2"})
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "http://parent:1") {
		t.Fatalf("同名键应被 extra 覆盖: %v", env)
	}
	if !strings.Contains(joined, "ANTHROPIC_BASE_URL=http://child:2") {
		t.Fatalf("缺少覆盖后的值: %v", env)
	}
	if !strings.Contains(joined, "AETHER_KEEP=yes") {
		t.Fatalf("父环境其余键应保留: %v", env)
	}
}

// TestLaunchAndStop 用真实进程验证拉起 -> 列表 -> 停止 -> 移除的全链路。
// 依赖 Windows 的 cmd /c 新控制台与 taskkill，其它平台跳过。
func TestLaunchAndStop(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("真实进程拉起仅实现 Windows，其它平台跳过")
	}
	m := New()
	env := BuildEnv("http://127.0.0.1:7860", "aether-local", "test-model")
	// timeout 是 cmd 内建命令、会实打实阻塞 30 秒、可被 taskkill 干净终止；
	// 尾部的 rem model={MODEL} 顺带验证 Launch 内部确实做了占位符展开。
	inst, err := m.Launch("claude_code", "timeout /t 30 /nobreak & rem model={MODEL}", env, LaunchOptions{})
	if err != nil {
		t.Fatalf("Launch 失败: %v", err)
	}
	if inst.PID <= 0 {
		t.Fatalf("PID 异常: %d", inst.PID)
	}
	if inst.ID <= 0 || inst.ClientID != "claude_code" || inst.Model != "test-model" || inst.StartedAt <= 0 {
		t.Fatalf("实例记录不完整: %+v", inst)
	}
	if !strings.Contains(inst.Command, "model=test-model") || strings.Contains(inst.Command, "{MODEL}") {
		t.Fatalf("占位符未展开: %q", inst.Command)
	}
	got := m.List()
	if len(got) != 1 || got[0].ID != inst.ID {
		t.Fatalf("List() = %+v, 期望恰有一条", got)
	}
	if err := m.Stop(inst.ID); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	if got := m.List(); len(got) != 0 {
		t.Fatalf("Stop 后 List() = %+v, 期望为空", got)
	}
	// 重复停止同一 ID：记录已移除，应返回 ErrNotFound。
	if err := m.Stop(inst.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复 Stop 应返回 ErrNotFound, got %v", err)
	}
}

func TestLaunchEmptyCommand(t *testing.T) {
	m := New()
	if _, err := m.Launch("claude_code", "   ", nil, LaunchOptions{}); err == nil {
		t.Fatalf("空命令应报错")
	}
	if got := m.List(); len(got) != 0 {
		t.Fatalf("失败启动不应留下记录: %+v", got)
	}
}

// TestIsolationEnv 验证各客户端的隔离变量：键名与官方文档一致，值必须
// 指进实例目录内部（客户端点目录下）；gemini_cli 没有官方隔离变量，
// 必须返回空 map（不支持），未知 client_id 与空目录同样返回空 map。
func TestIsolationEnv(t *testing.T) {
	dir := filepath.Join("base", "inst")
	cases := []struct{ id, key, sub string }{
		// 期望值与 isolationDefs 的官方出处一一对应（见 launcher.go 注释）。
		{"claude_code", "CLAUDE_CONFIG_DIR", ".claude"},
		{"codex_cli", "CODEX_HOME", ".codex"},
		{"opencode", "XDG_CONFIG_HOME", ".config"},
	}
	for _, tc := range cases {
		env := IsolationEnv(tc.id, dir)
		if len(env) != 1 {
			t.Fatalf("%s: 期望恰好一个隔离变量, got %v", tc.id, env)
		}
		want := filepath.Join(dir, tc.sub)
		if env[tc.key] != want {
			t.Fatalf("%s: %s = %q, want %q", tc.id, tc.key, env[tc.key], want)
		}
		if !IsolationSupported(tc.id) {
			t.Fatalf("%s: 应判定为支持隔离", tc.id)
		}
		// 隔离的根基：变量值必须落在实例目录内部。
		if !strings.HasPrefix(env[tc.key], dir+string(filepath.Separator)) {
			t.Fatalf("%s: 变量值 %q 未指向实例目录内部", tc.id, env[tc.key])
		}
	}
	// gemini_cli：官方没有可迁移配置目录的环境变量，不支持就是不支持。
	if env := IsolationEnv("gemini_cli", dir); len(env) != 0 {
		t.Fatalf("gemini_cli 未定义隔离变量，应返回空 map: %v", env)
	}
	if IsolationSupported("gemini_cli") {
		t.Fatalf("gemini_cli 不应判定为支持隔离")
	}
	// 未知 client_id 与空目录同样返回空 map。
	for _, tc := range []struct{ id, dir string }{
		{"who_am_i", dir},
		{"claude_code", ""},
		{"claude_code", "   "},
	} {
		if env := IsolationEnv(tc.id, tc.dir); len(env) != 0 {
			t.Fatalf("(%s,%q): 应返回空 map, got %v", tc.id, tc.dir, env)
		}
	}
}

// TestPrepareIsolationSeedsConfig 验证隔离目录创建与配置种子：目录在
// baseDir/<client_id>/<时间戳>-<短随机>/ 下、路径绝对，种子文件
// <实例目录>/.claude/settings.json 的 env 里带网关端点与鉴权占位值。
func TestPrepareIsolationSeedsConfig(t *testing.T) {
	base := t.TempDir()
	dir, seedWarn, err := PrepareIsolation(base, "claude_code", "http://127.0.0.1:7860/", "aether-local", "test-model")
	if err != nil {
		t.Fatalf("PrepareIsolation 失败: %v", err)
	}
	if seedWarn != "" {
		t.Fatalf("种子注入不应告警: %s", seedWarn)
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("实例目录应为绝对路径: %q", dir)
	}
	if !strings.HasPrefix(dir, filepath.Join(base, "claude_code")+string(filepath.Separator)) {
		t.Fatalf("实例目录应位于 baseDir/claude_code/ 下: %q", dir)
	}
	if !regexp.MustCompile(`^\d{8}-\d{6}-[0-9a-f]{6}$`).MatchString(filepath.Base(dir)) {
		t.Fatalf("实例目录名应为 <时间戳>-<短随机>: %q", filepath.Base(dir))
	}
	data, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("种子文件缺失: %v", err)
	}
	var seed struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &seed); err != nil {
		t.Fatalf("种子文件不是合法 JSON: %v", err)
	}
	if seed.Env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:7860" {
		t.Fatalf("种子里 ANTHROPIC_BASE_URL = %q", seed.Env["ANTHROPIC_BASE_URL"])
	}
	if seed.Env["ANTHROPIC_AUTH_TOKEN"] != "aether-local" || seed.Env["ANTHROPIC_MODEL"] != "test-model" {
		t.Fatalf("种子 env 不完整: %v", seed.Env)
	}
}

// TestPrepareIsolationSeedFailureNonBlocking 验证种子注入失败不阻塞：
// 未知 client_id 的 Apply 必然失败，但目录照常创建、只返回告警。
func TestPrepareIsolationSeedFailureNonBlocking(t *testing.T) {
	base := t.TempDir()
	dir, seedWarn, err := PrepareIsolation(base, "no_such_client", "http://127.0.0.1:7860", "k", "")
	if err != nil {
		t.Fatalf("种子失败不应阻塞目录创建: %v", err)
	}
	if seedWarn == "" {
		t.Fatalf("未知 client_id 应产生种子注入告警")
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("实例目录应为绝对路径: %q", dir)
	}
}

// TestCleanupWipesDirAndRemovesRecord 验证 Cleanup 的三种路径：wipe 删
// 目录并移除记录；非隔离实例（Dir 为空）wipe 也不碰文件系统；不存在的
// 实例返回 ErrNotFound。PID 传 0——taskkill 对其必然失败且被 Cleanup
// 忽略（收尾逻辑对"进程已不在"不应敏感），不依赖真实进程。
func TestCleanupWipesDirAndRemovesRecord(t *testing.T) {
	m := New()
	root := t.TempDir()
	sub := filepath.Join(root, "inst-dir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("准备实例目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "state.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("准备实例状态文件失败: %v", err)
	}

	m.mu.Lock()
	m.nextID = 2
	m.running[1] = &Instance{ID: 1, ClientID: "claude_code", PID: 0, Dir: sub}
	m.running[2] = &Instance{ID: 2, ClientID: "claude_code", PID: 0} // 非隔离实例
	m.mu.Unlock()

	dir, err := m.Cleanup(1, true)
	if err != nil || dir != sub {
		t.Fatalf("Cleanup(1, true) = (%q, %v)", dir, err)
	}
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Fatalf("wipe 后实例目录应消失: %v", err)
	}
	if got := m.List(); len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("Cleanup 后应只剩实例 #2: %+v", got)
	}

	// 非隔离实例：Dir 为空，wipe 也不应触碰文件系统，返回空目录串。
	if dir, err := m.Cleanup(2, true); err != nil || dir != "" {
		t.Fatalf("Cleanup(2, true) = (%q, %v)", dir, err)
	}

	// 不存在的实例：ErrNotFound。
	if _, err := m.Cleanup(3, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的实例应返回 ErrNotFound, got %v", err)
	}
}

// waitForFile 轮询等待实例进程写出文件内容。注意重定向在命令执行前
// 就会创建空文件，必须等到内容非空才算就绪（否则读到的是空壳）。
func waitForFile(t *testing.T, path string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(b)) != "" {
			return string(b)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("等待 %s 超时（实例进程未写出环境变量转储）", path)
	return ""
}

// TestLaunchIsolatedEndToEnd 真实进程全链路：隔离目录创建 -> 种子注入 ->
// 隔离变量随环境注入子进程（由 cmd 把 CLAUDE_CONFIG_DIR 回显到文件验证，
// 命令与 client_id 解耦，不必真的安装 claude）-> 清理删目录。仅 Windows。
func TestLaunchIsolatedEndToEnd(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("真实进程拉起仅实现 Windows，其它平台跳过")
	}
	base := t.TempDir()
	dir, seedWarn, err := PrepareIsolation(base, "claude_code", "http://127.0.0.1:7860", "aether-local", "test-model")
	if err != nil {
		t.Fatalf("PrepareIsolation 失败: %v", err)
	}
	if seedWarn != "" {
		t.Fatalf("种子注入不应告警: %s", seedWarn)
	}
	seedPath := filepath.Join(dir, ".claude", "settings.json")
	seedData, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatalf("种子文件缺失: %v", err)
	}
	if !strings.Contains(string(seedData), "ANTHROPIC_BASE_URL") {
		t.Fatalf("种子文件缺少 ANTHROPIC_BASE_URL: %s", seedData)
	}

	m := New()
	// set CLAUDE_CONFIG_DIR 把该变量回显到文件（证明子进程环境里确实有
	// 它、且值正确）；随后 timeout 挂住进程，给测试留出轮询窗口。
	// 注意：重定向目标必须经 env 变量携带引号（AETHER_DUMP 的值含 "），
	// 命令串本身零引号——Go exec 会把命令串里的 " 转义成 \"，cmd /c
	// 解析重定向目标时引号错位直接报"文件名语法不正确"（探针实测）；
	// 变量值则原样进入子进程环境，cmd 展开后引号恰好包住路径，含空格也可靠。
	outFile := filepath.Join(t.TempDir(), "env-dump.txt")
	env := BuildEnv("http://127.0.0.1:7860", "aether-local", "test-model")
	env["AETHER_DUMP"] = `"` + outFile + `"`
	command := `set CLAUDE_CONFIG_DIR>%AETHER_DUMP% & timeout /t 5 /nobreak >nul`
	inst, err := m.Launch("claude_code", command, env, LaunchOptions{IsolateDir: dir})
	if err != nil {
		t.Fatalf("隔离 Launch 失败: %v", err)
	}
	if inst.Dir != dir || inst.SeedError != "" {
		t.Fatalf("实例记录应带隔离目录且无种子告警: %+v", inst)
	}
	if got := m.List(); len(got) != 1 || got[0].Dir != dir {
		t.Fatalf("List() = %+v, 期望恰有一条且带 Dir", got)
	}

	want := IsolationEnv("claude_code", dir)["CLAUDE_CONFIG_DIR"]
	dump := strings.TrimSpace(waitForFile(t, outFile, 15*time.Second))
	if dump != "CLAUDE_CONFIG_DIR="+want {
		t.Fatalf("子进程环境里 CLAUDE_CONFIG_DIR = %q, want %q", dump, "CLAUDE_CONFIG_DIR="+want)
	}

	// 清理：结束实例进程树 + 删除隔离目录 + 移除记录。
	gotDir, err := m.Cleanup(inst.ID, true)
	if err != nil || gotDir != dir {
		t.Fatalf("Cleanup = (%q, %v)", gotDir, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("清理后隔离目录应消失: %v", err)
	}
	if got := m.List(); len(got) != 0 {
		t.Fatalf("清理后 List() = %+v, 期望为空", got)
	}
	if _, err := m.Cleanup(inst.ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复 Cleanup 应返回 ErrNotFound, got %v", err)
	}
}
