package gateway

import "strings"

// 渠道级兜底链（渠道编辑器 高级 → Fallback）。
//
// 语义与 aether 一致：本渠道接不住（额度耗尽 / 429 / 过载 / 宕机）时，
// 按用户给的顺序再试这些模型（形如 "provider/model"）。此前 `fallback`
// 只在前端 draft 里，providerDraft 没有字段，保存被静默丢弃。
//
// 实现方式：dispatch 拿到候选链后，在每个候选后面插入它所属渠道的兜底
// 绑定（去重、限量），这样"主渠道失败 → 本渠道兜底 → 下一个主候选"的顺序
// 自然成立，不需要改重试循环。

const maxProviderFallbacks = 8

// providerFallback 返回某渠道已保存的兜底模型列表（副本）。
func (g *Gateway) providerFallback(providerID string) []string {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	src := g.settings.ProviderFallback[providerID]
	if len(src) == 0 {
		return nil
	}
	out := make([]string, len(src))
	copy(out, src)
	return out
}

// setProviderFallback 保存兜底链；空列表删除条目。
func (g *Gateway) setProviderFallback(providerID string, fallback []string) error {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	clean := make([]string, 0, len(fallback))
	seen := map[string]bool{}
	for _, f := range fallback {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		clean = append(clean, f)
		if len(clean) >= maxProviderFallbacks {
			break
		}
	}
	if len(clean) == 0 {
		if g.settings.ProviderFallback != nil {
			delete(g.settings.ProviderFallback, providerID)
		}
	} else {
		if g.settings.ProviderFallback == nil {
			g.settings.ProviderFallback = map[string][]string{}
		}
		g.settings.ProviderFallback[providerID] = clean
	}
	return g.saveSettings()
}

// expandFallbacks 把每个候选所属渠道的兜底模型插到它后面。
func (g *Gateway) expandFallbacks(bindings []modelBinding) []modelBinding {
	if len(bindings) == 0 {
		return bindings
	}
	out := make([]modelBinding, 0, len(bindings)+4)
	seen := make(map[string]bool, len(bindings)+4)
	push := func(b modelBinding) {
		key := b.ProviderID + "/" + b.UpstreamModel
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, b)
	}
	for _, b := range bindings {
		push(b)
		for _, fb := range g.providerFallback(b.ProviderID) {
			for _, cand := range g.models.Resolve(fb) {
				push(cand)
			}
		}
	}
	return out
}
