//go:build windows

package executil

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestHideSetsNoWindowFlags(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "exit 0")
	Hide(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr 未设置")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("HideWindow 未设置")
	}
	if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("CREATE_NO_WINDOW 未设置: %#x", cmd.SysProcAttr.CreationFlags)
	}
	if cmd.SysProcAttr.CreationFlags&syscall.CREATE_NEW_PROCESS_GROUP != 0 {
		t.Fatal("不应额外创建进程组")
	}
}
