//go:build darwin

package launcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// startDetached 在 Terminal.app 新窗口里启动 command。
//
// macOS 没有 Windows 那种“新控制台返回 PID”的等价物：Terminal 的进程
// 由 LaunchServices 托管。这里让 shell 先把 $$ 写进临时文件再 exec，
// 从而拿到真正运行客户端命令的 PID，后续 Stop 才能按进程组终止。
func startDetached(command string, env []string) (int, error) {
	if strings.TrimSpace(command) == "" {
		return 0, errors.New("启动命令为空")
	}
	dir, err := os.MkdirTemp("", "aether-launch-*")
	if err != nil {
		return 0, err
	}
	script := filepath.Join(dir, "run.command")
	pidFile := filepath.Join(dir, "pid")
	var b strings.Builder
	b.WriteString("#!/bin/zsh\n")
	for _, kv := range env {
		key, value, ok := strings.Cut(kv, "=")
		if !ok || !validEnvKey(key) {
			continue
		}
		b.WriteString("export ")
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(shellQuote(value))
		b.WriteByte('\n')
	}
	b.WriteString("echo $$ > ")
	b.WriteString(shellQuote(pidFile))
	b.WriteByte('\n')
	b.WriteString("exec ")
	b.WriteString(command)
	b.WriteByte('\n')
	if err := os.WriteFile(script, []byte(b.String()), 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return 0, err
	}
	open := exec.Command("open", "-a", "Terminal", script)
	if err := open.Run(); err != nil {
		_ = os.RemoveAll(dir)
		return 0, fmt.Errorf("打开 Terminal 失败: %w", err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(pidFile); err == nil {
			pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
			if pid > 0 {
				_ = os.RemoveAll(dir)
				return pid, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = os.RemoveAll(dir)
	return 0, errors.New("Terminal 没有回报客户端进程 PID")
}

func validEnvKey(key string) bool {
	if key == "" || !(key[0] == '_' || unicode.IsLetter(rune(key[0]))) {
		return false
	}
	for _, r := range key[1:] {
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// startGuiDetached 直接启动 .app/Contents/MacOS 主程序，带独立进程组。
func startGuiDetached(exe string, args []string, env []string) (int, error) {
	if strings.TrimSpace(exe) == "" {
		return 0, errors.New("没有找到桌面端主程序")
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("启动失败: %w", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}

func killTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	// 先按进程组发，覆盖 shell + 客户端；再单独补一次主进程。
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	_ = syscall.Kill(pid, syscall.SIGTERM)
	for i := 0; i < 10; i++ {
		if !processAlive(pid) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
	if processAlive(pid) {
		return fmt.Errorf("结束进程 %d 失败", pid)
	}
	return nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}
