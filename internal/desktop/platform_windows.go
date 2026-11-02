//go:build windows

package desktop

// Windows 保持便携版行为：数据目录由 AETHER_DATA_DIR 或进程工作目录决定。
func preparePlatform() {}
