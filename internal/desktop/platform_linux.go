//go:build linux

package desktop

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

func preparePlatform() {
	if strings.TrimSpace(os.Getenv("AETHER_DATA_DIR")) != "" {
		return
	}
	base := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if base == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			base = filepath.Join(home, ".local", "share")
		}
	}
	if base == "" {
		return
	}
	dir := filepath.Join(base, "aether-cockpit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[WARN] 创建 Linux 数据目录失败: %v", err)
		return
	}
	_ = os.Setenv("AETHER_DATA_DIR", dir)
}
