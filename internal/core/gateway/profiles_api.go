package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/clients"
	"aether-cockpit/internal/core/eventbus"
)

// Profile 是一套命名好的客户端配置：激活时把其中每个客户端一次性写盘。
//
// 与"单客户端一键注入"的分工：单次注入解决"这个客户端接上网关"，
// 档案解决"我有多套常用组合（省钱档 / 高质量档），一键全切"。
type Profile struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Clients   []ProfileClient `json:"clients"`
	UpdatedAt int64           `json:"updated_at"`
}

// ProfileClient 是档案里单个客户端的期望配置。
type ProfileClient struct {
	ClientID string `json:"client_id"`
	Model    string `json:"model,omitempty"`
	Origin   string `json:"origin,omitempty"`
}

// profileID 由名称派生，保证同名档案覆盖而不是堆积。
func profileID(name string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(name)))
	return "p-" + hex.EncodeToString(sum[:4])
}

// handleProfiles GET /api/profiles
func (g *Gateway) handleProfiles(w http.ResponseWriter, r *http.Request) {
	g.settingsMu.Lock()
	profiles := append([]Profile(nil), g.settings.Profiles...)
	active := g.settings.ActiveProfile
	g.settingsMu.Unlock()
	if profiles == nil {
		profiles = []Profile{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": profiles, "active": active})
}

// handleSaveProfile POST /api/profiles
//
// 请求体：{"name":"省钱档","clients":[{"client_id":"claude_code","model":"qoder-cn/auto"}]}
func (g *Gateway) handleSaveProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string          `json:"id"`
		Name    string          `json:"name"`
		Clients []ProfileClient `json:"clients"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name 不能为空"})
		return
	}
	if len(body.Clients) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "至少选择一个客户端"})
		return
	}
	// 校验客户端 ID：写错一个 ID 会让整次激活只成功一半。
	mgr := g.clientsManager()
	if mgr == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "客户端注入不可用"})
		return
	}
	for _, c := range body.Clients {
		if _, ok := mgr.Lookup(c.ClientID); !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "未知客户端: " + c.ClientID})
			return
		}
	}

	p := Profile{
		ID:        strings.TrimSpace(body.ID),
		Name:      name,
		Clients:   body.Clients,
		UpdatedAt: time.Now().Unix(),
	}
	if p.ID == "" {
		p.ID = profileID(name)
	}

	g.settingsMu.Lock()
	replaced := false
	for i := range g.settings.Profiles {
		if g.settings.Profiles[i].ID == p.ID {
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
	eventbus.Emit(eventbus.TopicSpec+".updated", map[string]any{"source": "profile", "profile": p.ID})
	writeJSON(w, http.StatusOK, map[string]any{"profile": p, "replaced": replaced})
}

// handleDeleteProfile DELETE /api/profiles/{id}
func (g *Gateway) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g.settingsMu.Lock()
	kept := g.settings.Profiles[:0]
	found := false
	for _, p := range g.settings.Profiles {
		if p.ID == id {
			found = true
			continue
		}
		kept = append(kept, p)
	}
	g.settings.Profiles = kept
	if g.settings.ActiveProfile == id {
		g.settings.ActiveProfile = ""
	}
	err := g.saveSettings()
	g.settingsMu.Unlock()

	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "档案不存在: " + id})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// handleActivateProfile POST /api/profiles/{id}/activate
//
// 把档案里的每个客户端逐个写盘（每个客户端自身仍会先备份）。
// 单客户端失败不影响其它客户端，结果逐条返回。
func (g *Gateway) handleActivateProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	mgr := g.clientsManager()
	if mgr == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "客户端注入不可用"})
		return
	}

	g.settingsMu.Lock()
	var found *Profile
	for i := range g.settings.Profiles {
		if g.settings.Profiles[i].ID == id {
			cp := g.settings.Profiles[i]
			found = &cp
			break
		}
	}
	g.settingsMu.Unlock()
	if found == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "档案不存在: " + id})
		return
	}

	origin := requestOrigin(r, g.PublicAddr())
	apiKey := strings.TrimSpace(g.GatewayKey())
	if apiKey == "" {
		apiKey = "aether-local"
	}

	results := make([]map[string]any, 0, len(found.Clients))
	okN := 0
	for _, c := range found.Clients {
		o := strings.TrimSpace(c.Origin)
		if o == "" {
			o = origin
		}
		bak, err := mgr.Apply(c.ClientID, clients.ApplyParams{
			BaseURL: o,
			APIKey:  apiKey,
			Model:   strings.TrimSpace(c.Model),
		})
		item := map[string]any{"client_id": c.ClientID, "ok": err == nil}
		if err != nil {
			item["error"] = err.Error()
		} else {
			item["backup"] = bak
			okN++
		}
		results = append(results, item)
	}

	if okN > 0 {
		g.settingsMu.Lock()
		g.settings.ActiveProfile = id
		_ = g.saveSettings()
		g.settingsMu.Unlock()
		eventbus.Emit(eventbus.TopicSpec+".updated", map[string]any{"source": "profile_activate", "profile": id})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "ok": okN, "total": len(results)})
}
