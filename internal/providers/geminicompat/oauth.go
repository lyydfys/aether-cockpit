package geminicompat

// Gemini CLI / Code Assist OAuth：
//   - 本机 ~/.gemini/oauth_creds.json 导入（Google 官方 Gemini CLI 客户端）
//   - refresh_token 换 access_token
//   - loadCodeAssist 取 cloudaicompanionProject
//   - /v1internal:streamGenerateContent（响应仍是 Gemini SSE，可能包在 response 字段）

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	geminiOAuthTokenURL  = "https://oauth2.googleapis.com/token"
	codeAssistBaseURL    = "https://cloudcode-pa.googleapis.com"
	aiStudioBaseURL      = "https://generativelanguage.googleapis.com"
	cloudResourceBaseURL = "https://cloudresourcemanager.googleapis.com"
	geminiCLIUserAgent   = "GeminiCLI/0.1.5 (Windows; AMD64)"
)

type geminiOAuthFile struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiryDate   int64  `json:"expiry_date"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

func geminiOAuthPath() string {
	if v := strings.TrimSpace(os.Getenv("AETHER_GEMINI_OAUTH_CREDS")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".gemini", "oauth_creds.json")
}

// DiscoverLocalAccounts 读取 Gemini CLI 的本机 OAuth 登录态。
func (p *Provider) DiscoverLocalAccounts(ctx context.Context) ([]*provider.CredentialDraft, error) {
	if !p.cfg.OAuthEnabled {
		return nil, irError("该渠道不支持本机 Gemini OAuth 导入")
	}
	path := geminiOAuthPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, irError("没有找到 Gemini CLI 的 ~/.gemini/oauth_creds.json；请先运行 gemini 登录")
	}
	var f geminiOAuthFile
	if json.Unmarshal(raw, &f) != nil || strings.TrimSpace(f.AccessToken) == "" && strings.TrimSpace(f.RefreshToken) == "" {
		return nil, irError("Gemini CLI 凭据文件无法解析或缺少 token")
	}
	creds := map[string]string{
		"auth_mode":  "oauth",
		"token_type": firstNonEmpty(f.TokenType, "Bearer"),
	}
	if f.AccessToken != "" {
		creds["access_token"] = strings.TrimSpace(f.AccessToken)
	}
	if f.RefreshToken != "" {
		creds["refresh_token"] = strings.TrimSpace(f.RefreshToken)
	}
	if f.IDToken != "" {
		creds["id_token"] = strings.TrimSpace(f.IDToken)
	}
	if f.ExpiryDate > 0 {
		creds["expires_at"] = time.UnixMilli(f.ExpiryDate).UTC().Format(time.RFC3339)
	}
	label := geminiEmailFromIDToken(f.IDToken)
	if label == "" {
		label = "Gemini OAuth"
	} else {
		label = "Gemini (" + label + ")"
	}
	return []*provider.CredentialDraft{{
		Label: label, Credentials: creds, Source: "local_import:" + path,
	}}, nil
}

func geminiEmailFromIDToken(idToken string) string {
	parts := strings.Split(strings.TrimSpace(idToken), ".")
	if len(parts) < 2 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	return strings.TrimSpace(claims.Email)
}

func isGeminiOAuthAccount(env *provider.AccountEnv) bool {
	if env == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(env.GetCredential("auth_mode")), "oauth") {
		return true
	}
	return strings.TrimSpace(env.GetCredential("api_key")) == "" &&
		(strings.TrimSpace(env.GetCredential("refresh_token")) != "" || strings.TrimSpace(env.GetCredential("access_token")) != "")
}

func geminiOAuthClientCredentials(env *provider.AccountEnv) (string, string, error) {
	clientID := strings.TrimSpace(env.GetCredential("oauth_client_id"))
	clientSecret := strings.TrimSpace(env.GetCredential("oauth_client_secret"))
	if clientID == "" {
		clientID = strings.TrimSpace(os.Getenv("AETHER_GEMINI_CLI_OAUTH_CLIENT_ID"))
	}
	if clientSecret == "" {
		clientSecret = strings.TrimSpace(os.Getenv("AETHER_GEMINI_CLI_OAUTH_CLIENT_SECRET"))
	}
	if clientID == "" || clientSecret == "" {
		return "", "", errors.New("Gemini CLI token refresh requires oauth_client_id/oauth_client_secret or AETHER_GEMINI_CLI_OAUTH_CLIENT_ID/AETHER_GEMINI_CLI_OAUTH_CLIENT_SECRET")
	}
	return clientID, clientSecret, nil
}

func (p *Provider) ensureOAuthToken(ctx context.Context, env *provider.AccountEnv) (string, error) {
	access := strings.TrimSpace(env.GetCredential("access_token"))
	exp, _ := time.Parse(time.RFC3339, strings.TrimSpace(env.GetCredential("expires_at")))
	if access != "" && (exp.IsZero() || time.Now().Before(exp.Add(-60*time.Second))) {
		return access, nil
	}
	refresh := strings.TrimSpace(env.GetCredential("refresh_token"))
	if refresh == "" {
		if access != "" {
			return access, nil
		}
		return "", irError("Gemini OAuth 账号缺少 access_token / refresh_token")
	}
	tokenURL := strings.TrimSpace(env.GetCredential("oauth_token_url"))
	if tokenURL == "" {
		tokenURL = geminiOAuthTokenURL
	}
	clientID, clientSecret, err := geminiOAuthClientCredentials(env)
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := env.Client()
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Gemini OAuth 刷新失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Gemini OAuth 刷新失败: HTTP %d %s", resp.StatusCode, truncateText(string(body), 200))
	}
	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if json.Unmarshal(body, &tr) != nil || tr.AccessToken == "" {
		return "", errors.New("Gemini OAuth 刷新响应缺少 access_token")
	}
	if tr.ExpiresIn <= 0 {
		tr.ExpiresIn = 3600
	}
	if tr.RefreshToken == "" {
		tr.RefreshToken = refresh
	}
	env.SetCredential("access_token", tr.AccessToken)
	env.SetCredential("refresh_token", tr.RefreshToken)
	env.SetCredential("expires_at", time.Now().Add(time.Duration(tr.ExpiresIn)*time.Second).Format(time.RFC3339))
	return tr.AccessToken, nil
}

func (p *Provider) ensureCodeAssistProject(ctx context.Context, env *provider.AccountEnv, token string) (string, error) {
	if project := strings.TrimSpace(env.GetCredential("project_id")); project != "" {
		return project, nil
	}
	base := codeAssistBase(env)
	metadata := map[string]any{"ideType": "ANTIGRAVITY", "platform": "PLATFORM_UNSPECIFIED", "pluginType": "GEMINI"}
	tier := "LEGACY"
	project := ""
	if raw, err := p.codeAssistCall(ctx, env, token, base+"/v1internal:loadCodeAssist", map[string]any{"metadata": metadata}); err == nil {
		var out struct {
			CloudAICompanionProject any `json:"cloudaicompanionProject"`
			CurrentTier             *struct {
				ID string `json:"id"`
			} `json:"currentTier"`
			PaidTier *struct {
				ID string `json:"id"`
			} `json:"paidTier"`
			AllowedTiers []struct {
				ID        string `json:"id"`
				IsDefault bool   `json:"isDefault"`
			} `json:"allowedTiers"`
			IneligibleTiers []struct {
				ReasonCode    string `json:"reasonCode"`
				ReasonMessage string `json:"reasonMessage"`
				TierID        string `json:"tierId"`
			} `json:"ineligibleTiers"`
		}
		if json.Unmarshal(raw, &out) == nil {
			project = projectIDFromAny(out.CloudAICompanionProject)
			registered := out.PaidTier != nil && out.PaidTier.ID != "" || out.CurrentTier != nil && out.CurrentTier.ID != ""
			if out.PaidTier != nil && out.PaidTier.ID != "" {
				tier = out.PaidTier.ID
			} else if out.CurrentTier != nil && out.CurrentTier.ID != "" {
				tier = out.CurrentTier.ID
			} else {
				for _, t := range out.AllowedTiers {
					if t.IsDefault && t.ID != "" {
						tier = t.ID
						break
					}
				}
			}
			// 免费个人版 Code Assist 已被上游停用：直接给出可执行提示，
			// 不再拿任意 Cloud 项目去撞 403 SUBSCRIPTION_REQUIRED。
			if project == "" && !registered {
				for _, it := range out.IneligibleTiers {
					if it.ReasonCode == "UNSUPPORTED_CLIENT" || it.ReasonCode == "SUBSCRIPTION_REQUIRED" {
						msg := strings.TrimSpace(it.ReasonMessage)
						if msg == "" {
							msg = it.ReasonCode
						}
						return "", fmt.Errorf("Gemini Code Assist 不可用：%s；请改用 Gemini API Key，或换用有 Code Assist 许可的 Google 账号", msg)
					}
				}
			}
		}
	}
	if project == "" {
		// 已注册用户可能没有 companion project；尝试 onboardUser 创建/返回。
		onboard := map[string]any{"tierId": tier, "metadata": metadata}
		for attempt := 0; attempt < 5 && project == ""; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(2 * time.Second):
				}
			}
			raw, err := p.codeAssistCall(ctx, env, token, base+"/v1internal:onboardUser", onboard)
			if err != nil {
				break
			}
			var out struct {
				Done     bool `json:"done"`
				Response *struct {
					CloudAICompanionProject any `json:"cloudaicompanionProject"`
				} `json:"response"`
			}
			if json.Unmarshal(raw, &out) != nil {
				break
			}
			if out.Response != nil {
				project = projectIDFromAny(out.Response.CloudAICompanionProject)
			}
			if out.Done && project == "" {
				break
			}
		}
	}
	if project == "" {
		// 最后回退：从 Cloud Resource Manager 找 ACTIVE 项目。
		if p2, err := p.resourceManagerProject(ctx, env, token); err == nil {
			project = p2
		}
	}
	if project == "" {
		return "", errors.New("Gemini OAuth 无法自动获取 Project ID；请在账号里手动填写 project_id")
	}
	env.SetCredential("project_id", project)
	return project, nil
}

func codeAssistBase(env *provider.AccountEnv) string {
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" || strings.Contains(base, "generativelanguage.googleapis.com") {
		return codeAssistBaseURL
	}
	return base
}

func projectIDFromAny(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case map[string]any:
		id, _ := x["id"].(string)
		return strings.TrimSpace(id)
	}
	return ""
}

func (p *Provider) codeAssistCall(ctx context.Context, env *provider.AccountEnv, token, endpoint string, payload map[string]any) ([]byte, error) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", geminiCLIUserAgent)
	client := env.Client()
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d %s", resp.StatusCode, truncateText(string(raw), 200))
	}
	return raw, nil
}

func (p *Provider) resourceManagerProject(ctx context.Context, env *provider.AccountEnv, token string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("cloudresource_base_url")), "/")
	if base == "" {
		base = cloudResourceBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/projects", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", geminiCLIUserAgent)
	client := env.Client()
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var out struct {
		Projects []struct {
			ProjectID      string `json:"projectId"`
			DisplayName    string `json:"displayName"`
			LifecycleState string `json:"lifecycleState"`
		} `json:"projects"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return "", errors.New("projects 响应无法解析")
	}
	active := out.Projects[:0]
	for _, pr := range out.Projects {
		if pr.LifecycleState == "ACTIVE" && pr.ProjectID != "" {
			active = append(active, pr)
		}
	}
	for _, pr := range active {
		id, name := strings.ToLower(pr.ProjectID), strings.ToLower(pr.DisplayName)
		if strings.Contains(id, "cloud-ai-companion") || strings.Contains(name, "cloud ai companion") || strings.Contains(name, "code assist") {
			return pr.ProjectID, nil
		}
	}
	for _, pr := range active {
		id, name := strings.ToLower(pr.ProjectID), strings.ToLower(pr.DisplayName)
		if strings.Contains(id, "default") || strings.Contains(name, "default") {
			return pr.ProjectID, nil
		}
	}
	if len(active) > 0 {
		return active[0].ProjectID, nil
	}
	return "", errors.New("没有 ACTIVE 项目")
}

// chatAIStudio 走 generativelanguage OAuth（需要自定义 OAuth 客户端授予
// generative-language 权限）；与 Code Assist 不同，不需要 project。
func (p *Provider) chatAIStudio(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest, token string) (<-chan ir.UnifiedStreamEvent, error) {
	model := strings.TrimSpace(req.UpstreamModel)
	if model == "" {
		model = strings.TrimPrefix(strings.TrimSpace(req.Model), p.cfg.ID+"/")
	}
	model = strings.TrimPrefix(model, "models/")
	if model == "" {
		return nil, errors.New("Gemini OAuth 缺少模型名")
	}
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" || strings.Contains(base, "cloudcode-pa.googleapis.com") {
		base = aiStudioBaseURL
	}
	if strings.HasSuffix(base, "/v1") {
		base = strings.TrimSuffix(base, "/v1")
	}
	if !strings.HasSuffix(base, "/v1beta") {
		base += "/v1beta"
	}
	body, _ := json.Marshal(antigravity.GeminiBody(req, model))
	endpoint := base + "/models/" + url.PathEscape(model) + ":streamGenerateContent?alt=sse"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("User-Agent", "aether-cockpit/gemini-oauth")
	client := env.Client()
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Gemini AI Studio 连接失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return nil, fmt.Errorf("Gemini AI Studio 返回 %d: %s", resp.StatusCode, truncateText(string(raw), 300))
	}
	out := make(chan ir.UnifiedStreamEvent, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		antigravity.ParseGeminiSSE(ctx, out, resp.Body)
	}()
	return out, nil
}

func (p *Provider) chatOAuth(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	token, err := p.ensureOAuthToken(ctx, env)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(strings.TrimSpace(env.GetCredential("oauth_type")), "ai_studio") {
		return p.chatAIStudio(ctx, env, req, token)
	}
	project, err := p.ensureCodeAssistProject(ctx, env, token)
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(req.UpstreamModel)
	if model == "" {
		model = strings.TrimPrefix(strings.TrimSpace(req.Model), p.cfg.ID+"/")
	}
	model = strings.TrimPrefix(model, "models/")
	if model == "" {
		return nil, irError("Gemini OAuth 缺少模型名")
	}
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" || strings.Contains(base, "generativelanguage.googleapis.com") {
		base = codeAssistBaseURL
	}
	inner := antigravity.GeminiBody(req, model)
	payload := map[string]any{"model": model, "project": project, "request": inner}
	body, _ := json.Marshal(payload)
	endpoint := base + "/v1internal:streamGenerateContent?alt=sse"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("User-Agent", geminiCLIUserAgent)
	client := env.Client()
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Gemini Code Assist 连接失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return nil, fmt.Errorf("Gemini Code Assist 返回 %d: %s", resp.StatusCode, truncateText(string(raw), 300))
	}
	out := make(chan ir.UnifiedStreamEvent, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		antigravity.ParseGeminiSSE(ctx, out, resp.Body)
	}()
	return out, nil
}

func irError(msg string) error {
	return fmt.Errorf("%s", msg)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
