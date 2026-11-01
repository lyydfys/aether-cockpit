package gateway

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// POST /api/settings/reveal：在系统文件管理器里打开配置目录
// （设置页 "Config folder" 的 Open）。打开的永远是本机固定的配置目录，
// 不接受任何调用方给的路径，避免变成"任意目录打开"的入口。

func (g *Gateway) configDir() string {
	dir := filepath.Dir(g.settingsPath)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return dir
}

// revealDir 用系统文件管理器打开 dir。
func revealDir(dir string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// 不等待：explorer.exe 的退出码不表示成功与否。
	go func() { _ = cmd.Wait() }()
	return nil
}

// openConfigDir 可在测试里替换，避免真的拉起文件管理器。
var openConfigDir = revealDir

func (g *Gateway) handleRevealSettings(w http.ResponseWriter, r *http.Request) {
	dir := g.configDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSON(w, 500, map[string]any{"error": "无法创建配置目录: " + err.Error()})
		return
	}
	if err := openConfigDir(dir); err != nil {
		msg := strings.TrimSpace(err.Error())
		if msg == "" {
			msg = "无法打开文件管理器"
		}
		writeJSON(w, 500, map[string]any{"error": msg})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "dir": dir})
}
