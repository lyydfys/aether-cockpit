// Package apppaths 统一 data 目录解析：环境变量 AETHER_DATA_DIR 可整体搬迁，
// 未设置时保持便携默认（当前目录的 data/）。
package apppaths

import (
	"os"
	"path/filepath"
	"strings"
)

// EnvDataDir 是数据目录覆盖环境变量。
const EnvDataDir = "AETHER_DATA_DIR"

// Base 返回数据根目录。
func Base() string {
	if v := strings.TrimSpace(os.Getenv(EnvDataDir)); v != "" {
		return v
	}
	return "data"
}

// Data 返回数据目录下的相对路径。
func Data(rel ...string) string {
	return filepath.Join(append([]string{Base()}, rel...)...)
}

// DownloadDir 返回更新包的落盘目录：AETHER_DOWNLOAD_DIR 优先，
// 其次用户 Downloads，最后 data/downloads。
func DownloadDir() string {
	if v := strings.TrimSpace(os.Getenv("AETHER_DOWNLOAD_DIR")); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "Downloads")
	}
	return Data("downloads")
}
