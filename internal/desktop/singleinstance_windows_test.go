//go:build windows

package desktop

import (
	"fmt"
	"os"
	"testing"
)

func TestSingleInstanceMutex(t *testing.T) {
	name := fmt.Sprintf("Local\\AetherCockpitTest-%d", os.Getpid())
	if !acquireSingleInstance(name) {
		t.Fatal("首个实例应获取成功")
	}
	if acquireSingleInstance(name) {
		t.Fatal("同进程第二次获取应被判定为已在运行")
	}
}
