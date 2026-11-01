//go:build windows

package sessions

import (
	"aether-cockpit/internal/core/executil"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// WSL 会话计数（只读）：找出发行版里的 $HOME，通过 \\wsl.localhost\<distro>\…
// 读取与 Windows 相同的 .claude/projects / .codex/sessions 布局。
// 检测结果缓存 10 分钟；AETHER_WSL_HOMES（分号分隔）可覆盖，便于测试。

var wslMu sync.Mutex
var wslCache []sessionRoot
var wslAt time.Time

// wslEnvDistro 从注入路径推断发行版名，或读 AETHER_WSL_DISTRO。
func wslEnvDistro(p string) string {
	if d := strings.TrimSpace(os.Getenv("AETHER_WSL_DISTRO")); d != "" {
		return d
	}
	norm := strings.ReplaceAll(p, `\`, "/")
	for _, base := range []string{"//wsl.localhost/", "//wsl$/"} {
		if strings.HasPrefix(norm, base) {
			rest := strings.TrimPrefix(norm, base)
			if i := strings.IndexByte(rest, '/'); i > 0 {
				return rest[:i]
			}
		}
	}
	return "WSL"
}

func wslRoots() []sessionRoot {
	if v := strings.TrimSpace(os.Getenv("AETHER_WSL_HOMES")); v != "" {
		out := []sessionRoot{}
		for _, p := range strings.Split(v, string(os.PathListSeparator)) {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, sessionRoot{Home: p, WSL: wslEnvDistro(p)})
			}
		}
		return out
	}
	wslMu.Lock()
	defer wslMu.Unlock()
	if !wslAt.IsZero() && time.Since(wslAt) < 10*time.Minute {
		return append([]sessionRoot(nil), wslCache...)
	}
	wslCache = detectWSLRoots()
	wslAt = time.Now()
	return append([]sessionRoot(nil), wslCache...)
}

func detectWSLRoots() []sessionRoot {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "wsl.exe", "-l", "-q")
	executil.Hide(cmd)
	raw, err := cmd.Output()
	if err != nil {
		return nil
	}
	out := []sessionRoot{}
	for _, distro := range strings.Split(strings.ReplaceAll(decodeWSLUTF16(raw), "\r", ""), "\n") {
		distro = strings.TrimSpace(distro)
		if distro == "" || strings.HasPrefix(strings.ToLower(distro), "docker") {
			continue
		}
		hctx, hcancel := context.WithTimeout(context.Background(), 4*time.Second)
		cmd := exec.CommandContext(hctx, "wsl.exe", "-d", distro, "--", "sh", "-lc", `printf %s "$HOME"`)
		executil.Hide(cmd)
		rawHome, err := cmd.Output()
		hcancel()
		if err != nil {
			continue
		}
		home := strings.TrimRight(strings.TrimSpace(string(rawHome)), "/")
		if !strings.HasPrefix(home, "/") {
			continue
		}
		for _, base := range []string{"//wsl.localhost/", "//wsl$/"} {
			root := base + distro + home
			if hasAgentSessionDirs(root) {
				out = append(out, sessionRoot{Home: root, WSL: distro})
				break
			}
		}
	}
	return out
}

func hasAgentSessionDirs(home string) bool {
	for _, rel := range [][]string{{".claude", "projects"}, {".codex", "sessions"}} {
		if st, err := os.Stat(filepath.Join(append([]string{home}, rel...)...)); err == nil && st.IsDir() {
			return true
		}
	}
	return false
}

// decodeWSLUTF16 解析 wsl.exe 的 UTF-16LE 输出（发行版名为 ASCII）。
func decodeWSLUTF16(b []byte) string {
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		b = b[2:]
	}
	if len(b)%2 == 1 {
		b = b[:len(b)-1]
	}
	runes := make([]rune, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		runes = append(runes, rune(b[i])|rune(b[i+1])<<8)
	}
	return strings.TrimPrefix(string(runes), "\ufeff")
}
