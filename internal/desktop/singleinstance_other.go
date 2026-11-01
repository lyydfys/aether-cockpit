//go:build !windows

package desktop

// 非 Windows 平台暂不做单实例限制（桌面端目前只发布 Windows 产物）。
func acquireSingleInstance(name string) bool { return true }

func showAlreadyRunning(appName string) {}
