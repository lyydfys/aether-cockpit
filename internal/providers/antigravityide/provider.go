// Package antigravityide 把 Antigravity IDE 暴露为独立渠道。
//
// Antigravity IDE 与 Antigravity 共用本机 OAuth 登录态（Windows 凭据
// target=gemini:antigravity / jetski 回退文件），因此这里复用 antigravity
// 的本地导入、对话与额度实现，只替换渠道标识与展示名。
package antigravityide

import (
	"context"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/antigravity"
)

func init() {
	provider.Register(&Provider{})
}

// Provider 包装 antigravity 的实现。
type Provider struct {
	antigravity.Provider
}

// Manifest 使用独立 ID/展示名，模型前缀同步替换。
func (p *Provider) Manifest() provider.ProviderManifest {
	m := p.Provider.Manifest()
	m.ID = "antigravity-ide"
	m.DisplayName = "Antigravity IDE"
	m.Family = "antigravity"
	m.BrandColor = "#1A73E8"
	m.Description = "Antigravity IDE：与 Antigravity 共用本机 Google 登录态（gemini:antigravity），支持本机导入、浏览器授权、额度与对话。"
	// 深拷贝：antigravity 的 Manifest 与全局 model 清单共享切片，直接改会污染原渠道。
	models := make([]ir.ModelMeta, len(m.Models))
	copy(models, m.Models)
	for i := range models {
		models[i].ID = strings.Replace(models[i].ID, "antigravity/", "antigravity-ide/", 1)
	}
	m.Models = models
	return m
}

// DiscoverLocalAccounts 复用扫描并改写展示名，避免与 Antigravity 卡片混淆。
func (p *Provider) DiscoverLocalAccounts(ctx context.Context) ([]*provider.CredentialDraft, error) {
	drafts, err := p.Provider.DiscoverLocalAccounts(ctx)
	for _, d := range drafts {
		if d != nil {
			d.Label = strings.Replace(d.Label, "Antigravity", "Antigravity IDE", 1)
		}
	}
	return drafts, err
}

var (
	_ provider.BaseProvider          = (*Provider)(nil)
	_ provider.ChatCapability        = (*Provider)(nil)
	_ provider.LocalImportCapability = (*Provider)(nil)
	_ provider.QuotaCapability       = (*Provider)(nil)
)
