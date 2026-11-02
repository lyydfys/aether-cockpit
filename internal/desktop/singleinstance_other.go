//go:build !windows && !darwin && !linux

package desktop

// 其它平台暂不做单实例限制。
func acquireSingleInstance(name string) bool { return true }

func showAlreadyRunning(appName string) {}
