package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GET/POST /api/privacy-model —— 设置页「本地模型」面板。
//
// aether 的模型是它自己下载的 GGUF；本项目的 PII 检测是"用户自带边车
// 进程"（SidecarConfig，行协议）。这里如实桥接同一个面板：
// 没配置边车时 supported=false（面板显示"此系统暂不可用"），配置后
// 显示命令、文件大小与运行状态；on/off 对应边车开关，remove 清除配置。
// 没有"下载进度"这回事，所以 downloading 永远为 false。

func (g *Gateway) privacyModelView() map[string]any {
	g.settingsMu.Lock()
	cfg := g.settings.Privacy
	g.settingsMu.Unlock()

	v := map[string]any{
		"supported":   false,
		"installed":   false,
		"on":          false,
		"running":     false,
		"model":       "custom-sidecar",
		"size":        int64(0),
		"models":      []any{},
		"gpu":         false,
		"downloading": false,
	}
	side := cfg.Sidecar
	if side == nil || strings.TrimSpace(side.Command) == "" {
		if cfg.Enabled {
			if p := g.privacyErr.Load(); p != nil {
				v["error"] = *p
			}
		}
		return v
	}

	name := filepath.Base(strings.TrimSpace(side.Command))
	var size int64
	installed := false
	if fi, err := os.Stat(side.Command); err == nil && !fi.IsDir() {
		installed = true
		size = fi.Size()
	} else if p, err := exec.LookPath(side.Command); err == nil {
		if fi, err := os.Stat(p); err == nil {
			installed = true
			size = fi.Size()
			name = filepath.Base(p)
		}
	}
	on := cfg.Enabled && sidecarOn(side)
	eng := g.currentPrivacyEngine()
	running := eng != nil && eng.sidecar != nil

	v["supported"] = true
	v["installed"] = installed
	v["on"] = on
	v["running"] = running
	v["model"] = name
	v["size"] = size
	v["models"] = []any{map[string]any{
		"model": name, "size": size, "supported": true,
		"installed": installed, "downloading": false,
	}}
	if cfg.Enabled {
		if p := g.privacyErr.Load(); p != nil {
			v["failed"] = *p
		}
	}
	return v
}

func (g *Gateway) handlePrivacyModel(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			Do    string `json:"do"`
			Model string `json:"model"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body)
		switch body.Do {
		case "", "refresh", "cancel", "pick":
			// 只有一个自带边车，pick 没有别的可换：返回现状即可。
		case "on", "off":
			g.settingsMu.Lock()
			side := g.settings.Privacy.Sidecar
			if side == nil || strings.TrimSpace(side.Command) == "" {
				g.settingsMu.Unlock()
				writeJSON(w, http.StatusOK, g.privacyModelView())
				return
			}
			on := body.Do == "on"
			side.Enabled = &on
			if on {
				// 模型要工作，遮蔽总开关必须也是开的。
				g.settings.Privacy.Enabled = true
			}
			_ = g.saveSettings()
			g.rebuildPrivacyEngine()
			g.settingsMu.Unlock()
		case "remove":
			g.settingsMu.Lock()
			g.settings.Privacy.Sidecar = nil
			_ = g.saveSettings()
			g.rebuildPrivacyEngine()
			g.settingsMu.Unlock()
		default:
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "未知动作: " + body.Do})
			return
		}
	}
	writeJSON(w, http.StatusOK, g.privacyModelView())
}
