//go:build windows

package executil

import (
	"os/exec"
	"syscall"
)

// createNoWindow 与 Windows CREATE_NO_WINDOW 一致：子进程不分配控制台。
// 对 git / node / npx / powershell 这类后台工具必须同时设 HideWindow，
// 否则从 GUI 进程启动时会闪出一个终端窗口。
const createNoWindow = 0x08000000

// Hide 让后台子进程完全无窗口。
func Hide(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
