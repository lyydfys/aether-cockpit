//go:build !windows

package executil

import "os/exec"

// Hide 在非 Windows 平台是空操作。
func Hide(cmd *exec.Cmd) {}
