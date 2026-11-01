package apppaths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDataFollowsEnv(t *testing.T) {
	t.Setenv(EnvDataDir, filepath.Join(t.TempDir(), "aether-data"))
	if got, want := Data("logs", "aether.log"), filepath.Join(Base(), "logs", "aether.log"); got != want {
		t.Fatalf("Data=%q want %q", got, want)
	}

	t.Setenv(EnvDataDir, "")
	if got := Data("accounts.json"); got != filepath.Join("data", "accounts.json") {
		t.Fatalf("默认路径=%q", got)
	}
}

func TestDownloadDirFollowsEnv(t *testing.T) {
	t.Setenv("AETHER_DOWNLOAD_DIR", filepath.Join(t.TempDir(), "dl"))
	if got := DownloadDir(); got != filepath.Join(Base(), "dl") {
		// Base() 是 AETHER_DATA_DIR 或 data；这里只断言等于环境变量本身
		if got != os.Getenv("AETHER_DOWNLOAD_DIR") {
			t.Fatalf("DownloadDir=%q", got)
		}
	}
	t.Setenv("AETHER_DOWNLOAD_DIR", "")
	if got := DownloadDir(); got == "" {
		t.Fatalf("默认下载目录为空")
	}
}
