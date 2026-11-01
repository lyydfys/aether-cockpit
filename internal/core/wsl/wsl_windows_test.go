//go:build windows

package wsl

import (
	"os"
	"strings"
	"testing"
	"unicode/utf16"
)

func utf16LE(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 0, len(u)*2)
	for _, v := range u {
		b = append(b, byte(v), byte(v>>8))
	}
	return b
}

func TestWSLDistrosSkipsDockerAndBlanks(t *testing.T) {
	raw := utf16LE("Ubuntu-22.04\r\ndocker-desktop\r\n\r\n  \r\nDebian\r\n")
	got := wslDistros(raw)
	if len(got) != 2 || got[0] != "Ubuntu-22.04" || got[1] != "Debian" {
		t.Fatalf("wslDistros = %#v，want [Ubuntu-22.04 Debian]", got)
	}
}

func TestWSLDistrosEmptyOutput(t *testing.T) {
	if got := wslDistros(nil); len(got) != 0 {
		t.Fatalf("wslDistros(nil) = %#v，want 空", got)
	}
}

// 残缺结果只短暂缓存：恢复环境后立刻再调，仍应命中那份残缺缓存（否则
// 每次请求都要重新付冷启动超时）。
func TestPartialResultIsCachedBriefly(t *testing.T) {
	orig := os.Getenv("PATH")
	t.Cleanup(func() {
		os.Setenv("PATH", orig)
		Invalidate()
	})
	t.Setenv("AETHER_WSL_HOMES", "")
	os.Setenv("PATH", t.TempDir())
	Invalidate()

	_ = Homes()
	first := LastProblem()
	if first == "" {
		t.Fatalf("PATH 坏掉时应记录失败原因")
	}

	os.Setenv("PATH", orig)
	if got := Homes(); len(got) != 0 {
		t.Fatalf("1 分钟内应复用残缺缓存（空），实际 %#v", got)
	}
	if LastProblem() != first {
		t.Fatalf("原因应来自残缺缓存：got %q want %q", LastProblem(), first)
	}
}

// 找不到 wsl.exe 时要给出可读原因，而不是静默返回空列表——否则界面只能
// 显示"不可用"，用户不知道是没装 WSL 还是检测失败。
func TestDetectFailureExplainsItself(t *testing.T) {
	orig := os.Getenv("PATH")
	t.Cleanup(func() {
		os.Setenv("PATH", orig)
		Invalidate()
	})
	t.Setenv("AETHER_WSL_HOMES", "")
	os.Setenv("PATH", t.TempDir())
	Invalidate()

	if got := Homes(); len(got) != 0 {
		t.Fatalf("Homes() = %#v，want 空", got)
	}
	if p := LastProblem(); !strings.Contains(p, "wsl.exe") {
		t.Fatalf("LastProblem() = %q，want 含 wsl.exe 的失败原因", p)
	}
}
