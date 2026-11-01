package gateway

// 开机自启开关：桌面端独有（配置路径、启动参数由壳决定）。
// 无头模式如实返回 unsupported，而不是写一个会在错误目录启动的 Run 项。

import (
	"encoding/json"
	"io"
	"net/http"

	"aether-cockpit/internal/core/appstart"
)

func (g *Gateway) autoStartStatus() appstart.Status {
	if !g.cfg.DesktopApp {
		return appstart.Status{Supported: false}
	}
	if g.autoStartGet == nil {
		return appstart.Get()
	}
	return g.autoStartGet()
}

// handleAutoStartSetting POST /api/settings/autostart {"on":true|false}
func (g *Gateway) handleAutoStartSetting(w http.ResponseWriter, r *http.Request) {
	if !g.cfg.DesktopApp {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "无头模式不支持开机自启（启动参数与数据目录由你的脚本决定）"})
		return
	}
	var body struct {
		On *bool `json:"on"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil || body.On == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 on"})
		return
	}
	set := g.autoStartSet
	if set == nil {
		set = appstart.Set
	}
	if err := set(*body.On); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "写入开机自启失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "autostart": g.autoStartStatus()})
}
