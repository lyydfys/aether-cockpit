//go:build windows

package wsl

import (
	"os"
	"testing"
	"time"
)

// 有缓存来源（AETHER_WSL_HOMES）时 TryHomes 立即 ready，不走后台检测。
// 无缓存分支会真的跑 wsl.exe，本机按用户要求不碰 WSL，故不在测试里覆盖。
func TestTryHomesReadyWithOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AETHER_WSL_HOMES", home)
	t.Setenv("AETHER_WSL_DISTRO", "Ubuntu-22.04")

	homes, ready := TryHomes()
	if !ready {
		t.Fatal("有覆盖来源时应 ready=true")
	}
	if len(homes) != 1 || homes[0].Path != home {
		t.Fatalf("homes = %+v", homes)
	}
	if os.Getenv("AETHER_WSL_HOMES") == "" {
		t.Fatal("环境变量应仍在")
	}
}

// 没有缓存、检测又很慢（这里用空 PATH 让 wsl.exe 立刻失败）时，TryHomes 必须
// **立即**返回 ready=false 并去后台补——不能阻塞调用方。
func TestTryHomesDoesNotBlock(t *testing.T) {
	orig := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", orig); Invalidate() })
	t.Setenv("AETHER_WSL_HOMES", "")
	os.Setenv("PATH", t.TempDir())
	Invalidate()

	start := time.Now()
	_, ready := TryHomes()
	if ready {
		t.Fatal("没有缓存时首次应 ready=false")
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("TryHomes 不应阻塞调用方：%v", d)
	}
}
