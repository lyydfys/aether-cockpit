//go:build darwin

package gateway

import (
	"os/exec"
	"strings"
)

// processRunning 用 pgrep 判断桌面端是否在运行。调用方传的是 Windows
// 习惯的 "claude.exe"，macOS 进程名没有 .exe，先剥掉再按常见大小写尝试。
func processRunning(image string) bool {
	name := strings.TrimSuffix(strings.TrimSpace(image), ".exe")
	if name == "" {
		return false
	}
	candidates := []string{name, strings.ToUpper(name[:1]) + name[1:]}
	switch strings.ToLower(name) {
	case "chatgpt":
		candidates = append(candidates, "ChatGPT")
	case "codex":
		candidates = append(candidates, "Codex")
	case "claude":
		candidates = append(candidates, "Claude")
	}
	for _, candidate := range candidates {
		out, err := exec.Command("pgrep", "-x", candidate).Output()
		if err == nil && strings.TrimSpace(string(out)) != "" {
			return true
		}
	}
	out, err := exec.Command("pgrep", "-if", name).Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}
