//go:build !windows

package launcher

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenTerminal 在一个新的终端窗口里于 dir 执行 command。
//
// 非 Windows 走各平台自带的终端：macOS 用 Terminal.app 的 do script，
// Linux 依次尝试常见的终端模拟器。都不可用时返回错误，让上层提示用户
// 手动执行命令（比静默什么都不做要好）。
func OpenTerminal(dir, command string) error {
	if command == "" {
		return fmt.Errorf("命令为空")
	}
	script := command
	if dir != "" {
		script = fmt.Sprintf("cd %q && %s", dir, command)
	}
	switch runtime.GOOS {
	case "darwin":
		esc := escapeAppleScript(script)
		return exec.Command("osascript", "-e",
			fmt.Sprintf(`tell application "Terminal" to do script "%s"`, esc),
			"-e", `tell application "Terminal" to activate`).Start()
	default:
		for _, term := range [][]string{
			{"x-terminal-emulator", "-e", "bash", "-lc", script},
			{"gnome-terminal", "--", "bash", "-lc", script},
			{"konsole", "-e", "bash", "-lc", script},
			{"xterm", "-e", "bash", "-lc", script},
		} {
			if _, err := exec.LookPath(term[0]); err != nil {
				continue
			}
			return exec.Command(term[0], term[1:]...).Start()
		}
		return fmt.Errorf("没有找到可用的终端模拟器")
	}
}

// escapeAppleScript 转义 AppleScript 字符串里的反斜杠与双引号。
func escapeAppleScript(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\\' || r == '"' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}
