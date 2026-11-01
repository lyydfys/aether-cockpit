package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"aether-cockpit/internal/provider"
)

// 渠道页的开关与排序：停用渠道（不参与路由）、渠道顺序、账号顺序、
// 余额探测，以及窗口壳的少量无操作端点。

// providerIsEnabled 报告渠道是否启用（由 settings.DisabledProviders 驱动）。
func (g *Gateway) providerIsEnabled(pid string) bool {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	for _, d := range g.settings.DisabledProviders {
		if d == pid {
			return false
		}
	}
	return true
}

// providerOrder 返回用户排定的渠道顺序（未列出的保持注册顺序）。
func (g *Gateway) providerOrder() map[string]int {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	out := make(map[string]int, len(g.settings.ProviderOrder))
	for i, id := range g.settings.ProviderOrder {
		out[id] = i
	}
	return out
}

// handleProviderSwitch POST /api/provider/on|off {id}
func (g *Gateway) handleProviderSwitch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少渠道 id"})
		return
	}
	if _, ok := provider.Get(body.ID); !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "渠道未注册: " + body.ID})
		return
	}
	off := strings.HasSuffix(r.URL.Path, "/off")
	g.settingsMu.Lock()
	list := make([]string, 0, len(g.settings.DisabledProviders)+1)
	for _, d := range g.settings.DisabledProviders {
		if d != body.ID {
			list = append(list, d)
		}
	}
	if off {
		list = append(list, body.ID)
	}
	g.settings.DisabledProviders = list
	err := g.saveSettings()
	g.settingsMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
		return
	}
	g.respondProviders(w, r)
}

// handleProvidersArrange POST /api/providers/arrange {order:[...]}
func (g *Gateway) handleProvidersArrange(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Order []string `json:"order"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	valid := make([]string, 0, len(body.Order))
	for _, id := range body.Order {
		if _, ok := provider.Get(id); ok {
			valid = append(valid, id)
		}
	}
	g.settingsMu.Lock()
	g.settings.ProviderOrder = valid
	err := g.saveSettings()
	g.settingsMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
		return
	}
	g.respondProviders(w, r)
}

// handleProviderAccountsArrange POST /api/provider/arrange {id, accountOrder:[labels]}
//
// 账号顺序映射成显式优先级（第 1 个 = 首选），与「设为首选」同一套。
func (g *Gateway) handleProviderAccountsArrange(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID           string   `json:"id"`
		AccountOrder []string `json:"accountOrder"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	envs := g.pool.ListByProvider(body.ID)
	byLabel := make(map[string]*provider.AccountEnv, len(envs))
	for _, e := range envs {
		byLabel[e.Label] = e
	}
	next := 1
	for _, label := range body.AccountOrder {
		if e := byLabel[label]; e != nil {
			e.SetPriority(next)
			next++
		}
	}
	g.saveAccountsNow()
	g.respondProviders(w, r)
}

// handleWindowStub POST /api/window/* —— 单窗口桌面壳的窗口动作。
// 本项目只有一个主窗口：切视图由前端路由完成，这里只需成功应答。
func (g *Gateway) handleWindowStub(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
