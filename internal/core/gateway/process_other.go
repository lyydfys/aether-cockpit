//go:build !windows && !darwin

package gateway

import (
	"os/exec"
	"strings"
)

// processRunning 用 pgrep 判断进程是否存在（Linux 及其它 Unix）。
func processRunning(image string) bool {
	name := strings.TrimSuffix(strings.TrimSpace(image), ".exe")
	if name == "" {
		return false
	}
	out, err := exec.Command("pgrep", "-x", name).Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}
