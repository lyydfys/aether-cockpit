package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// Azure OpenAI 除资源 api-key 外还支持 Entra ID（Microsoft Entra /
// 原 Azure AD）令牌。这里实现 client-credentials 换取与缓存，
// 让只有应用注册（tenant/client/secret）的用户也能接入。

const (
	defaultAzureTokenScope = "https://cognitiveservices.azure.com/.default"
	defaultAzureTokenURL   = "https://login.microsoftonline.com/%s/oauth2/v2.0/token"
)

// azureAccessToken 返回可直接使用的 Bearer 令牌：
// 优先复用未过期的 access_token，否则用 tenant/client/secret 换取。
func (p *Provider) azureAccessToken(ctx context.Context, env *provider.AccountEnv) (string, error) {
	if token := strings.TrimSpace(env.GetCredential("access_token")); token != "" && azureTokenFresh(env) {
		return token, nil
	}

	tenant := strings.TrimSpace(env.GetCredential("azure_tenant_id"))
	clientID := strings.TrimSpace(env.GetCredential("azure_client_id"))
	secret := strings.TrimSpace(env.GetCredential("azure_client_secret"))
	if tenant == "" || clientID == "" || secret == "" {
		if strings.TrimSpace(env.GetCredential("access_token")) != "" {
			return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth,
				"Azure Entra access_token 已过期，请重新填写，或补充 tenant/client/secret 自动续期").
				WithProvider(p.cfg.ID)
		}
		return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"Azure 需要 api-key，或 Entra 的 azure_tenant_id / azure_client_id / azure_client_secret").
			WithProvider(p.cfg.ID)
	}

	tokenURL := strings.TrimSpace(env.GetCredential("azure_token_url"))
	if tokenURL == "" {
		tokenURL = fmt.Sprintf(defaultAzureTokenURL, url.PathEscape(tenant))
	}
	scope := strings.TrimSpace(env.GetCredential("azure_scope"))
	if scope == "" {
		scope = defaultAzureTokenScope
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {secret},
		"scope":         {scope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", ir.NewAPIError(ir.ErrCodeInternal, "构造 Entra 令牌请求失败").
			WithCause(err).WithProvider(p.cfg.ID)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := env.Client().Do(req)
	if err != nil {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Entra 令牌请求失败").
			WithCause(err).WithProvider(p.cfg.ID).WithRetryable(true)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var detail struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		_ = json.Unmarshal(raw, &detail)
		msg := strings.TrimSpace(detail.ErrorDescription)
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		if detail.Error != "" {
			msg = detail.Error + ": " + msg
		}
		return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"Entra 令牌获取失败: "+clipAzureText(msg, 200)).WithProvider(p.cfg.ID)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &tokenResp); err != nil || tokenResp.AccessToken == "" {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Entra 令牌响应缺少 access_token").
			WithProvider(p.cfg.ID)
	}
	if tokenResp.ExpiresIn <= 0 {
		tokenResp.ExpiresIn = 3600
	}
	env.SetCredential("access_token", tokenResp.AccessToken)
	env.SetCredential("expires_at", time.Now().Add(time.Duration(tokenResp.ExpiresIn)*time.Second).Format(time.RFC3339))
	return tokenResp.AccessToken, nil
}

// azureTokenFresh 判断缓存的令牌是否仍可用（预留 2 分钟续期窗口）。
// expires_at 缺失时按手动粘贴的长效令牌处理，直接复用。
func azureTokenFresh(env *provider.AccountEnv) bool {
	raw := strings.TrimSpace(env.GetCredential("expires_at"))
	if raw == "" {
		return true
	}
	exp, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return true
	}
	return time.Now().Add(2 * time.Minute).Before(exp)
}

func clipAzureText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
