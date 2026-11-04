//go:build windows

package transport

import (
	"os/exec"
	"syscall"
)

// newRegCmd 构造 `reg query` 命令，并抑制控制台窗口弹出。
//
// 为什么不用 -Command + shell：网关是 GUI 进程，弹一下黑框会很明显；
// reg.exe 本身不需要 shell 中转。
func newRegCmd(key, name string) *exec.Cmd {
	cmd := exec.Command("reg", "query", `HKCU\`+key, "/v", name)
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// CREATE_NO_WINDOW：避免每次读注册表闪一下控制台窗口。
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags = 0x08000000
	return cmd
}
