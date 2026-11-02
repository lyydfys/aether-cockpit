//go:build darwin

package launcher

import (
	"os"
	"testing"
)

func TestProcessAliveDarwin(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("当前进程应被判定为存活")
	}
}
