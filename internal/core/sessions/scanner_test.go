package sessions

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestScanCodexFollowsSymlink 覆盖统一会话历史把 ~/.codex/sessions 软链到
// 其它盘的情况：WalkDir 默认不跟随符号链接，必须解析根目录才扫得到。
func TestScanCodexFollowsSymlink(t *testing.T) {
	realDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(realDir, "2026", "10", "31"), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"event_msg","payload":{"type":"user_message","message":"linked session"}}` + "\n"
	if err := os.WriteFile(filepath.Join(realDir, "2026", "10", "31", "rollout-1.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, filepath.Join(home, ".codex", "sessions")); err != nil {
		t.Skipf("当前环境不能创建符号链接: %v", err)
	}
	list := scanCodex(sessionRoot{Home: home})
	if len(list) != 1 || list[0].Title != "linked session" {
		t.Fatalf("scanCodex = %+v", list)
	}
}

// TestScanCodexFollowsJunction 覆盖 Windows 目录联接：~/.codex/sessions
// 常被指到其它盘，WalkDir 不跟随联通点会静默漏掉全部 Codex 会话。
func TestScanCodexFollowsJunction(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("目录联接是 Windows 专有场景")
	}
	realDir := t.TempDir()
	dayDir := filepath.Join(realDir, "2026", "10", "31")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"event_msg","payload":{"type":"user_message","message":"junction session"}}`
	if err := os.WriteFile(filepath.Join(dayDir, "rollout-1.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".codex", "sessions")
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, realDir).CombinedOutput()
	if err != nil {
		t.Skipf("当前环境不能创建目录联接: %v %s", err, out)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	list := scanCodex(sessionRoot{Home: home})
	if len(list) != 1 || list[0].Title != "junction session" {
		t.Fatalf("scanCodex = %+v", list)
	}
}

// TestScanRealIncludesCodex 是回归护栏：Codex 目录里有 jsonl 时，
// Scan 必须至少返回一条 Codex 会话。跳过没装 Codex/没有会话的机器。
func TestScanRealIncludesCodex(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("无法定位用户目录")
	}
	root := filepath.Join(home, ".codex", "sessions")
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		t.Skip("本机没有 ~/.codex/sessions")
	}
	files := 0
	walkSessionFiles(root, 0, func(p string, info os.FileInfo) {
		if strings.HasSuffix(strings.ToLower(p), ".jsonl") {
			files++
		}
	})
	if files == 0 {
		t.Skip("Codex 目录没有 jsonl")
	}
	count := 0
	for _, s := range Scan() {
		if s.Agent == "codex" {
			count++
		}
	}
	if count == 0 {
		t.Fatalf("Codex 目录有 %d 个 jsonl，但 Scan 没有返回任何 Codex 会话", files)
	}
	t.Logf("Codex 文件 %d 个，扫描到 %d 个会话", files, count)
}

// TestScanReal 用本机真实目录验证扫描不会崩、能解析出标题。
// 没有会话文件的机器上跳过。
func TestScanReal(t *testing.T) {
	list := Scan()
	if len(list) == 0 {
		t.Skip("本机没有可扫描的会话文件")
	}
	withTitle := 0
	for _, s := range list {
		if s.Title != "" && s.Title != "(无标题会话)" {
			withTitle++
		}
		if s.Agent == "" {
			t.Error("agent 不应为空")
		}
		if s.Last == 0 {
			t.Error("last 不应为 0")
		}
	}
	t.Logf("扫描到 %d 个会话，其中 %d 个解析出标题", len(list), withTitle)
	if withTitle == 0 {
		t.Error("一个标题都没解析出来，解析逻辑可能有问题")
	}
}

func TestClip(t *testing.T) {
	long := ""
	for i := 0; i < 200; i++ {
		long += "字"
	}
	got := clip(long)
	if len([]rune(got)) > 82 {
		t.Errorf("clip 未截断: %d 字符", len([]rune(got)))
	}
	if got[len(got)-3:] != "…" {
		t.Errorf("应以省略号结尾: %q", got[len(got)-3:])
	}
}
