//go:build windows

package launcher

import "fmt"

// OpenTerminal 在一个新的终端窗口里于 dir 执行 command。
//
// 为什么单独抽出来：会话页的核心闭环是"看完 → 接着干"。只给一个可复制的
// 命令字符串等于把用户推回手动粘贴，而 GUI 进程本身没有可用的控制台，
// 必须显式分配一个新控制台窗口（见 startDetached 的 CREATE_NEW_CONSOLE）。
func OpenTerminal(dir, command string) error {
	if command == "" {
		return fmt.Errorf("命令为空")
	}
	line := command
	if dir != "" {
		// /d 让 cd 也能跨盘符切换；目录名用引号包住，含空格也不会断。
		line = fmt.Sprintf("cd /d %q && %s", dir, command)
	}
	// 命令结束后保留窗口：agent 需要继续交互。
	line += " & cmd /k"
	if _, err := startDetached(line, nil); err != nil {
		return err
	}
	return nil
}
