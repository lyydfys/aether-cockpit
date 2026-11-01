package workbuddy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// DiscoverLocalAccounts 从本机 WorkBuddy 桌面端读取登录态。
//
// WorkBuddy 5.6+ 把 accessToken 封进加密信封（suite/ciphertext），本包不做解密，
// 只给出可执行的替代方案。错误信息绝不携带任何凭证内容。
func (p *Provider) DiscoverLocalAccounts(ctx context.Context) ([]*provider.CredentialDraft, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	infoPath, err := localInfoPath(p.profile.infoFile)
	if err != nil {
		return nil, p.localImportUnavailable(err.Error())
	}
	raw, err := os.ReadFile(infoPath)
	if err != nil {
		return nil, p.localImportUnavailable("未找到 " + infoPath)
	}

	draft, parseErr := parseLocalAuth(raw, p.profile, infoPath)
	if parseErr != nil {
		return nil, parseErr
	}
	if draft == nil {
		return nil, p.localImportUnavailable("登录态里没有可用的 accessToken")
	}
	return []*provider.CredentialDraft{draft}, nil
}

// localInfoPath 返回桌面端登录态文件路径（Windows %LOCALAPPDATA%）。
func localInfoPath(infoFile string) (string, error) {
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			base = filepath.Join(home, "AppData", "Local")
		}
	}
	if base == "" {
		return "", &localImportError{message: "当前系统没有 LOCALAPPDATA，无法定位 WorkBuddy 登录态"}
	}
	return filepath.Join(base, "CodeBuddyExtension", "Data", "Public", "auth", infoFile), nil
}

type localImportError struct{ message string }

func (e *localImportError) Error() string { return e.message }

func (p *Provider) localImportUnavailable(reason string) error {
	msg := "没有找到 WorkBuddy 的本机登录态（" + reason + "）。请确认 WorkBuddy 桌面端已登录，或在渠道页手动填写 token / user_id。"
	return ir.NewAPIError(ir.ErrCodeNoAccount, msg).WithProvider(p.profile.id)
}

// parseLocalAuth 解析 workbuddy-desktop*.info。
//
// 返回 (nil, nil) 表示文件可读但没有明文 token（调用方按"未找到"处理）；
// 返回错误表示登录态已被新版客户端加密，需要走手动登录。
func parseLocalAuth(raw []byte, prof profile, source string) (*provider.CredentialDraft, error) {
	var info struct {
		Auth struct {
			AccessToken  json.RawMessage `json:"accessToken"`
			RefreshToken json.RawMessage `json:"refreshToken"`
			TokenType    string          `json:"tokenType"`
			Domain       string          `json:"domain"`
		} `json:"auth"`
		Account struct {
			UID      string          `json:"uid"`
			Nickname json.RawMessage `json:"nickname"`
		} `json:"account"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, nil
	}

	token, _, err := decodeAuthField(info.Auth.AccessToken)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeNoAccount,
			"WorkBuddy 登录态解密失败："+err.Error()).WithProvider(prof.id)
	}
	if token == "" {
		return nil, nil
	}
	refresh, _, _ := decodeAuthField(info.Auth.RefreshToken)

	uid := strings.TrimSpace(info.Account.UID)
	domain := strings.TrimSpace(info.Auth.Domain)
	if domain == "" {
		domain = prof.domain
	}
	label, _, _ := decodeAuthField(info.Account.Nickname)
	if label == "" {
		label = uid
	}
	if label == "" {
		label = "WorkBuddy"
	}

	return &provider.CredentialDraft{
		Label: label,
		Credentials: map[string]string{
			"token":         token,
			"user_id":       uid,
			"domain":        domain,
			"refresh_token": refresh,
		}, Source: "local_import:" + source,
	}, nil
}

// parseAccessToken 区分明文 token 与 5.6+ 加密信封。
// 第二个返回值非空时表示是被封存的登录态，值为 suite 名称。
func parseAccessToken(raw json.RawMessage) (token, sealedSuite string) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", ""
	}
	if strings.HasPrefix(trimmed, `"`) {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return strings.TrimSpace(s), ""
		}
		return "", ""
	}
	if strings.HasPrefix(trimmed, "{") {
		var envelope struct {
			Suite      string `json:"suite"`
			Ciphertext string `json:"ciphertext"`
		}
		_ = json.Unmarshal(raw, &envelope)
		if envelope.Suite != "" {
			return "", envelope.Suite
		}
		if envelope.Ciphertext != "" {
			return "", "unknown"
		}
	}
	return "", ""
}

// decodeAuthField 解出明文字段；5.6+ 加密字段会通过官方客户端绑定取密钥后解密。
func decodeAuthField(raw json.RawMessage) (string, bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", false, nil
	}
	if strings.HasPrefix(trimmed, "{") {
		key, err := loadWBKey()
		if err != nil {
			return "", true, err
		}
		return decryptWBField(raw, key)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s), false, nil
	}
	return "", false, nil
}

func nicknameString(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, `"`) {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
