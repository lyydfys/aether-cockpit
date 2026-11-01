//go:build windows

package clients

import (
	"os"
	"testing"
)

// TestLiveZedCredential 真实写入/读回/删除 Windows 凭据（目标 URL 用不可达的
// 本地地址，避免影响真实 Zed 配置）。默认跳过；ZED_LIVE=1 才跑。
func TestLiveZedCredential(t *testing.T) {
	if os.Getenv("ZED_LIVE") != "1" {
		t.Skip("设置 ZED_LIVE=1 才跑真实凭据管理器读写")
	}
	url := "http://127.0.0.1:1/v1"
	key := "sk-live-zed-test"
	_ = deleteZedCredential(url) // 清掉可能残留
	if err := writeZedCredential(url, key); err != nil {
		t.Fatalf("write: %v", err)
	}
	back, err := readZedCredential(url)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if back != key {
		t.Fatalf("read back = %q", back)
	}
	if err := deleteZedCredential(url); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := readZedCredential(url); err == nil {
		t.Fatal("删除后仍能读到")
	}
	t.Logf("credential target zed:url=%s round-trip ok", url)
}
