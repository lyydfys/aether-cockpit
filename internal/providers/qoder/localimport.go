package qoder

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

type desktopAuth struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    string `json:"expiresAt"`
	User         struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"user"`
}

// DiscoverLocalAccounts 从 Qoder 桌面客户端读取登录态。
func (p *Provider) DiscoverLocalAccounts(ctx context.Context) ([]*provider.CredentialDraft, error) {
	auth, source, err := readDesktopAuth(p.profile)
	if err != nil || auth == nil {
		msg := "没有找到 Qoder 桌面登录态。请确认客户端已登录，或在渠道页手动粘贴 token / machine_id。"
		if err != nil {
			msg += "（" + err.Error() + "）"
		}
		return nil, ir.NewAPIError(ir.ErrCodeNoAccount, msg).WithProvider(p.profile.id)
	}
	if strings.TrimSpace(auth.Token) == "" {
		return nil, ir.NewAPIError(ir.ErrCodeNoAccount, "Qoder 桌面登录态里没有 token").
			WithProvider(p.profile.id)
	}
	machineID := p.readMachineID()
	if machineID == "" {
		return nil, ir.NewAPIError(ir.ErrCodeNoAccount, "Qoder 缺少 machine_id（COSY 签名必需）").
			WithProvider(p.profile.id)
	}

	label := strings.TrimSpace(auth.User.Name)
	if label == "" {
		label = p.profile.displayName
	}
	draft := &provider.CredentialDraft{
		Label: label,
		Credentials: map[string]string{
			"token":         strings.TrimSpace(auth.Token),
			"refresh_token": strings.TrimSpace(auth.RefreshToken),
			"user_id":       strings.TrimSpace(auth.User.ID),
			"user_name":     strings.TrimSpace(auth.User.Name),
			"user_email":    strings.TrimSpace(auth.User.Email),
			"machine_id":    machineID,
			"expires_at":    strings.TrimSpace(auth.ExpiresAt),
		},
		Source: source,
	}
	if t, err := time.Parse(time.RFC3339Nano, auth.ExpiresAt); err == nil {
		draft.ExpiresAt = t.Unix()
	}
	return []*provider.CredentialDraft{draft}, nil
}

// parseDesktopAuthJSON 是测试与 CLI 明文格式共用的解析入口。
func parseDesktopAuthJSON(raw []byte) (*desktopAuth, error) {
	var auth desktopAuth
	if err := json.Unmarshal(raw, &auth); err != nil {
		return nil, err
	}
	if strings.TrimSpace(auth.Token) == "" {
		return nil, ir.NewAPIError(ir.ErrCodeNoAccount, "登录态缺少 token")
	}
	return &auth, nil
}
