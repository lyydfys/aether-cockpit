//go:build windows

package desktop

import (
	"log"
	"syscall"
	"unsafe"
)

// Windows 单实例：用命名互斥体保证同一用户只跑一个桌面端。
// 第二个实例如果共享 data/，两个网关会同时写 accounts.json，必须挡住。

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	user32           = syscall.NewLazyDLL("user32.dll")
	procCreateMutexW = kernel32.NewProc("CreateMutexW")
	procMessageBoxW  = user32.NewProc("MessageBoxW")

	instanceMu uintptr
)

// acquireSingleInstance 返回 false 表示已有实例在运行。
func acquireSingleInstance(name string) bool {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return true
	}
	h, _, lastErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(p)))
	if h == 0 {
		log.Printf("[WARN] 创建单实例互斥体失败，继续启动: %v", lastErr)
		return true
	}
	const errAlreadyExists = syscall.Errno(183)
	if errno, ok := lastErr.(syscall.Errno); ok && errno == errAlreadyExists {
		return false
	}
	instanceMu = h
	return true
}

// showAlreadyRunning 用系统弹窗告诉用户已经有一个实例。
func showAlreadyRunning(appName string) {
	title, _ := syscall.UTF16PtrFromString(appName)
	text, _ := syscall.UTF16PtrFromString("Aether Cockpit 已在运行。\n请在任务栏托盘打开已有窗口，或先退出再启动。")
	const mbIconInformation = 0x40
	_, _, _ = procMessageBoxW.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), mbIconInformation)
}
