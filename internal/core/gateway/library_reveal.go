package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// revealPathOpener 在系统文件管理器里显示路径：目录直接打开，文件则选中。
// 变量化便于单测替换，不真的弹窗。
var revealPathOpener = func(path string, isDir bool) error {
	switch runtime.GOOS {
	case "windows":
		if isDir {
			return exec.Command("explorer.exe", path).Start()
		}
		return exec.Command("explorer.exe", "/select,"+path).Start()
	case "darwin":
		if isDir {
			return exec.Command("open", path).Start()
		}
		return exec.Command("open", "-R", path).Start()
	default:
		dir := path
		if !isDir {
			dir = filepath.Dir(path)
		}
		return exec.Command("xdg-open", dir).Start()
	}
}

// handleLibraryReveal POST /api/library/reveal {"path":"…"}
//
// 资源库页的「在文件管理器中显示」：只接受本机回环请求，路径必须存在。
func (g *Gateway) handleLibraryReveal(w http.ResponseWriter, r *http.Request) {
	if !isLocalRequest(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "只允许本机打开文件管理器"})
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body)
	p := strings.TrimSpace(body.Path)
	if p == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少路径"})
		return
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "路径无效: " + err.Error()})
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "路径不存在: " + err.Error()})
		return
	}
	if err := revealPathOpener(abs, info.IsDir()); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "打开文件管理器失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": abs})
}
