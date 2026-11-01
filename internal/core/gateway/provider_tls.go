package gateway

import (
	"net/http"

	"aether-cockpit/internal/provider"
)

// 渠道级"不校验证书"（渠道编辑器 高级 → Don't check its certificate）。
//
// 自建/自签证书的中转站需要在单个渠道上跳过校验；此前 `insecureTLS` 只在前端
// draft 里，`providerDraft` 没有字段，保存被静默丢弃。现在存进
// settings.ProviderInsecureTLS，并在铸造账号 http.Client 时传给 transport
// （只影响该渠道的客户端，不动全局设置）。

// providerInsecureTLS 报告渠道是否跳过证书校验。
func (g *Gateway) providerInsecureTLS(providerID string) bool {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	return g.settings.ProviderInsecureTLS[providerID]
}

// setProviderInsecureTLS 保存该开关；false 删除条目。
func (g *Gateway) setProviderInsecureTLS(providerID string, insecure bool) error {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.ProviderInsecureTLS == nil {
		g.settings.ProviderInsecureTLS = map[string]bool{}
	}
	if insecure {
		g.settings.ProviderInsecureTLS[providerID] = true
	} else {
		delete(g.settings.ProviderInsecureTLS, providerID)
	}
	if len(g.settings.ProviderInsecureTLS) == 0 {
		g.settings.ProviderInsecureTLS = nil
	}
	return g.saveSettings()
}

// clientFor 按渠道的 TLS 开关铸造账号客户端。
func (g *Gateway) clientFor(env *provider.AccountEnv, policy provider.NetworkPolicy) (*http.Client, error) {
	return g.tr.NewClientOpts(policy, env.Proxy(), env.TLSProfile, g.providerInsecureTLS(env.ProviderID))
}
