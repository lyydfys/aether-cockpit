package desktop

import (
	"os/exec"
	"path/filepath"
	"runtime"

	"aether-cockpit/internal/core/logging"
)

// openLogDir 在系统文件管理器里打开日志目录。
//
// 托盘菜单原来叫「打开日志目录」，动作却是用浏览器打开管理界面（文案与动作
// 不符）。这里按文案做真事；管理界面另设一项，两个入口各归各位。
//
// 与 gateway.revealDir 同构，但那边未导出且只服务 HTTP 接口，这里不为了
// 复用二十行而把内部函数提升成跨包 API。
func openLogDir() error {
	return openDir(filepath.Dir(logging.DefaultPath()))
}

func openDir(dir string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// 不等待：explorer.exe 的退出码不表示成功与否。
	go func() { _ = cmd.Wait() }()
	return nil
}
