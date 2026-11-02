//go:build windows

package gateway

import (
	"os/exec"
	"strings"

	"aether-cockpit/internal/core/executil"
)

// processRunning 用 tasklist 判断某个应用是否已经在运行。
func processRunning(image string) bool {
	cmd := exec.Command("tasklist", "/FI", "IMAGENAME eq "+image, "/NH")
	executil.Hide(cmd)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), strings.ToLower(image))
}
