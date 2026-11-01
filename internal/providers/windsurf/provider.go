// Package windsurf 提供 Windsurf / Devin 平台的账号接入。
//
// 登录是邮箱密码 + seat-management protobuf 链路（PostAuth → OTT → RegisterUser），
// 产出 auth1_token / session_token / ide_token / account_id / org_id。
// 对话与额度协议尚未验证，暂不声明 Chat/Quota。
package windsurf

import (
	"context"
	"fmt"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

const channelID = "windsurf"

func init() {
	provider.Register(&Provider{})
}

// Provider 实现 Windsurf / Devin 渠道。
type Provider struct{}

func (p *Provider) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID:            channelID,
		DisplayName:   "Windsurf / Devin",
		Family:        "windsurf",
		RegionTag:     "GLOBAL",
		BrandColor:    "#0EA5E9",
		Description:   "Windsurf / Devin：支持邮箱密码登录（seat-management 四步链）或粘贴 auth1_token；可刷新会话与 IDE token。对话/额度协议尚未接线。",
		NetworkPolicy: provider.NetPolicyProxyFirst,
		SupportedAuths: []provider.AuthMethod{
			provider.AuthManualInput,
		},
		AuthFormSchema: []provider.FormFieldSchema{
			{Key: "auth1_token", Label: "Auth1 Token", Type: "password", Required: true, Placeholder: "auth1_...", Help: "长期凭证；邮箱密码登录会自动写入"},
			{Key: "email", Label: "邮箱（可选）", Type: "text"},
		},
		Capabilities: provider.CapabilitySet{
			Refresh: true,
		},
	}
}

// RefreshCredential 用 auth1_token 重跑 PostAuth → OTT → RegisterUser，
// 更新 session/ide token；供手动恢复与周期保活使用。
func (p *Provider) RefreshCredential(ctx context.Context, env *provider.AccountEnv) (*provider.CredentialDraft, error) {
	auth1 := strings.TrimSpace(env.GetCredential("auth1_token"))
	if auth1 == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Windsurf 账号缺少 auth1_token").WithProvider(channelID)
	}
	res, err := FullRefresh(ctx, auth1)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, err.Error()).WithProvider(channelID)
	}
	creds := map[string]string{
		"auth1_token":   res.Auth1Token,
		"session_token": res.SessionToken,
		"ide_token":     res.IDEToken,
		"account_id":    res.AccountID,
		"org_id":        res.OrgID,
	}
	if email := strings.TrimSpace(env.GetCredential("email")); email != "" {
		creds["email"] = email
	}
	return &provider.CredentialDraft{Credentials: creds, Source: "windsurf_refresh"}, nil
}

// ValidateAuth1 校验长期凭证格式。
func ValidateAuth1(v string) error {
	if !strings.HasPrefix(strings.TrimSpace(v), "auth1_") {
		return fmt.Errorf("auth1_token 应以 auth1_ 开头")
	}
	return nil
}

var (
	_ provider.BaseProvider           = (*Provider)(nil)
	_ provider.TokenRefreshCapability = (*Provider)(nil)
)
