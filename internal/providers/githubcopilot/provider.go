// Package githubcopilot 提供 GitHub Copilot 渠道的账号接入与额度读取。
//
// 目标对齐 local-vault：设备码/本机登录态/手动 GitHub Token 三种接入，
// 账号卡展示 Copilot 的 chat / completions / premium interactions 额度。
// 对话转发不在此包：Copilot 的 chat 端点需要另一套 token 与请求头，
// 未验证前不提供 Chat 能力，避免界面宣称可用但实际 401。
package githubcopilot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

const channelID = "github-copilot"

// copilotUserURL 可被测试替换。
var copilotUserURL = "https://api.github.com/copilot_internal/user"

// githubDeviceCodeURL / githubTokenURL 是 VS Code 公开 GitHub OAuth 应用的设备码端点。
const (
	githubDeviceCodeURL = "https://github.com/login/device/code"
	githubTokenURL      = "https://github.com/login/oauth/access_token"
	githubClientID      = "Iv1.b507a08c87ecfe98"
)

func init() {
	provider.Register(&Provider{})
}

// Provider 实现 GitHub Copilot 渠道。
type Provider struct{}

var manifestModels = []ir.ModelMeta{
	{ID: channelID + "/gpt-4o", UpstreamModel: "gpt-4o", DisplayName: "GPT-4o (Copilot)", ContextWindow: 128000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Streaming: true}},
	{ID: channelID + "/gpt-4.1", UpstreamModel: "gpt-4.1", DisplayName: "GPT-4.1 (Copilot)", ContextWindow: 128000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Streaming: true}},
	{ID: channelID + "/claude-sonnet-4", UpstreamModel: "claude-sonnet-4", DisplayName: "Claude Sonnet 4 (Copilot)", ContextWindow: 200000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Streaming: true, Reasoning: true}},
}

func (p *Provider) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID:            channelID,
		DisplayName:   "GitHub Copilot",
		Family:        "copilot",
		RegionTag:     "GLOBAL",
		BrandColor:    "#24292F",
		Description:   "GitHub Copilot 订阅：支持 GitHub 设备码登录、读取本机 github-copilot 登录态、手动粘贴 GitHub Token；可读 chat / completions / premium interactions 额度。对话转发尚未接线（避免假可用）。",
		NetworkPolicy: provider.NetPolicyProxyFirst,
		SupportedAuths: []provider.AuthMethod{
			provider.AuthLocalImport,
			provider.AuthDeviceCode,
			provider.AuthManualInput,
		},
		OAuth: &provider.OAuthConfig{
			DeviceCodeURL: githubDeviceCodeURL,
			TokenURL:      githubTokenURL,
			ClientID:      githubClientID,
			Scopes:        []string{"read:user", "user:email"},
		},
		AuthFormSchema: []provider.FormFieldSchema{
			{
				Key: "api_key", Label: "GitHub Token", Type: "password", Required: true,
				Placeholder: "ghu_... / github_pat_...",
				Help:        "用于读取 Copilot 额度；设备码登录会自动写入",
			},
		},
		Models: manifestModels,
		Capabilities: provider.CapabilitySet{
			Chat:        true,
			LocalImport: true,
			LocalInject: true,
			Quota:       true,
			Refresh:     true,
		},
	}
}

// DiscoverLocalAccounts 读取 Copilot CLI/Vim 的 hosts.json / apps.json。
func (p *Provider) DiscoverLocalAccounts(ctx context.Context) ([]*provider.CredentialDraft, error) {
	var out []*provider.CredentialDraft
	seen := map[string]bool{}
	for _, path := range copilotHostsPaths() {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, d := range parseCopilotHosts(raw) {
			if seen[d.Label] {
				continue
			}
			seen[d.Label] = true
			d.Source = "local_import:" + path
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeNoAccount, "没有找到 GitHub Copilot 的本机登录态；请在 Copilot CLI 登录，或使用设备码/手动 Token").WithProvider(channelID)
	}
	return out, nil
}

func copilotHostsPaths() []string {
	home, _ := os.UserHomeDir()
	paths := []string{
		filepath.Join(home, ".config", "github-copilot", "hosts.json"),
		filepath.Join(home, ".config", "github-copilot", "apps.json"),
	}
	for _, env := range []string{"APPDATA", "LOCALAPPDATA"} {
		if dir := os.Getenv(env); dir != "" {
			paths = append(paths,
				filepath.Join(dir, "github-copilot", "hosts.json"),
				filepath.Join(dir, "github-copilot", "apps.json"))
		}
	}
	return paths
}

// parseCopilotHosts 兼容 {"github.com":{"user":"x","oauth_token":"ghu_.."}} 与
// 顶层/嵌套任意位置出现 oauth_token 的形态。
func parseCopilotHosts(raw []byte) []*provider.CredentialDraft {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	var out []*provider.CredentialDraft
	var walk func(v any, host string)
	walk = func(v any, host string) {
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		tok, _ := m["oauth_token"].(string)
		user, _ := m["user"].(string)
		if strings.TrimSpace(tok) != "" {
			label := "GitHub Copilot"
			if host != "" {
				label += " (" + host + ")"
			}
			if strings.TrimSpace(user) != "" {
				label += " · " + strings.TrimSpace(user)
			}
			out = append(out, &provider.CredentialDraft{
				Label: label,
				Credentials: map[string]string{
					"api_key":      strings.TrimSpace(tok),
					"github_token": strings.TrimSpace(tok),
					"base_url":     "https://api.githubcopilot.com",
				},
			})
			return
		}
		for k, child := range m {
			walk(child, k)
		}
	}
	walk(root, "github.com")
	return out
}

type copilotSnapshot struct {
	Entitlement      float64 `json:"entitlement"`
	Remaining        float64 `json:"remaining"`
	PercentRemaining float64 `json:"percent_remaining"`
	Unlimited        bool    `json:"unlimited"`
	QuotaID          string  `json:"quota_id"`
}

type copilotUser struct {
	CopilotPlan    string                     `json:"copilot_plan"`
	ChatEnabled    *bool                      `json:"chat_enabled"`
	QuotaSnapshots map[string]copilotSnapshot `json:"quota_snapshots"`
	QuotaResetDate string                     `json:"quota_reset_date"`
	LimitedQuotas  map[string]json.RawMessage `json:"limited_user_quotas"`
}

// QueryQuota 读取 /copilot_internal/user 并映射成统一额度窗口。
func (p *Provider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	token := strings.TrimSpace(env.GetCredential("github_token"))
	if token == "" {
		token = strings.TrimSpace(env.GetCredential("api_key"))
	}
	if token == "" {
		token = strings.TrimSpace(env.GetCredential("access_token"))
	}
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Copilot 账号缺少 GitHub Token").WithProvider(channelID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, copilotUserURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2025-04-01")
	req.Header.Set("User-Agent", "AetherCockpit")
	resp, err := env.Client().Do(req)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "读取 Copilot 额度失败: "+err.Error()).
			WithProvider(channelID).WithRetryable(true)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "Copilot 额度接口返回 %d: %s", resp.StatusCode, msg).WithProvider(channelID)
	}
	var u copilotUser
	if err := json.Unmarshal(raw, &u); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Copilot 额度响应不是合法 JSON").WithProvider(channelID)
	}

	resetAt := int64(0)
	if t, err := time.Parse("2006-01-02", strings.TrimSpace(u.QuotaResetDate)); err == nil {
		resetAt = t.UTC().Unix()
	}
	order := []string{"premium_interactions", "chat", "completions"}
	names := map[string]string{
		"premium_interactions": "Premium requests",
		"chat":                 "Chat",
		"completions":          "Completions",
	}
	status := &provider.QuotaStatus{Plan: u.CopilotPlan, Unit: "次", ReadAt: time.Now().Unix()}
	for _, id := range order {
		s, ok := u.QuotaSnapshots[id]
		if !ok || s.Unlimited {
			continue
		}
		remainingPct := s.PercentRemaining
		if remainingPct == 0 && s.Entitlement > 0 {
			remainingPct = s.Remaining / s.Entitlement * 100
		}
		usedPct := 100 - remainingPct
		if usedPct < 0 {
			usedPct = 0
		}
		if usedPct > 100 {
			usedPct = 100
		}
		w := provider.QuotaWindow{
			Name: names[id], Used: usedPct,
			Amount: int64(s.Entitlement - s.Remaining), Limit: int64(s.Entitlement),
			Unit: "次", ResetsAt: resetAt,
		}
		status.Windows = append(status.Windows, w)
		if id == "premium_interactions" {
			status.Total = int(s.Entitlement)
			status.Remaining = int(s.Remaining)
			status.Used = int(s.Entitlement - s.Remaining)
		}
	}
	if status.Total == 0 && len(u.QuotaSnapshots) > 0 {
		// 没有 premium 口径时用第一个可读窗口兜底。
		keys := make([]string, 0, len(u.QuotaSnapshots))
		for k, s := range u.QuotaSnapshots {
			if !s.Unlimited {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			s := u.QuotaSnapshots[keys[0]]
			status.Total = int(s.Entitlement)
			status.Remaining = int(s.Remaining)
			status.Used = int(s.Entitlement - s.Remaining)
		}
	}
	if u.ChatEnabled != nil && !*u.ChatEnabled {
		status.Message = "该账号未开通 Copilot Chat"
	}
	if len(status.Windows) == 0 {
		status.Message = "额度接口没有返回可读窗口"
	}
	return status, nil
}

var (
	_ provider.BaseProvider          = (*Provider)(nil)
	_ provider.LocalImportCapability = (*Provider)(nil)
	_ provider.QuotaCapability       = (*Provider)(nil)
)
