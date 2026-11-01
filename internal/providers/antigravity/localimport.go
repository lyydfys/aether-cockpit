package antigravity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// DiscoverLocalAccounts 从本机 Antigravity 客户端读取 OAuth 登录态。
//
// Windows 上官方客户端把 token 写在凭据管理器 target=gemini:antigravity；
// 其他平台暂不支持读取，返回可执行的提示。
func (p *Provider) DiscoverLocalAccounts(ctx context.Context) ([]*provider.CredentialDraft, error) {
	// 两个来源，与 cockpit-tools / sub2api 一致：
	//  1. 官方客户端的系统凭据（Windows 凭据管理器 target=gemini:antigravity）；
	//  2. 独立版（Standalone 2.x）在 Keyring 不可用时的回退文件
	//     ~/.gemini/jetski-standalone-oauth-token。
	var lastErr error
	if raw, err := readLocalCredential(); err == nil && len(raw) > 0 {
		if draft := parseLocalCredential(raw); draft != nil {
			return []*provider.CredentialDraft{draft}, nil
		}
	} else if err != nil {
		lastErr = err
	}
	if raw, err := readFallbackTokenFile(); err == nil && len(raw) > 0 {
		if draft := parseLocalCredential(raw); draft != nil {
			draft.Source = "local_import:file:~/.gemini/jetski-standalone-oauth-token"
			return []*provider.CredentialDraft{draft}, nil
		}
	}
	return nil, localImportUnavailable(lastErr)
}

// readFallbackTokenFile 读取独立版 Antigravity 的回退凭据文件。
func readFallbackTokenFile() ([]byte, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(home, ".gemini", "jetski-standalone-oauth-token"))
}

// parseLocalCredential 解析官方客户端写入凭据管理器的 JSON。
func parseLocalCredential(raw []byte) *provider.CredentialDraft {
	text := strings.TrimSpace(string(raw))
	// macOS / Linux 的 Keyring 有时以 go-keyring-base64: 前缀保存。
	if strings.HasPrefix(text, "go-keyring-base64:") {
		if decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(text, "go-keyring-base64:")); err == nil {
			text = string(decoded)
		}
	}
	var blob struct {
		Token struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			TokenType    string `json:"token_type"`
			Expiry       string `json:"expiry"`
		} `json:"token"`
		AuthMethod string `json:"auth_method"`
		IDToken    string `json:"id_token"`
	}
	if err := json.Unmarshal([]byte(text), &blob); err != nil {
		return nil
	}
	if strings.TrimSpace(blob.Token.AccessToken) == "" {
		return nil
	}
	label := "Antigravity (本机)"
	if email := emailFromIDToken(blob.IDToken); email != "" {
		label = "Antigravity (" + email + ")"
	}
	draft := &provider.CredentialDraft{
		Label: label,
		Credentials: map[string]string{
			"access_token":  strings.TrimSpace(blob.Token.AccessToken),
			"refresh_token": strings.TrimSpace(blob.Token.RefreshToken),
			// id_token 既是账号标识（email），也是 cockpit-tools 一键切号
			// 写回 IDE 时的必备字段；导入时保住它，切号才不用重新登录。
			"id_token":   strings.TrimSpace(blob.IDToken),
			"token_type": strings.TrimSpace(blob.Token.TokenType),
		},
		Source: "local_import:windows-credential:gemini:antigravity",
	}
	if draft.Credentials["token_type"] == "" {
		draft.Credentials["token_type"] = "Bearer"
	}
	if expiry := parseExpiry(blob.Token.Expiry); !expiry.IsZero() {
		draft.Credentials["expires_at"] = expiry.Format("2006-01-02T15:04:05Z07:00")
		draft.ExpiresAt = expiry.Unix()
	}
	return draft
}

// localImportUnavailable 把"本机没有可读登录态"转成可执行的提示。
func localImportUnavailable(cause error) error {
	msg := "没有找到 Antigravity 的本机登录态。请确认官方客户端已登录，或在渠道页手动粘贴 access_token / refresh_token。"
	if cause != nil {
		msg += "（" + cause.Error() + "）"
	}
	return ir.NewAPIError(ir.ErrCodeNoAccount, msg).WithProvider("antigravity")
}

// emailFromIDToken 从 OIDC id_token 的 payload 里读 email（不校验签名，
// 只用于给账号起一个好认的名字）。
func emailFromIDToken(idToken string) string {
	parts := strings.Split(strings.TrimSpace(idToken), ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return strings.TrimSpace(claims.Email)
}
