//go:build windows

package appstart

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// 测试使用独立的 Run 值名，绝不触碰真实的 AetherCockpit 自启项。
func testValueName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("AetherCockpitTest-%d", os.Getpid())
}

func TestRegistryRoundTrip(t *testing.T) {
	name := testValueName(t)
	t.Cleanup(func() { _ = deleteValue(name) })

	if v, err := getValue(name); err != nil || v != "" {
		t.Fatalf("初始应为空: %q %v", v, err)
	}
	if err := setValue(name, `"C:\Program Files\Aether Cockpit\AetherCockpit.exe"`); err != nil {
		t.Fatalf("setValue: %v", err)
	}
	v, err := getValue(name)
	if err != nil || !strings.Contains(v, "AetherCockpit.exe") {
		t.Fatalf("读回不符: %q %v", v, err)
	}
	if err := deleteValue(name); err != nil {
		t.Fatalf("deleteValue: %v", err)
	}
	if v, _ := getValue(name); v != "" {
		t.Fatalf("删除后仍存在: %q", v)
	}
	if err := deleteValue(name); err != nil {
		t.Fatalf("重复删除应幂等: %v", err)
	}
}

func TestCurrentCommandHasExe(t *testing.T) {
	cmd, err := CurrentCommand()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cmd, `"`) || !strings.HasSuffix(cmd, `"`) || !strings.Contains(strings.ToLower(cmd), ".exe") {
		t.Fatalf("启动命令格式不符: %q", cmd)
	}
}

func TestSameCommand(t *testing.T) {
	a := `"C:\A\AetherCockpit.exe"`
	if !sameCommand(a, `c:\a\aethercockpit.exe`) {
		t.Fatal("大小写/引号差异应视为同一命令")
	}
	if sameCommand(a, `"C:\B\AetherCockpit.exe"`) {
		t.Fatal("不同路径不应视为相同")
	}
}
