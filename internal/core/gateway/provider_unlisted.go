package gateway

// 渠道级"只在路由分组里使用"（渠道编辑器 高级 → Only through routing groups）。
//
// magpie 语义：该渠道的模型不出现在下游的模型清单里，只能通过路由分组/显式
// 指名使用。此前 `unlisted` 只在前端 draft 里，providerDraft 没有字段，保存被
// 静默丢弃——勾了等于没勾。
//
// 实现：存进 settings.ProviderUnlisted，并在 modelVisible（/v1/models 与
// /v1beta/models 的共同出口）里隐藏该渠道的直接条目；虚拟分组名单独列出，
// 分组内的模型照常可用，显式请求也仍然可达（只是不再"被推荐"）。

// providerUnlisted 报告渠道是否设为"只在分组里使用"。
func (g *Gateway) providerUnlisted(providerID string) bool {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	return g.settings.ProviderUnlisted[providerID]
}

// setProviderUnlisted 保存该开关；false 删除条目。
func (g *Gateway) setProviderUnlisted(providerID string, unlisted bool) error {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.ProviderUnlisted == nil {
		g.settings.ProviderUnlisted = map[string]bool{}
	}
	if unlisted {
		g.settings.ProviderUnlisted[providerID] = true
	} else {
		delete(g.settings.ProviderUnlisted, providerID)
	}
	if len(g.settings.ProviderUnlisted) == 0 {
		g.settings.ProviderUnlisted = nil
	}
	return g.saveSettings()
}
