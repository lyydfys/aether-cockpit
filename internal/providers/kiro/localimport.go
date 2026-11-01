package kiro

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// DiscoverLocalAccounts 从本机 Kiro 账号文件读取登录态。
//
// 目前支持 `.antigravity_cockpit/kiro_accounts.json`（accounts 数组）；
// Kiro CLI 的 SQLite 登录态需要额外解析，未在此实现。
func (p *Provider) DiscoverLocalAccounts(ctx context.Context) ([]*provider.CredentialDraft, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, localImportError(err)
	}
	path := filepath.Join(home, ".antigravity_cockpit", "kiro_accounts.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, localImportError(err)
	}

	var doc struct {
		Accounts []struct {
			AccessToken string `json:"accessToken"`
			ProfileArn  string `json:"profileArn"`
			Region      string `json:"region"`
			Email       string `json:"email"`
			Label       string `json:"label"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, localImportError(err)
	}

	var drafts []*provider.CredentialDraft
	for _, a := range doc.Accounts {
		token := strings.TrimSpace(a.AccessToken)
		arn := strings.TrimSpace(a.ProfileArn)
		if token == "" || arn == "" {
			continue
		}
		label := firstNonEmpty(a.Label, a.Email, "Kiro")
		drafts = append(drafts, &provider.CredentialDraft{
			Label: label,
			Credentials: map[string]string{
				"access_token": token,
				"profile_arn":  arn,
				"region":       strings.TrimSpace(a.Region),
			},
			Source: "local_import:" + path,
		})
	}
	if len(drafts) == 0 {
		return nil, localImportError(nil)
	}
	return drafts, nil
}

func localImportError(cause error) error {
	msg := "没有找到 Kiro 本机登录态。请先登录 Kiro CLI/IDE，或手动填写 access_token、profile_arn 与 region。"
	if cause != nil {
		msg += "（" + cause.Error() + "）"
	}
	return ir.NewAPIError(ir.ErrCodeNoAccount, msg).WithProvider("kiro")
}
