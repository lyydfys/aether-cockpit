package gateway

// 渠道级模型选择与模型积分倍率：
//   - 选择决定 /v1/models 与 Gemini 模型清单里出现哪些模型；
//   - 倍率只用于前端展示"这个模型每积分消耗多少"（缺省 1）。

import (
	"strings"

	"aether-cockpit/internal/provider"
)

// normalizeModelName 把编辑器可能提交的 "渠道/模型" 折成裸模型名。
func normalizeModelName(pid, model string) string {
	m := strings.TrimSpace(model)
	if m == "" {
		return ""
	}
	return strings.TrimPrefix(m, pid+"/")
}

// providerModelSelection 返回渠道的模型选择。
// configured=false 表示从未配置（暴露全部）。
func (g *Gateway) providerModelSelection(pid string) ([]string, bool) {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	list, ok := g.settings.ProviderModels[pid]
	if !ok || list == nil {
		return nil, false
	}
	return append([]string(nil), list...), true
}

// providerModelCreditMap 返回渠道的模型倍率表（只含正值）。
func (g *Gateway) providerModelCreditMap(pid string) map[string]float64 {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	src := g.settings.ProviderModelCredit[pid]
	out := make(map[string]float64, len(src))
	for k, v := range src {
		if v > 0 {
			out[normalizeModelName(pid, k)] = v
		}
	}
	return out
}

func modelInList(list []string, model string) bool {
	model = strings.TrimSpace(model)
	for _, m := range list {
		if strings.TrimSpace(m) == model {
			return true
		}
	}
	return false
}

// saveProviderModelSelection 保存渠道级模型选择与倍率；models 可为空（=都不暴露）。
func (g *Gateway) saveProviderModelSelection(pid string, models []string, rates map[string]float64) error {
	clean := make([]string, 0, len(models))
	seen := map[string]bool{}
	for _, raw := range models {
		m := normalizeModelName(pid, raw)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		clean = append(clean, m)
	}
	rateMap := make(map[string]float64, len(rates))
	for k, v := range rates {
		m := normalizeModelName(pid, k)
		if m != "" && v > 0 {
			rateMap[m] = v
		}
	}

	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.ProviderModels == nil {
		g.settings.ProviderModels = map[string][]string{}
	}
	g.settings.ProviderModels[pid] = clean
	if g.settings.ProviderModelCredit == nil {
		g.settings.ProviderModelCredit = map[string]map[string]float64{}
	}
	if len(rateMap) == 0 {
		delete(g.settings.ProviderModelCredit, pid)
	} else {
		g.settings.ProviderModelCredit[pid] = rateMap
	}
	return g.saveSettings()
}

// accountHasProvider 报告该渠道是否已有未停用账号（"已在本项目接入"）。
func (g *Gateway) accountHasProvider(pid string) bool {
	for _, env := range g.pool.All() {
		if env.ProviderID == pid && !env.Disabled() {
			return true
		}
	}
	return false
}

// accountServesModel 判断账号白名单是否允许该模型（空白名单 = 全部允许）。
func accountServesModel(env *provider.AccountEnv, full, bare string) bool {
	allowed := env.AllowedModels()
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if a == full || a == bare || strings.TrimPrefix(a, env.ProviderID+"/") == bare {
			return true
		}
	}
	return false
}

// availableModelIDs 返回当前真正可用的模型（渠道已接入 + 渠道选择 + 账号白名单）。
func (g *Gateway) availableModelIDs() map[string]bool {
	ids := map[string]bool{}
	known := g.models.KnownModels()
	for _, env := range g.pool.All() {
		if env.Disabled() {
			continue
		}
		pid := env.ProviderID
		sel, configured := g.providerModelSelection(pid)
		for _, m := range known {
			if !strings.HasPrefix(m.ID, pid+"/") {
				continue
			}
			bare := strings.TrimPrefix(m.ID, pid+"/")
			if configured && !modelInList(sel, bare) {
				continue
			}
			if !accountServesModel(env, m.ID, bare) {
				continue
			}
			ids[m.ID] = true
		}
	}
	return ids
}

// availableGroups 返回至少有一个成员渠道已接入的路由组。
func (g *Gateway) availableGroups() map[string]bool {
	out := map[string]bool{}
	for name, info := range g.models.VirtualGroupDetails() {
		for _, pid := range info.Chain {
			if g.accountHasProvider(pid) {
				out[name] = true
				break
			}
		}
	}
	return out
}
