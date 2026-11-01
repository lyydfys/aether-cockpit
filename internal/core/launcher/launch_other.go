//go:build !windows

package launcher

import "errors"

// startDetached 非 Windows 占位实现：v1 的多开依赖 cmd /c 新控制台窗口
// 与 taskkill，仅支持 Windows；其它平台返回明确错误，保证跨平台编译。
func startDetached(command string, env []string) (int, error) {
	return 0, errors.New("应用多开目前仅支持 Windows（macOS / Linux 支持在规划中）")
}

// startGuiDetached 非 Windows 占位实现。
func startGuiDetached(exe string, args []string, env []string) (int, error) {
	return 0, errors.New("桌面应用多开目前仅支持 Windows")
}

// killTree 非 Windows 占位实现，语义同 startDetached。
func killTree(pid int) error {
	return errors.New("应用多开目前仅支持 Windows（macOS / Linux 支持在规划中）")
}
