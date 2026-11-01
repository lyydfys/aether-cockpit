//go:build windows

package gateway

import "syscall"

// SetThreadExecutionState 让本进程在运行期间阻止系统进入睡眠；
// 关闭时清除 ES_SYSTEM_REQUIRED（仍保持 ES_CONTINUOUS，交给系统正常策略）。
const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

var (
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procSetThreadExecutionState = kernel32.NewProc("SetThreadExecutionState")
)

func setKeepAwake(on bool) error {
	flags := uintptr(esContinuous)
	if on {
		flags |= esSystemRequired
	}
	r, _, err := procSetThreadExecutionState.Call(flags)
	if r == 0 {
		return err
	}
	return nil
}
