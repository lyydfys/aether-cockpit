package githubcopilot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/reverse"
)

// Copilot 对话需要把 GitHub OAuth token 换成短期 Copilot token；
// 协议转换复用 reverse 引擎，本文件只负责 token 交换与请求头。
var copilotTokenURL = "https://api.github.com/copilot_internal/v2/token"

var (
	copilotOnce   sync.Once
	copilotEngine *reverse.Provider
)

func engine() *reverse.Provider {
	copilotOnce.Do(func() {
		copilotEngine = reverse.New(reverse.Profile{
			ID:              channelID,
			DefaultBaseURL:  "https://api.githubcopilot.com",
			DefaultChatPath: "/chat/completions",
			Headers: map[string]string{
				// 版本组合取自 Copilot Chat 扩展的真实抓包（0.35.0 / VS Code 1.107）。
				// 旧值 0.26.7 / 1.99.0 已经落后真实客户端好几个版本，容易被按
				// "过旧客户端"归档。
				"Copilot-Integration-Id": "vscode-chat",
				"Editor-Version":         copilotEditorVersion,
				"Editor-Plugin-Version":  "copilot-chat/" + copilotChatVersion,
				"User-Agent":             "GitHubCopilotChat/" + copilotChatVersion,
			},
			// X-Initiator 不是固定值：用户发起是 user，工具续轮是 agent。
			DynamicHeaders: func(req *ir.UnifiedChatRequest) map[string]string {
				h := map[string]string{"X-Initiator": copilotInitiator(req)}
				if copilotHasImages(req) {
					h["Copilot-Vision-Request"] = "true"
				}
				return h
			},
		})
	})
	return copilotEngine
}

// ChatStream 确保 Copilot token 有效后，交给 reverse 引擎做 IR→OpenAI 转换。
func (p *Provider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	if err := ensureCopilotToken(ctx, env); err != nil {
		return nil, err
	}
	return engine().ChatStream(ctx, env, req)
}

// ensureCopilotToken 用 github_token 换 Copilot token，缓存到 access_token/expires_at。
func ensureCopilotToken(ctx context.Context, env *provider.AccountEnv) error {
	access := strings.TrimSpace(env.GetCredential("access_token"))
	exp, _ := strconv.ParseInt(strings.TrimSpace(env.GetCredential("expires_at")), 10, 64)
	if access != "" && exp > time.Now().Add(60*time.Second).Unix() {
		return nil
	}
	gh := strings.TrimSpace(env.GetCredential("github_token"))
	if gh == "" {
		gh = strings.TrimSpace(env.GetCredential("api_key"))
	}
	if gh == "" {
		if access != "" {
			return nil
		}
		return ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Copilot 账号缺少 GitHub Token").WithProvider(channelID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, copilotTokenURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+gh)
	req.Header.Set("Accept", "application/json")
	// 与对话请求用同一组版本号：同一账号在 token 交换与对话上出现两套
	// 客户端版本，是最容易被关联的破绽。
	req.Header.Set("Editor-Version", copilotEditorVersion)
	req.Header.Set("Editor-Plugin-Version", "copilot-chat/"+copilotChatVersion)
	req.Header.Set("Copilot-Integration-Id", "vscode-chat")
	req.Header.Set("User-Agent", "GitHubCopilotChat/"+copilotChatVersion)
	client := env.Client()
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Copilot token 交换失败: "+err.Error()).
			WithProvider(channelID).WithRetryable(true)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "Copilot token 交换返回 %d: %s", resp.StatusCode, msg).WithProvider(channelID)
	}
	var tok struct {
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil || strings.TrimSpace(tok.Token) == "" {
		return ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Copilot token 响应里没有 token").WithProvider(channelID)
	}
	env.SetCredential("access_token", strings.TrimSpace(tok.Token))
	if tok.ExpiresAt > 0 {
		env.SetCredential("expires_at", strconv.FormatInt(tok.ExpiresAt, 10))
	}
	return nil
}

// RefreshCredential 手动/周期刷新时也走同一套 token 交换。
func (p *Provider) RefreshCredential(ctx context.Context, env *provider.AccountEnv) (*provider.CredentialDraft, error) {
	if err := ensureCopilotToken(ctx, env); err != nil {
		return nil, err
	}
	creds := map[string]string{
		"access_token": env.GetCredential("access_token"),
	}
	if exp := env.GetCredential("expires_at"); exp != "" {
		creds["expires_at"] = exp
	}
	return &provider.CredentialDraft{Credentials: creds, Source: "copilot_token_exchange"}, nil
}

var (
	_ provider.ChatCapability         = (*Provider)(nil)
	_ provider.TokenRefreshCapability = (*Provider)(nil)
)
