//go:build windows

package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeMSIXTargetFrom(t *testing.T) {
	install := t.TempDir()
	if err := os.MkdirAll(filepath.Join(install, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(install, "app", "Claude.exe")
	if err := os.WriteFile(exe, []byte("stub"), 0o600); err != nil {
		t.Fatal(err)
	}
	target, gotExe := claudeMSIXTargetFrom("Claude_pzs8sxrjxfjjc!Claude", install)
	if target != `shell:AppsFolder\Claude_pzs8sxrjxfjjc!Claude` {
		t.Fatalf("target = %q", target)
	}
	if gotExe != exe {
		t.Fatalf("exe = %q, want %q", gotExe, exe)
	}
	if target, _ := claudeMSIXTargetFrom("", install); target != "" {
		t.Fatal("空包族不应生成 target")
	}
	if target, _ := claudeMSIXTargetFrom("Claude_pzs8sxrjxfjjc", install); target != "" {
		t.Fatal("没有 Application Id 的 AUMID 不应生成 target")
	}
}

// TestClaudeMSIXTargetRealPackage 只在本机确实安装了 Microsoft Store 版
// Claude 时验证真实清单解析；CI/未安装环境跳过，不作为必过项。
func TestClaudeMSIXTargetRealPackage(t *testing.T) {
	target, exe := claudeMSIXTarget()
	if target == "" {
		t.Skip("本机没有 Microsoft Store 版 Claude")
	}
	if !strings.HasPrefix(target, `shell:AppsFolder\Claude_`) || !strings.Contains(target, "!") {
		t.Fatalf("真实 MSIX target 异常: %q", target)
	}
	if exe != "" && !strings.HasSuffix(strings.ToLower(exe), "claude.exe") {
		t.Fatalf("真实 MSIX exe 异常: %q", exe)
	}
}
