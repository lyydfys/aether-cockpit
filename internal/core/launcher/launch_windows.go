//go:build windows

package launcher

import (
	"aether-cockpit/internal/core/executil"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// 进程创建标志（取值与 golang.org/x/sys/windows 同名常量一致；go.mod 里
// x/sys 目前只是 indirect 依赖，为两个常量把它转成正依赖不值得，直接用
// syscall 十六进制字面量并注释含义）：
//
//   - 0x00000010 CREATE_NEW_CONSOLE：为子进程分配全新的控制台窗口——
//     多开的核心体验，每个实例一个独立终端；
//   - 0x00000200 CREATE_NEW_PROCESS_GROUP：子进程成为新进程组的根，
//     便于 taskkill /T 把整组（cmd 外壳 + 客户端本体）一次带走。
const (
	createNewConsole      = 0x00000010
	createNewProcessGroup = 0x00000200
)

// startDetached 在新控制台窗口中启动 command，进程脱离父进程：
// 网关退出不影响实例。返回子进程 PID（cmd.exe 的 PID，Stop 时整组终止）。
func startDetached(command string, env []string) (int, error) {
	cmd := exec.Command("cmd", "/c", command)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewConsole | createNewProcessGroup,
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("启动失败: %w", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release() // 不 wait：实例生命周期独立于网关进程
	return pid, nil
}

// startGuiDetached 直接启动 GUI 程序（不分配控制台、不等待），返回
// 应用主进程 PID。多开桌面端用它带 --user-data-dir 启动独立 profile。
func startGuiDetached(exe string, args []string, env []string) (int, error) {
	cmd := exec.Command(exe, args...)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("启动失败: %w", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}

// killTree 终止整棵进程树（cmd 外壳 + 真正的客户端进程）。
//
// taskkill /T 只要有一个子进程已经自己退出（或拒绝访问），整体就返回非零，
// 但目标进程往往已经没了。因此以"进程是否仍存在"为最终判据：
// 已经不在 = 目的达到，不把 exit status 128 当成失败抛给用户。
func killTree(pid int) error {
	tk := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
	executil.Hide(tk)
	out, err := tk.CombinedOutput()
	if err == nil {
		return nil
	}
	if !processAlive(pid) {
		return nil
	}
	// taskkill /T 对 Electron 的多进程树偶尔先返回非零；给主进程一点
	// 退出时间再判定，避免“已经关掉了却提示停止失败”。
	for i := 0; i < 5; i++ {
		time.Sleep(200 * time.Millisecond)
		if !processAlive(pid) {
			return nil
		}
	}
	return fmt.Errorf("结束进程 %d 失败: %v（%s）", pid, err, strings.TrimSpace(string(out)))
}

// processAlive 用 tasklist 判断 PID 是否仍然存在。
// 不用 FindProcess：Windows 上 taskkill 之外的 signal 0 语义不可靠。
func processAlive(pid int) bool {
	tl := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH")
	executil.Hide(tl)
	out, err := tl.Output()
	if err != nil {
		// 查不出来时按"还活着"处理，避免把没杀掉的进程当成功。
		return true
	}
	return strings.Contains(string(out), strconv.Itoa(pid))
}
