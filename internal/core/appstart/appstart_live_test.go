//go:build windows

package appstart

import (
	"os"
	"strings"
	"testing"
)

// 真实注册表往返：默认跳过，设置 AETHER_LIVE_AUTOSTART=1 才写 HKCU\Run。
// 只在测试进程存活期间写真实值名，defer 保证清理。
func TestLiveAutoStart(t *testing.T) {
	if os.Getenv("AETHER_LIVE_AUTOSTART") != "1" {
		t.Skip("设置 AETHER_LIVE_AUTOSTART=1 运行真实注册表测试")
	}
	_ = Set(false)
	t.Cleanup(func() { _ = Set(false) })

	if err := Set(true); err != nil {
		t.Fatalf("Set(true): %v", err)
	}
	st := Get()
	if !st.Supported || !st.Enabled || !strings.Contains(strings.ToLower(st.Command), ".exe") {
		t.Fatalf("开启后状态不符: %+v", st)
	}
	t.Logf("live autostart command=%s", st.Command)
	if err := Set(false); err != nil {
		t.Fatalf("Set(false): %v", err)
	}
	if Get().Enabled {
		t.Fatal("关闭后仍启用")
	}
}
