//go:build darwin

package desktop

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// preparePlatform 处理 macOS .app 与命令行启动的差异：
//   - Finder 启动时工作目录是 /，数据必须落到 ~/Library/Application Support；
//   - GUI 进程不继承登录 shell 的 PATH，Claude Code/Codex CLI 会找不到。
func preparePlatform() {
	if strings.TrimSpace(os.Getenv("AETHER_DATA_DIR")) == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			dir := filepath.Join(home, "Library", "Application Support", "AetherCockpit")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				log.Printf("[WARN] 创建 macOS 数据目录失败: %v", err)
			} else {
				_ = os.Setenv("AETHER_DATA_DIR", dir)
			}
		}
	}
	ensureLoginShellPath()
}

func ensureLoginShellPath() {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/zsh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, shell, "-lc", `printf '\n%s\n' "$PATH"`).Output()
	if err != nil {
		return
	}
	loginPath := lastNonEmptyLine(string(out))
	home, _ := os.UserHomeDir()
	extra := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	if home != "" {
		extra = append(extra, filepath.Join(home, ".local", "bin"))
	}
	if loginPath != "" {
		extra = append(extra, loginPath)
	}
	merged := mergeSearchPath(os.Getenv("PATH"), strings.Join(extra, string(os.PathListSeparator)))
	if merged != "" {
		_ = os.Setenv("PATH", merged)
	}
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if v := strings.TrimSpace(lines[i]); v != "" {
			return v
		}
	}
	return ""
}

func mergeSearchPath(current, extra string) string {
	seen := map[string]bool{}
	out := make([]string, 0, 16)
	for _, list := range []string{extra, current} {
		for _, p := range filepath.SplitList(list) {
			p = strings.TrimSpace(p)
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}
