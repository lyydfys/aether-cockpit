package gateway

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"aether-cockpit/internal/core/apppaths"
	"aether-cockpit/internal/core/clients"
	"aether-cockpit/internal/core/eventbus"
)

// 客户端配置一键注入 —— 对齐 aether 的"原子化改写客户端配置"体验。
//
// 与 /api/inject（生成片段让用户自己粘贴）互补：inject 适合"看一眼内容"，
// 这里适合"直接接好"，并永远保留可一键还原的备份。

// clientsManager 懒初始化本机客户端管理器。
func (g *Gateway) clientsManager() *clients.Manager {
	g.clientsOnce.Do(func() {
		m, err := clients.New()
		if err != nil {
			log.Printf("[WARN] 客户端注入不可用（无法定位主目录）: %v", err)
		}
		if m != nil {
			// 自定义客户端（默认 data/clients.json）：让 CLI 类工具不用改代码就能接入。
			path := g.cfg.CustomClientsPath
			if strings.TrimSpace(path) == "" {
				path = apppaths.Data("clients.json")
			}
			if err := m.LoadCustomFile(path); err != nil {
				log.Printf("[WARN] 自定义客户端定义无效，已忽略: %v", err)
			}
			// 键级还原状态：记录"我方写过哪些路径、接入前是什么"，
			// 断开时只还原这些路径，用户接入后手改的其它键保留。
			m.SetKeyStatePath(apppaths.Data("clients-state.json"))
		}
		g.clientMgr = m
	})
	// WSL 发行版会随用户安装/启停变化：每次取用时同步一次目标列表
	// （检测本身有缓存，这里只是一次切片替换）。
	if g.clientMgr != nil {
		g.clientMgr.SetTargets(g.wslTargets())
	}
	return g.clientMgr
}

// handleClients GET /api/clients —— 列出全部客户端与接入状态。
func (g *Gateway) handleClients(w http.ResponseWriter, r *http.Request) {
	mgr := g.clientsManager()
	if mgr == nil {
		writeJSON(w, http.StatusOK, map[string]any{"clients": []any{}, "available": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"clients":   mgr.List(),
		"available": true,
		"targets":   mgr.Targets(),
		"wsl":       g.wslView(),
	})
}

// handleClientApply POST /api/clients/{id}/apply
//
// 请求体（均可选）：
//
//	{"model": "claude-sonnet-4-6", "origin": "http://192.168.1.5:7860"}
//
// origin 缺省用当前请求的 Host——浏览器能打开管理界面，客户端就能连上网关。
func (g *Gateway) handleClientApply(w http.ResponseWriter, r *http.Request) {
	mgr := g.clientsManager()
	if mgr == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "客户端注入不可用"})
		return
	}
	id := r.PathValue("id")

	var body struct {
		Model  string `json:"model"`
		Origin string `json:"origin"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
			return
		}
	}

	origin := strings.TrimSpace(body.Origin)
	if origin == "" {
		o, oerr := g.clientOriginFor(r, mgr, id)
		if oerr != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"error": oerr.Error()})
			return
		}
		origin = o
	}
	apiKey := strings.TrimSpace(g.cfg.APIKey)
	if apiKey == "" {
		// 本地免鉴权时给一个非空占位值：多数客户端会拒绝空 Authorization。
		apiKey = "aether-local"
	}

	bak, err := mgr.Apply(id, clients.ApplyParams{
		BaseURL: origin,
		APIKey:  apiKey,
		Model:   strings.TrimSpace(body.Model),
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	eventbus.Emit(eventbus.TopicSpec+".updated", map[string]any{"source": "client_apply", "client": id})
	writeJSON(w, http.StatusOK, map[string]any{
		"applied":     id,
		"backup":      bak,
		"origin":      origin,
		"has_api_key": apiKey != "aether-local",
	})
}

// handleClientRestore POST /api/clients/{id}/restore
//
// 请求体：{"backup": "<文件路径>"}，缺省恢复最近一份备份。
func (g *Gateway) handleClientRestore(w http.ResponseWriter, r *http.Request) {
	mgr := g.clientsManager()
	if mgr == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "客户端注入不可用"})
		return
	}
	id := r.PathValue("id")

	var body struct {
		Backup string `json:"backup"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
			return
		}
	}
	if err := mgr.Restore(id, strings.TrimSpace(body.Backup)); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	eventbus.Emit(eventbus.TopicSpec+".updated", map[string]any{"source": "client_restore", "client": id})
	writeJSON(w, http.StatusOK, map[string]any{"restored": id})
}
