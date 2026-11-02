//go:build darwin || linux

package desktop

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
)

var instanceLock *os.File

// acquireSingleInstance 用临时目录里的 flock 做进程级单实例。
// 文件锁随进程退出自动释放，不会留下 Windows 注册表/句柄那类残留。
func acquireSingleInstance(name string) bool {
	path := filepath.Join(os.TempDir(), "aether-cockpit.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		// 锁文件不可写时宁可放行，也不能让用户完全打不开应用。
		log.Printf("[WARN] 单实例锁不可用，继续启动: %v", err)
		return true
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return false
	}
	instanceLock = f
	return true
}

func showAlreadyRunning(appName string) {
	log.Printf("[INFO] %s 已在运行", appName)
	if runtime.GOOS != "darwin" {
		return
	}
	// 非阻塞提示；用户仍可在 Dock/菜单栏里找到已运行的实例。
	script := fmt.Sprintf(`display notification "%s 已在运行" with title "%s"`, appName, appName)
	_ = exec.Command("osascript", "-e", script).Start()
}
