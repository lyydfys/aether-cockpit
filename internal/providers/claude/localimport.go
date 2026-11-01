package claude

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// DiscoverLocalAccounts 扫描本机 Claude Code CLI 的 OAuth 登录态。
//
// Claude Code（订阅登录）把凭证落在 ~/.claude/.credentials.json：
//
//	{ "claudeAiOauth": { "accessToken": "...", "refreshToken": "...",
//	                    "expiresAt": 1730000000000, "scopes": [...] } }
//
// 注意平台差异：macOS 存 Keychain（"Claude Code-credentials"），本包不读；
// Windows / Linux 均为上述文件。文件不存在或结构不符时返回可执行的提示，
// 错误信息绝不携带任何凭证内容。
//
// 替代路径：本机没有 credentials.json（例如 Claude Code 未登录或登录在
// 别的机器）时，可在渠道表单手动粘贴浏览器 claude.ai 的 sessionKey Cookie
// （credentials.session_key），由 sessionkey.go 的三步流自动换取 OAuth
// 凭证 —— 这是本机扫描之外唯一不依赖 credentials.json 的接入方式。
func (p *Provider) DiscoverLocalAccounts(ctx context.Context) ([]*provider.CredentialDraft, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	path, err := credentialsPath()
	if err != nil {
		return nil, localImportUnavailable(err.Error())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, localImportUnavailable("未找到 " + path)
	}

	draft := parseCredentialsFile(raw)
	if draft == nil {
		// 结构不符时把顶层键名打到日志（只有键名，没有值），便于排查
		// Claude Code 版本升级导致的字段变化。
		log.Printf("[CLAUDE] %s 结构无法识别，顶层键：%v", path, topLevelKeys(raw))
		return nil, localImportUnavailable(path + " 里没有可用的 claudeAiOauth 登录态")
	}
	draft.Source = "local_import:" + path
	return []*provider.CredentialDraft{draft}, nil
}

// credentialsPath 返回登录态文件路径。
// Claude Code 支持用 CLAUDE_CONFIG_DIR 把配置目录迁走，优先尊重它。
func credentialsPath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, ".credentials.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", &localImportError{message: "当前系统无法定位用户主目录，找不到 ~/.claude"}
	}
	return filepath.Join(home, ".claude", ".credentials.json"), nil
}

type localImportError struct{ message string }

func (e *localImportError) Error() string { return e.message }

func localImportUnavailable(reason string) error {
	msg := "没有找到 Claude Code 的本机订阅登录态（" + reason + "）。" +
		"请确认本机 Claude Code 已用订阅账号登录（/login），或在渠道页手动粘贴 access_token / refresh_token。"
	return ir.NewAPIError(ir.ErrCodeNoAccount, msg).WithProvider(channelID)
}

// topLevelKeys 返回 JSON 的顶层键名列表（仅键名，用于日志诊断）。
func topLevelKeys(raw []byte) []string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// parseCredentialsFile 解析 Claude Code 的 credentials.json。
//
// 官方字段是 camelCase（accessToken / refreshToken / expiresAt，毫秒时间戳）；
// 同时兼容 snake_case 与"顶层就是 oauth 对象"两种历史形态，避免字段改名
// 或三方改造过的文件导入失败。返回 nil 表示没有可用的订阅凭证。
func parseCredentialsFile(raw []byte) *provider.CredentialDraft {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil
	}

	oauthRaw, ok := top["claudeAiOauth"]
	if !ok {
		// 容错：部分三方工具会把 oauth 对象直接写在顶层。
		if _, hasToken := top["accessToken"]; hasToken {
			oauthRaw, ok = json.RawMessage(raw), true
		} else if _, hasToken := top["access_token"]; hasToken {
			oauthRaw, ok = json.RawMessage(raw), true
		}
	}
	if !ok {
		return nil
	}

	var oauth struct {
		AccessToken  string          `json:"accessToken"`
		RefreshToken string          `json:"refreshToken"`
		ExpiresAt    json.RawMessage `json:"expiresAt"`
		ClientID     string          `json:"client_id"`
		// snake_case 兼容形态（旧版 / 三方改造过的文件）。
		AccessTokenAlt  string          `json:"access_token"`
		RefreshTokenAlt string          `json:"refresh_token"`
		ExpiresAtAlt    json.RawMessage `json:"expires_at"`
		ClientIDAlt     string          `json:"clientId"`
		Scopes          []string        `json:"scopes"`
	}
	if err := json.Unmarshal(oauthRaw, &oauth); err != nil {
		return nil
	}

	accessToken := strings.TrimSpace(firstNonEmpty(oauth.AccessToken, oauth.AccessTokenAlt))
	if accessToken == "" {
		return nil
	}
	refreshToken := strings.TrimSpace(firstNonEmpty(oauth.RefreshToken, oauth.RefreshTokenAlt))
	clientID := strings.TrimSpace(firstNonEmpty(oauth.ClientID, oauth.ClientIDAlt))
	expiresAt := parseExpiry(firstNonEmpty(
		jsonString(oauth.ExpiresAt),
		jsonString(oauth.ExpiresAtAlt),
	))

	draft := &provider.CredentialDraft{
		Label: "Claude 订阅 (本机)",
		Credentials: map[string]string{
			"access_token":  accessToken,
			"refresh_token": refreshToken,
		},
	}
	if !expiresAt.IsZero() {
		// 统一写成 RFC3339：网关的 parseUnixValue 与本包 parseExpiry 都认它。
		draft.Credentials["expires_at"] = expiresAt.Format(time.RFC3339)
		draft.ExpiresAt = expiresAt.Unix()
	}
	if clientID != "" {
		// 官方文件一般不带 client_id；带了就带上，刷新时优先生效。
		draft.Credentials["oauth_client_id"] = clientID
	}
	return draft
}

// jsonString 把 JSON 值（数字或字符串）摊平成普通字符串，空值返回 ""。
func jsonString(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return trimmed
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
