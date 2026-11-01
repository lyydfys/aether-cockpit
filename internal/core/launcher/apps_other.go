//go:build !windows

package launcher

import "errors"

// DesktopApps 非 Windows 暂无桌面端一键启动。
func DesktopApps() []DesktopApp { return nil }

// LaunchDesktopApp 非 Windows 占位实现。
func LaunchDesktopApp(id string) error {
	return errors.New("桌面应用一键启动目前仅支持 Windows")
}
