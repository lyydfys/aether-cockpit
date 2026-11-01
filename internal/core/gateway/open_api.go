package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
)

// openExternal 打开系统浏览器/默认应用。只接受 http(s) 地址，
// 不经 shell（Windows 用 rundll32 的参数形式），避免命令注入。
var openExternal = func(raw string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", raw).Start()
	case "darwin":
		return exec.Command("open", raw).Start()
	default:
		return exec.Command("xdg-open", raw).Start()
	}
}

// handleOpen POST /api/open {"url":"https://…"}
//
// aether 前端的站外链接（供应商主页、状态页等）在桌面模式下走这里。
func (g *Gateway) handleOpen(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body)
	raw := strings.TrimSpace(body.URL)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "只允许打开 http(s) 地址"})
		return
	}
	if err := openExternal(u.String()); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "打开链接失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
