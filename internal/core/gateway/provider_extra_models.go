package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"aether-cockpit/internal/provider"
)

// 渠道级「手动模型」：上游 /models 漏报（workbuddy-cn 没有 deepseek-v4.1-flash）
// 或厂商根本不实现 /models（Trae）时，用户可以把模型补进目录。
//
// 与 One API / New API 的「添加缺失模型」等价：模型一旦加入，就参与
// 路由与 /v1/models，不受 live 目录是否漏报影响；所有目录模式（merge /
// fallback / allowlist）都会追加，因为这是用户显式配置的模型。

// providerExtraModels 返回渠道手动模型副本。
func (g *Gateway) providerExtraModels(pid string) []string {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	return append([]string(nil), g.settings.ProviderExtraModels[pid]...)
}

func cleanExtraModelName(pid, model string) (string, error) {
	m := normalizeModelName(pid, model)
	if m == "" {
		return "", fmt.Errorf("模型名不能为空")
	}
	if strings.Contains(m, "/") {
		return "", fmt.Errorf("模型名 %q 不能包含 /（这里只填上游裸模型名）", m)
	}
	return m, nil
}

// addProviderExtraModel 添加一个手动模型；如果渠道已配置模型选择，
// 同时把它加入选择，保证 /v1/models 能看到。
func (g *Gateway) addProviderExtraModel(pid, model string) error {
	if _, ok := provider.Get(pid); !ok {
		return fmt.Errorf("渠道未注册: %s", pid)
	}
	m, err := cleanExtraModelName(pid, model)
	if err != nil {
		return err
	}
	g.settingsMu.Lock()
	if g.settings.ProviderExtraModels == nil {
		g.settings.ProviderExtraModels = map[string][]string{}
	}
	list := append([]string(nil), g.settings.ProviderExtraModels[pid]...)
	if !modelInList(list, m) {
		list = append(list, m)
	}
	g.settings.ProviderExtraModels[pid] = list
	if sel, ok := g.settings.ProviderModels[pid]; ok && !modelInList(sel, m) {
		g.settings.ProviderModels[pid] = append(append([]string(nil), sel...), m)
	}
	err = g.saveSettings()
	g.settingsMu.Unlock()
	if err != nil {
		return err
	}
	g.models.Rebuild()
	return nil
}

// removeProviderExtraModel 删除手动模型，并从模型选择里一并移除。
func (g *Gateway) removeProviderExtraModel(pid, model string) error {
	m, err := cleanExtraModelName(pid, model)
	if err != nil {
		return err
	}
	g.settingsMu.Lock()
	list := g.settings.ProviderExtraModels[pid]
	out := make([]string, 0, len(list))
	for _, x := range list {
		if normalizeModelKey(x) != normalizeModelKey(m) {
			out = append(out, x)
		}
	}
	if len(out) == 0 {
		delete(g.settings.ProviderExtraModels, pid)
	} else {
		g.settings.ProviderExtraModels[pid] = out
	}
	if len(g.settings.ProviderExtraModels) == 0 {
		g.settings.ProviderExtraModels = nil
	}
	if sel, ok := g.settings.ProviderModels[pid]; ok {
		kept := make([]string, 0, len(sel))
		for _, x := range sel {
			if normalizeModelKey(x) != normalizeModelKey(m) {
				kept = append(kept, x)
			}
		}
		g.settings.ProviderModels[pid] = kept
	}
	err = g.saveSettings()
	g.settingsMu.Unlock()
	if err != nil {
		return err
	}
	g.models.Rebuild()
	return nil
}

// handleProviderExtraModel POST /api/providers/{id}/models/extra
// body: {"model":"deepseek-v4.1-flash","action":"add|remove"}
func (g *Gateway) handleProviderExtraModel(w http.ResponseWriter, r *http.Request) {
	pid := strings.TrimSpace(r.PathValue("id"))
	if pid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少渠道 id"})
		return
	}
	var body struct {
		Model  string `json:"model"`
		Action string `json:"action"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	var err error
	switch strings.ToLower(strings.TrimSpace(body.Action)) {
	case "add", "":
		err = g.addProviderExtraModel(pid, body.Model)
	case "remove":
		err = g.removeProviderExtraModel(pid, body.Model)
	default:
		err = fmt.Errorf("未知动作 %q（可选 add / remove）", body.Action)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	g.respondProviders(w, r)
}
