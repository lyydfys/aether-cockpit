// Package vertex 实现 Google Vertex AI 渠道：用 gcloud 的 Application
// Default Credentials（authorized_user）换 access_token，再调用
// generateContent / streamGenerateContent。请求体与 SSE 复用 Antigravity
// 的 Gemini 翻译（antigravity.GeminiBody / ParseGeminiSSE）。
package vertex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/antigravity"
)

const (
	channelID   = "vertex"
	oauthToken  = "https://oauth2.googleapis.com/token"
	apiRootHost = "aiplatform.googleapis.com"
)

func init() {
	provider.Register(&Provider{})
}

// Provider 实现 Google Vertex AI 渠道。
type Provider struct{}

func (p *Provider) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID: channelID, DisplayName: "Google Vertex AI", Family: "vertex", RegionTag: "GLOBAL", BrandColor: "#4285F4",
		Description:   "Google Vertex AI：使用 gcloud Application Default Credentials（authorized_user）或 service_account JSON 访问你的 Google Cloud 项目；项目与位置在供应商编辑器里填写。",
		NetworkPolicy: provider.NetPolicyProxyFirst,
		SupportedAuths: []provider.AuthMethod{
			provider.AuthManualInput,
		},
		AuthFormSchema: []provider.FormFieldSchema{
			{Key: "project", Label: "Project ID", Type: "text", Required: true,
				Help: "启用 aiplatform.googleapis.com 的 Google Cloud 项目"},
			{Key: "location", Label: "Location", Type: "text", Placeholder: "global",
				Help: "global / us / eu 或具体区域（如 us-central1）"},
			{Key: "credentials", Label: "ADC / Service Account 文件", Type: "text",
				Placeholder: "~/.config/gcloud/application_default_credentials.json",
				Help:        "留空时读默认 ADC；也可填 service_account JSON 的路径"},
		},
		Models: []ir.ModelMeta{
			{ID: "vertex/gemini-2.5-pro", UpstreamModel: "gemini-2.5-pro", DisplayName: "Gemini 2.5 Pro", ContextWindow: 1048576,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Vision: true, Streaming: true}},
			{ID: "vertex/gemini-2.5-flash", UpstreamModel: "gemini-2.5-flash", DisplayName: "Gemini 2.5 Flash", ContextWindow: 1048576,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Vision: true, Streaming: true}},
			{ID: "vertex/gemini-2.0-flash", UpstreamModel: "gemini-2.0-flash", DisplayName: "Gemini 2.0 Flash", ContextWindow: 1048576,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Vision: true, Streaming: true}},
		},
		Capabilities: provider.CapabilitySet{Chat: true},
	}
}

// ADCFile 是 gcloud 凭据的两种形态：
//   - authorized_user：个人 ADC（refresh_token）
//   - service_account：服务账号（private_key / client_email / project_id）
type ADCFile struct {
	Type         string `json:"type"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
	TokenURI     string `json:"token_uri"`
	ProjectID    string `json:"project_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
}

// ParseADC 解析并校验 ADC 文件，支持 authorized_user 与 service_account。
func ParseADC(raw []byte) (*ADCFile, error) {
	var f ADCFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	switch strings.TrimSpace(f.Type) {
	case "authorized_user":
		if f.RefreshToken == "" || f.ClientID == "" || f.ClientSecret == "" {
			return nil, errNotAuthorizedUser
		}
	case "service_account":
		if strings.TrimSpace(f.PrivateKey) == "" || strings.TrimSpace(f.ClientEmail) == "" {
			return nil, errNotServiceAccount
		}
	default:
		return nil, errUnsupportedADC
	}
	if strings.TrimSpace(f.TokenURI) == "" {
		f.TokenURI = oauthToken
	}
	return &f, nil
}

var (
	errNotAuthorizedUser = &adcError{"authorized_user 凭据缺少 refresh_token/client_id/client_secret"}
	errNotServiceAccount = &adcError{"service_account 凭据缺少 private_key/client_email"}
	errUnsupportedADC    = &adcError{"仅支持 gcloud ADC 的 authorized_user 或 service_account 形态"}
)

type adcError struct{ msg string }

func (e *adcError) Error() string { return e.msg }

// ReadADC 读凭据文件（支持 ~ 展开）；path 为空时用 gcloud 默认路径。
func ReadADC(path string) ([]byte, string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, "", err
		}
		p = filepath.Join(home, ".config", "gcloud", "application_default_credentials.json")
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, "", err
		}
		p = filepath.Join(home, p[2:])
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, p, err
	}
	if len(raw) > 1<<20 {
		return nil, p, &adcError{"ADC 文件过大"}
	}
	return raw, p, nil
}

func (p *Provider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	token, err := ensureToken(ctx, env)
	if err != nil {
		return nil, err
	}
	project := strings.TrimSpace(env.GetCredential("project"))
	if project == "" {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "Vertex 账号缺少 project（service_account 文件里也没有 project_id）").WithProvider(channelID)
	}
	location := strings.TrimSpace(env.GetCredential("location"))
	if location == "" {
		location = "global"
	}
	model := strings.TrimSpace(req.UpstreamModel)
	if model == "" {
		model = strings.TrimPrefix(strings.TrimSpace(req.Model), channelID+"/")
	}
	if model == "" {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "Vertex 缺少模型名").WithProvider(channelID)
	}
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		host := apiRootHost
		switch location {
		case "global":
			host = apiRootHost
		case "us", "eu":
			host = "aiplatform." + location + ".rep.googleapis.com"
		default:
			host = location + "-aiplatform.googleapis.com"
		}
		base = "https://" + host
	}
	endpoint := base + "/v1/projects/" + url.PathEscape(project) + "/locations/" + url.PathEscape(location) +
		"/publishers/google/models/" + url.PathEscape(model) + ":streamGenerateContent?alt=sse"
	payload, err := json.Marshal(antigravity.GeminiBody(req, model))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "Vertex 请求序列化失败").WithCause(err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 Vertex 请求失败").WithCause(err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("User-Agent", "google-cloud-vertex/1.0.0")

	resp, err := env.Client().Do(httpReq)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Vertex 上游连接失败").
			WithCause(err).WithProvider(channelID).WithRetryable(true)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return nil, mapUpstreamError(resp.StatusCode, string(raw), resp.Header)
	}
	out := make(chan ir.UnifiedStreamEvent, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		antigravity.ParseGeminiSSE(ctx, out, resp.Body)
	}()
	return out, nil
}

// ensureToken 复用缓存 access_token；否则用 ADC refresh_token 换新并写回账号。
func ensureToken(ctx context.Context, env *provider.AccountEnv) (string, error) {
	if tok := strings.TrimSpace(env.GetCredential("access_token")); tok != "" {
		exp := strings.TrimSpace(env.GetCredential("expires_at"))
		if exp == "" {
			return tok, nil
		}
		if t, err := time.Parse(time.RFC3339, exp); err == nil && time.Now().Before(t.Add(-60*time.Second)) {
			return tok, nil
		}
	}
	rawADC := strings.TrimSpace(env.GetCredential("adc_json"))
	if rawADC == "" {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Vertex 账号缺少 ADC 凭据；请在供应商编辑器里填写凭据文件").WithProvider(channelID)
	}
	adc, err := ParseADC([]byte(rawADC))
	if err != nil {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth, err.Error()).WithProvider(channelID)
	}
	if adc.Type == "service_account" {
		if strings.TrimSpace(env.GetCredential("project")) == "" && adc.ProjectID != "" {
			env.SetCredential("project", adc.ProjectID)
		}
		return serviceAccountToken(ctx, env, adc)
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", adc.ClientID)
	form.Set("client_secret", adc.ClientSecret)
	form.Set("refresh_token", adc.RefreshToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, adc.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", ir.NewAPIError(ir.ErrCodeInternal, "构造 Vertex 令牌请求失败").WithCause(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := env.Client().Do(req)
	if err != nil {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Vertex 令牌刷新失败（需要 oauth2.googleapis.com 网络/代理）").
			WithCause(err).WithProvider(channelID).WithRetryable(true)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "Vertex 令牌刷新失败：HTTP %d %s",
			resp.StatusCode, truncate(strings.TrimSpace(string(body)), 300)).WithProvider(channelID)
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil || tr.AccessToken == "" {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Vertex 令牌刷新响应缺少 access_token").WithProvider(channelID)
	}
	if tr.ExpiresIn <= 0 {
		tr.ExpiresIn = 3600
	}
	env.SetCredential("access_token", tr.AccessToken)
	env.SetCredential("expires_at", time.Now().Add(time.Duration(tr.ExpiresIn)*time.Second).Format(time.RFC3339))
	return tr.AccessToken, nil
}

func mapUpstreamError(status int, body string, hdr http.Header) *ir.APIError {
	msg := truncate(strings.TrimSpace(body), 400)
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "Vertex 鉴权失败（%d）：%s", status, msg).WithProvider(channelID)
	case status == http.StatusTooManyRequests:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit, "Vertex 限流：%s", msg).
			WithProvider(channelID).WithRetryable(true).WithStatus(http.StatusTooManyRequests).
			WithRetryAfter(provider.RetryAfterHint(hdr, msg))
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return ir.NewAPIErrorf(ir.ErrCodeBadRequest, "Vertex 拒绝请求：%s", msg).WithProvider(channelID)
	case status >= 500:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Vertex 上游 %d：%s", status, msg).
			WithProvider(channelID).WithRetryable(true).WithStatus(http.StatusBadGateway)
	default:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Vertex 上游返回 %d：%s", status, msg).WithProvider(channelID)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
