//go:build !windows

package wsl

import "fmt"

// Homes 在非 Windows 平台恒为空：WSL 是 Windows 宿主上的东西。
func Homes() []Home { return nil }

// LastProblem 在非 Windows 平台没有检测可言。
func LastProblem() string { return "" }

// TryHomes 在非 Windows 平台没有 WSL。
func TryHomes() ([]Home, bool) { return nil, true }

func invalidateDetection() {}

// HostIP 在非 Windows 平台不可用。
func HostIP() (string, error) {
	return "", fmt.Errorf("WSL 只在 Windows 上可用")
}

// EnsureRunning 在非 Windows 平台不可用。
func EnsureRunning(distro string) error {
	return fmt.Errorf("WSL 只在 Windows 上可用")
}
