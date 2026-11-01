package sessions

import (
	"os"
	"path/filepath"
	"testing"
)

// 关掉 WSL 后 scanRoots 不再枚举 WSL 家目录——否则 /api/sessions 仍会跑
// wsl.exe 与 UNC stat，"关闭开关"就没有意义。
func TestScanRootsRespectsWSLSwitch(t *testing.T) {
	home := filepath.Join(t.TempDir(), "wslhome")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AETHER_WSL_HOMES", home)
	t.Cleanup(func() { SetWSLEnabled(true) })

	SetWSLEnabled(true)
	if n := len(scanRoots()); n != 2 {
		t.Fatalf("开启时应含本机 + WSL 两个根：%d", n)
	}
	SetWSLEnabled(false)
	roots := scanRoots()
	if len(roots) != 1 {
		t.Fatalf("关闭时只应有本机根：%+v", roots)
	}
	if roots[0].WSL != "" {
		t.Fatalf("关闭时第一个根不应是 WSL：%+v", roots[0])
	}
}
