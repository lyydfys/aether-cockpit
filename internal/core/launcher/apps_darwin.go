//go:build darwin

package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	darwinAppsMu    sync.Mutex
	darwinAppsAt    time.Time
	darwinAppsCache []DesktopApp
)

const darwinAppsTTL = 2 * time.Minute

// DesktopApps 探测 /Applications 与 ~/Applications 下的桌面客户端。
// macOS 没有 Windows 的注册表协议；用 .app bundle 作为启动目标。
func DesktopApps() []DesktopApp {
	darwinAppsMu.Lock()
	defer darwinAppsMu.Unlock()
	if time.Since(darwinAppsAt) < darwinAppsTTL {
		return append([]DesktopApp(nil), darwinAppsCache...)
	}
	darwinAppsAt = time.Now()
	darwinAppsCache = detectDarwinDesktopApps()
	return append([]DesktopApp(nil), darwinAppsCache...)
}

func detectDarwinDesktopApps() []DesktopApp {
	out := make([]DesktopApp, 0, 2)
	if app := findDarwinApp("Claude.app"); app != "" {
		out = append(out, DesktopApp{
			ID: "claude_desktop", Name: "Claude 桌面端", Available: true,
			Target: app, Executable: darwinAppExecutable(app), Note: "使用当前用户目录与登录态",
		})
	} else {
		out = append(out, DesktopApp{
			ID: "claude_desktop", Name: "Claude 桌面端", Available: false,
			Reason: "未检测到 /Applications 或 ~/Applications 里的 Claude.app",
		})
	}
	if app := findDarwinApp("Codex.app", "OpenAI Codex.app"); app != "" {
		out = append(out, DesktopApp{
			ID: "codex_desktop", Name: "Codex 桌面端", Available: true,
			Target: app, Executable: darwinAppExecutable(app), Note: "使用当前用户目录与登录态",
		})
	} else {
		out = append(out, DesktopApp{
			ID: "codex_desktop", Name: "Codex 桌面端", Available: false,
			Reason: "未检测到 /Applications 或 ~/Applications 里的 Codex.app",
		})
	}
	return out
}

func findDarwinApp(names ...string) string {
	home, _ := os.UserHomeDir()
	roots := []string{"/Applications"}
	if home != "" {
		roots = append(roots, filepath.Join(home, "Applications"))
	}
	for _, root := range roots {
		for _, name := range names {
			p := filepath.Join(root, name)
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				return p
			}
		}
	}
	return ""
}

func darwinAppExecutable(app string) string {
	name := strings.TrimSuffix(filepath.Base(app), ".app")
	p := filepath.Join(app, "Contents", "MacOS", name)
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p
	}
	return ""
}

// LaunchDesktopApp 用 open -a 打开 .app；open 会复用已运行实例。
func LaunchDesktopApp(id string) error {
	for _, app := range DesktopApps() {
		if app.ID != id {
			continue
		}
		if !app.Available {
			if app.Reason != "" {
				return fmt.Errorf("%s 不可用：%s", app.Name, app.Reason)
			}
			return fmt.Errorf("%s 不可用", app.Name)
		}
		cmd := exec.Command("open", "-a", app.Target)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("启动失败: %w", err)
		}
		_ = cmd.Process.Release()
		return nil
	}
	return fmt.Errorf("未知桌面应用: %s", id)
}
