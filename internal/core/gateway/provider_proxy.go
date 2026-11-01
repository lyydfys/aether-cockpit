package gateway

import (
	"strings"

	"aether-cockpit/internal/provider"
)

// 渠道级 / 账号级代理：
//   - ""        跟随全局代理（proxy_api.go 的 settings/proxy）
//   - "direct"  该渠道/账号强制直连
//   - URL       http/https/socks5 自定义代理
//
// 账号级优先于渠道级，渠道级优先于全局；direct_force 渠道默认直连，
// 但显式 custom 仍可覆盖（与 aether 的 provider proxy 语义一致）。

func accountProxyKey(providerID, label, accountID string) string {
	name := strings.ToLower(strings.TrimSpace(label))
	if name == "" {
		name = accountID
	}
	return providerID + "\x00" + name
}

func (g *Gateway) providerProxy(providerID string) string {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	return g.settings.ProviderProxies[providerID]
}

func (g *Gateway) resolvedAccountProxy(providerID, label, accountID string) string {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if v, ok := g.settings.AccountProxies[accountProxyKey(providerID, label, accountID)]; ok {
		return v
	}
	return g.settings.ProviderProxies[providerID]
}

// providerProxyView 返回打码后的渠道/账号代理（响应给编辑器）。
func (g *Gateway) providerProxyView(providerID string) (string, map[string]string) {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	proxy := maskProxyURL(g.settings.ProviderProxies[providerID])
	accounts := map[string]string{}
	prefix := providerID + "\x00"
	for k, v := range g.settings.AccountProxies {
		if strings.HasPrefix(k, prefix) && v != "" {
			accounts[strings.TrimPrefix(k, prefix)] = maskProxyURL(v)
		}
	}
	return proxy, accounts
}

// setProviderProxy 保存渠道/账号代理并立刻重建该渠道账号的客户端。
// proxy 为 nil 表示不修改渠道级设置；accountProxies 为 nil 表示不修改账号级。
func (g *Gateway) setProviderProxy(providerID string, proxy *string, accountProxies map[string]string) error {
	g.settingsMu.Lock()
	if g.settings.ProviderProxies == nil {
		g.settings.ProviderProxies = map[string]string{}
	}
	if g.settings.AccountProxies == nil {
		g.settings.AccountProxies = map[string]string{}
	}
	if proxy != nil {
		raw := strings.TrimSpace(*proxy)
		if raw != "" {
			raw = resolveProxyInput(raw, g.settings.ProviderProxies[providerID])
			g.settings.ProviderProxies[providerID] = raw
		} else {
			delete(g.settings.ProviderProxies, providerID)
		}
	}
	for user, v := range accountProxies {
		key := accountProxyKey(providerID, user, "")
		raw := strings.TrimSpace(v)
		if raw == "" {
			delete(g.settings.AccountProxies, key)
			continue
		}
		raw = resolveProxyInput(raw, g.settings.AccountProxies[key])
		g.settings.AccountProxies[key] = raw
	}
	saveErr := g.saveSettings()
	g.settingsMu.Unlock()

	for _, env := range g.pool.ListByProvider(providerID) {
		env.SetProxyOverride(g.resolvedAccountProxy(providerID, env.Label, env.AccountID))
		if err := g.rebuildAccountClient(env); err != nil {
			return err
		}
	}
	g.saveAccountsNow()
	return saveErr
}

// rebuildAccountClient 用当前 transport（全局代理）与账号覆盖重建客户端。
func (g *Gateway) rebuildAccountClient(env *provider.AccountEnv) error {
	client, err := g.clientFor(env, env.NetworkPolicy)
	if err != nil {
		return err
	}
	env.SetHTTPClient(client)
	// 重建客户端后重新套上渠道自定义头（否则换代理会把头丢掉）。
	g.applyProviderTransport(env)
	return nil
}
