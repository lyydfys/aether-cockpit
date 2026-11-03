package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/provider"
)

// 渠道页的开关与排序：停用渠道（不参与路由）、渠道顺序、账号顺序、
// 余额探测，以及窗口壳的少量无操作端点。

// providerIsEnabled 报告渠道是否参与路由。
//
// 两层：手动停用（永久，直到用户开回来）+ 自动下线（账号全灭时由巡检置上，
// 到 Until 自动放行，等巡检真的清掉记录）。
func (g *Gateway) providerIsEnabled(pid string) bool {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	for _, d := range g.settings.DisabledProviders {
		if d == pid {
			return false
		}
	}
	if st, ok := g.settings.AutoDisabledProviders[pid]; ok {
		if st.Until == 0 || time.Now().Unix() < st.Until {
			return false
		}
		// 到点了：账号的 resetAt 过期后 Available() 会自己转 ready，
		// 这里放行不会白撞上游；巡检跑一轮后会正式清掉记录。
	}
	return true
}

// providerManuallyOff 只判手动停用。
// 前端要区分"我关的"和"系统因为没额度关的"，两者文案与恢复动作不同。
func (g *Gateway) providerManuallyOff(pid string) bool {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	for _, d := range g.settings.DisabledProviders {
		if d == pid {
			return true
		}
	}
	return false
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
	// 手动启用时把自动下线状态一起清掉，否则巡检下一轮又会把它关回去，
	// 用户会以为开关坏了。（清完如果账号确实全灭，巡检会重新置上。）
	if !off {
		g.clearAutoDisable(body.ID)
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
