package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/clients"
)

// aether Agents 页的「方案」用的是 /api/profile/{save|use|delete}：
// save 捕捉当前已接入客户端的配置，use 逐个写回，delete 删除。
// 这些是 /api/profiles CRUD 的兼容别名，返回 /api/state 形状（前端直接当 state 用）。

// handleProfileActionCompat POST /api/profile/{save|use|delete}
func (g *Gateway) handleProfileActionCompat(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name 不能为空"})
		return
	}

	switch action {
	case "save":
		g.profileSaveCompat(w, r, name)
	case "use":
		g.profileUseCompat(w, r, name)
	case "delete":
		g.profileDeleteCompat(w, r, name)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "未知方案动作: " + action})
	}
}

// profileSaveCompat 把当前已接入客户端的 base_url/model 快照成同名方案。
func (g *Gateway) profileSaveCompat(w http.ResponseWriter, r *http.Request, name string) {
	mgr := g.clientsManager()
	if mgr == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "客户端注入不可用"})
		return
	}
	snapshot := make([]ProfileClient, 0, 4)
	for _, st := range mgr.List() {
		if st.Managed == nil || strings.TrimSpace(st.Managed.BaseURL) == "" {
			continue
		}
		snapshot = append(snapshot, ProfileClient{
			ClientID: st.ID,
			Model:    st.Managed.Model,
			Origin:   st.Managed.BaseURL,
		})
	}
	if len(snapshot) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "还没有接入任何 Agent；先在 Agents 页打开接入再保存方案"})
		return
	}
	p := Profile{ID: profileID(name), Name: name, Clients: snapshot, UpdatedAt: time.Now().Unix()}
	g.settingsMu.Lock()
	replaced := false
	for i := range g.settings.Profiles {
		if g.settings.Profiles[i].ID == p.ID || g.settings.Profiles[i].Name == name {
			g.settings.Profiles[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		g.settings.Profiles = append(g.settings.Profiles, p)
	}
	err := g.saveSettings()
	g.settingsMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
		return
	}
	g.writeStateWith(w, nil)
}

func (g *Gateway) profileUseCompat(w http.ResponseWriter, r *http.Request, name string) {
	mgr := g.clientsManager()
	if mgr == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "客户端注入不可用"})
		return
	}
	g.settingsMu.Lock()
	var found *Profile
	for i := range g.settings.Profiles {
		if g.settings.Profiles[i].Name == name || g.settings.Profiles[i].ID == name {
			cp := g.settings.Profiles[i]
			found = &cp
			break
		}
	}
	g.settingsMu.Unlock()
	if found == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "方案不存在: " + name})
		return
	}

	origin := requestOrigin(r, g.PublicAddr())
	apiKey := strings.TrimSpace(g.GatewayKey())
	if apiKey == "" {
		apiKey = "aether-local"
	}
	okN := 0
	for _, c := range found.Clients {
		o := strings.TrimSpace(c.Origin)
		if o == "" {
			o = origin
		}
		if _, err := mgr.Apply(c.ClientID, clients.ApplyParams{
			BaseURL: o, APIKey: apiKey, Model: strings.TrimSpace(c.Model),
		}); err == nil {
			okN++
		}
	}
	if okN > 0 {
		g.settingsMu.Lock()
		g.settings.ActiveProfile = found.ID
		_ = g.saveSettings()
		g.settingsMu.Unlock()
	}
	g.writeStateWith(w, map[string]any{
		"changed": okN,
		"library": map[string]any{},
	})
}

func (g *Gateway) profileDeleteCompat(w http.ResponseWriter, r *http.Request, name string) {
	g.settingsMu.Lock()
	kept := make([]Profile, 0, len(g.settings.Profiles))
	found := false
	for _, p := range g.settings.Profiles {
		if p.Name == name || p.ID == name {
			found = true
			if g.settings.ActiveProfile == p.ID {
				g.settings.ActiveProfile = ""
			}
			continue
		}
		kept = append(kept, p)
	}
	g.settings.Profiles = kept
	err := g.saveSettings()
	g.settingsMu.Unlock()
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "方案不存在: " + name})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
		return
	}
	g.writeStateWith(w, nil)
}

// writeStateWith 输出 /api/state 形状，附带动作结果（前端把它整体当 state）。
func (g *Gateway) writeStateWith(w http.ResponseWriter, extra map[string]any) {
	b, err := json.Marshal(g.buildAetherState())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "生成状态失败"})
		return
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "生成状态失败"})
		return
	}
	for k, v := range extra {
		m[k] = v
	}
	writeJSON(w, http.StatusOK, m)
}
