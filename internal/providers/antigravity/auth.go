// Package antigravity 是 Antigravity（Google Cloud Code Assist 变体）渠道。
//
// 协议要点（与官方 Antigravity 客户端一致）：
//   - OAuth：Google OAuth2，scope 含 cloud-platform / userinfo / cclog；
//   - 项目发现：POST /v1internal:loadCodeAssist，取 cloudaicompanionProject；
//   - 对话：POST /v1internal:streamGenerateContent?alt=sse，请求体为
//     {model, userAgent, requestType, project, requestId, request:{contents,...}}。
//
// 本包负责把 IR 请求翻译成 Gemini 风格请求，并把 SSE 里的
// candidates / usageMetadata 还原成 IR 的极简事件。
package antigravity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// Antigravity OAuth client credentials are provided by the user.
const (
	tokenEndpoint = "https://oauth2.googleapis.com/token"
	dailyBaseURL  = "https://daily-cloudcode-pa.googleapis.com"

	// 官方客户端 UA。Cloud Code 对低于 2.23.0 的客户端会拒绝新模型。
	// UA 由 useragent.go 按本机平台生成（见 antigravityUserAgent）。

	tokenSafetyWindow = 5 * time.Minute
)

// oauthClientPair 是一组 Google OAuth 客户端参数。
//
// 上游 Antigravity 客户端把公开客户端参数编译在 language_server 里；
// 这里只在运行时从本机官方客户端读取，不把 secret 写进源码/仓库。
// 参考项目 local-vault / aether 使用的是同一个公开 client_id。
type oauthClientPair struct {
	ID     string
	Secret string
}

// 公开 Antigravity client_id 的 SHA-256 前缀（不是 secret）。用于在
// language_server 里的多组 Google OAuth 参数中优先挑出 Antigravity 那组。
const antigravityClientIDHashPrefix = "bf00c418"

var (
	oauthIDPattern     = regexp.MustCompile(`[0-9]{6,}-[a-z0-9]{10,}\.apps\.googleusercontent\.com`)
	oauthSecretPattern = regexp.MustCompile(`GOCSPX-[A-Za-z0-9_-]{10,}`)

	oauthClientsOnce sync.Once
	oauthClients     []oauthClientPair
	oauthWorkingPair atomic.Pointer[oauthClientPair]
)

func explicitOAuthClientPair(env *provider.AccountEnv) (oauthClientPair, bool) {
	clientID := strings.TrimSpace(env.GetCredential("oauth_client_id"))
	clientSecret := strings.TrimSpace(env.GetCredential("oauth_client_secret"))
	if clientID == "" {
		clientID = strings.TrimSpace(os.Getenv("AETHER_ANTIGRAVITY_OAUTH_CLIENT_ID"))
	}
	if clientSecret == "" {
		clientSecret = strings.TrimSpace(os.Getenv("AETHER_ANTIGRAVITY_OAUTH_CLIENT_SECRET"))
	}
	if clientID == "" || clientSecret == "" {
		return oauthClientPair{}, false
	}
	return oauthClientPair{ID: clientID, Secret: clientSecret}, true
}

// antigravityOAuthClientCredentials 只返回用户显式配置的参数；
// 供 manifest 广告 OAuth 能力与现有配置测试使用。自动发现只在刷新令牌时启用，
// 避免把本机客户端里的参数当成用户配置。
func antigravityOAuthClientCredentials(env *provider.AccountEnv) (string, string, error) {
	pair, ok := explicitOAuthClientPair(env)
	if !ok {
		return "", "", fmt.Errorf("Antigravity token refresh requires oauth_client_id/oauth_client_secret or AETHER_ANTIGRAVITY_OAUTH_CLIENT_ID/AETHER_ANTIGRAVITY_OAUTH_CLIENT_SECRET")
	}
	return pair.ID, pair.Secret, nil
}

func antigravityLanguageServerPaths() []string {
	if v := strings.TrimSpace(os.Getenv("AETHER_ANTIGRAVITY_LANGUAGE_SERVER")); v != "" {
		return []string{v}
	}
	var out []string
	switch runtime.GOOS {
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			for _, name := range []string{"antigravity", "Antigravity"} {
				out = append(out, filepath.Join(d, "Programs", name, "resources", "bin", "language_server.exe"))
			}
		}
		if d := os.Getenv("ProgramFiles"); d != "" {
			out = append(out, filepath.Join(d, "Antigravity", "resources", "bin", "language_server.exe"))
		}
	case "darwin":
		if home, err := os.UserHomeDir(); err == nil {
			out = append(out, filepath.Join(home, "Library", "Application Support", "Antigravity", "resources", "bin", "language_server"))
		}
	default:
		out = append(out,
			"/opt/antigravity/resources/bin/language_server",
			"/usr/share/antigravity/resources/bin/language_server",
		)
	}
	return out
}

func isKnownAntigravityClientID(id string) bool {
	sum := sha256.Sum256([]byte(id))
	return strings.HasPrefix(hex.EncodeToString(sum[:]), antigravityClientIDHashPrefix)
}

// scanOAuthFile 从官方客户端二进制里抽取所有 Google OAuth 参数。
// 文件较大（约 190 MB），按块扫描并保留 256 字节重叠，避免把参数读进内存。
func scanOAuthFile(path string) []oauthClientPair {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	const chunk = 4 << 20
	const overlap = 256
	buf := make([]byte, chunk+overlap)
	carry := 0
	ids := map[string]bool{}
	secrets := map[string]bool{}

	for {
		n, readErr := f.Read(buf[carry:])
		if n > 0 {
			data := buf[:carry+n]
			for _, m := range oauthIDPattern.FindAll(data, -1) {
				ids[string(m)] = true
			}
			for _, m := range oauthSecretPattern.FindAll(data, -1) {
				secrets[string(m)] = true
			}
			if len(data) > overlap {
				copy(buf, data[len(data)-overlap:])
				carry = overlap
			} else {
				carry = len(data)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			break
		}
	}
	if len(ids) == 0 || len(secrets) == 0 {
		return nil
	}

	// Antigravity 的 client_id 优先，其余 ID 作为兜底；secret 无法从二进制里
	// 可靠配对（多个产品共用文件），所以每个 ID 尝试全部 secret。
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		if isKnownAntigravityClientID(id) {
			ordered = append(ordered, id)
		}
	}
	for id := range ids {
		if !isKnownAntigravityClientID(id) {
			ordered = append(ordered, id)
		}
	}
	pairs := make([]oauthClientPair, 0, len(ordered)*len(secrets))
	for _, id := range ordered {
		for secret := range secrets {
			pairs = append(pairs, oauthClientPair{ID: id, Secret: secret})
		}
	}
	return pairs
}

func discoverOAuthClientPairs() []oauthClientPair {
	oauthClientsOnce.Do(func() {
		for _, path := range antigravityLanguageServerPaths() {
			if pairs := scanOAuthFile(path); len(pairs) > 0 {
				oauthClients = pairs
				return
			}
		}
	})
	return oauthClients
}

var oauthScopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
	"https://www.googleapis.com/auth/cclog",
	"https://www.googleapis.com/auth/experimentsandconfigs",
}

// ensureAccessToken 保证账号有可用的 access token。
//
// 过期窗口内主动刷新，而不是等 401 后再补救：Antigravity 的 401
// 会同时中断正在进行的流，客户端体验很差。
func (p *Provider) ensureAccessToken(ctx context.Context, env *provider.AccountEnv) (string, error) {
	token := strings.TrimSpace(env.GetCredential("access_token"))
	expiresAt := parseExpiry(env.GetCredential("expires_at"))
	needRefresh := token == "" || (!expiresAt.IsZero() && time.Now().After(expiresAt.Add(-tokenSafetyWindow)))

	if needRefresh {
		refresh := strings.TrimSpace(env.GetCredential("refresh_token"))
		if refresh == "" {
			if token != "" {
				return token, nil
			}
			return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth,
				"Antigravity 账号缺少 access_token，且没有 refresh_token；请重新导入本机登录态或粘贴刷新令牌").
				WithProvider("antigravity")
		}
		if err := p.refreshAccessToken(ctx, env, refresh); err != nil {
			return "", err
		}
		return env.GetCredential("access_token"), nil
	}
	return token, nil
}

type oauthRefreshError struct {
	Status int
	Body   string
	Err    error
}

func (e *oauthRefreshError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, truncate(e.Body, 300))
}

func oauthRefreshKind(err error) string {
	var oe *oauthRefreshError
	if !errors.As(err, &oe) {
		return "other"
	}
	low := strings.ToLower(oe.Body)
	if strings.Contains(low, "invalid_grant") {
		return "grant"
	}
	if oe.Status == http.StatusUnauthorized || strings.Contains(low, "invalid_client") {
		return "client"
	}
	return "other"
}

func dedupeOAuthPairs(in []oauthClientPair) []oauthClientPair {
	seen := make(map[string]bool, len(in))
	out := make([]oauthClientPair, 0, len(in))
	for _, pair := range in {
		key := pair.ID + "\x00" + pair.Secret
		if pair.ID == "" || pair.Secret == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, pair)
	}
	return out
}

// refreshAccessToken 使用 refresh_token 换新 access_token，并写回账号。
//
// 客户端参数优先用用户显式配置；否则从本机官方 Antigravity 客户端的
// language_server 里运行时读取。二进制里可能同时有多组 Google OAuth
// 参数，按「已知 client_id → 其他 client_id × 全部 secret」依次试，
// 成功的组合缓存在进程内，避免每次刷新重复扫描 190 MB 文件。
func (p *Provider) refreshAccessToken(ctx context.Context, env *provider.AccountEnv, refreshToken string) error {
	var candidates []oauthClientPair
	if pair := oauthWorkingPair.Load(); pair != nil {
		candidates = append(candidates, *pair)
	}
	if pair, ok := explicitOAuthClientPair(env); ok {
		candidates = append(candidates, pair)
	}
	candidates = append(candidates, referenceOAuthClientPair)
	candidates = append(candidates, discoverOAuthClientPairs()...)
	candidates = dedupeOAuthPairs(candidates)
	if len(candidates) == 0 {
		return ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"Antigravity 刷新令牌需要 Google OAuth 客户端参数：未在本机找到官方 Antigravity 客户端；请安装/登录官方客户端，或配置 oauth_client_id/oauth_client_secret（AETHER_ANTIGRAVITY_OAUTH_CLIENT_ID/SECRET）").
			WithProvider("antigravity")
	}

	var last error
	sawGrant := false
	for _, pair := range candidates {
		err := p.refreshAccessTokenWithPair(ctx, env, refreshToken, pair)
		if err == nil {
			working := pair
			oauthWorkingPair.Store(&working)
			return nil
		}
		last = err
		switch oauthRefreshKind(err) {
		case "client":
			continue
		case "grant":
			// 某个错误 secret 也可能回 invalid_grant；继续试完，最后再定性。
			sawGrant = true
			continue
		default:
			return err
		}
	}
	if sawGrant {
		return ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"Antigravity refresh_token 已失效或不属于本机客户端，请重新导入官方客户端登录态").
			WithCause(last).WithProvider("antigravity")
	}
	return ir.NewAPIError(ir.ErrCodeUpstreamAuth,
		"Antigravity 本机客户端里没有可用的 OAuth 参数（client_id/secret 组合均被拒绝），请重新登录官方客户端或配置 OAuth 参数").
		WithCause(last).WithProvider("antigravity")
}

func (p *Provider) refreshAccessTokenWithPair(ctx context.Context, env *provider.AccountEnv, refreshToken string, pair oauthClientPair) error {
	form := url.Values{}
	form.Set("client_id", pair.ID)
	form.Set("client_secret", pair.Secret)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return ir.NewAPIError(ir.ErrCodeInternal, "构造 Antigravity 刷新请求失败").WithCause(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", antigravityUserAgent())

	client := env.Client()
	resp, err := client.Do(req)
	if err != nil {
		return ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Antigravity 刷新令牌请求失败"+refreshHint(err)).
			WithCause(err).WithProvider("antigravity").WithRetryable(true)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &oauthRefreshError{Status: resp.StatusCode, Body: string(body)}
	}

	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Antigravity 刷新响应无法解析").WithCause(err)
	}
	if strings.TrimSpace(tr.AccessToken) == "" {
		return ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Antigravity 刷新响应缺少 access_token").
			WithProvider("antigravity")
	}

	env.SetCredential("access_token", tr.AccessToken)
	if strings.TrimSpace(tr.RefreshToken) != "" {
		env.SetCredential("refresh_token", tr.RefreshToken)
	}
	if strings.TrimSpace(tr.IDToken) != "" {
		env.SetCredential("id_token", tr.IDToken)
	}
	if tr.ExpiresIn > 0 {
		env.SetCredential("expires_at", time.Now().Add(time.Duration(tr.ExpiresIn)*time.Second).
			Format(time.RFC3339))
	}
	return nil
}

// ensureProjectID 返回账号绑定的 GCP 项目；缺失时通过 loadCodeAssist 发现。
func (p *Provider) ensureProjectID(ctx context.Context, env *provider.AccountEnv, accessToken string) (string, error) {
	if id := strings.TrimSpace(env.GetCredential("project_id")); id != "" {
		return id, nil
	}
	if strings.TrimSpace(accessToken) == "" {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Antigravity 缺少 access_token").WithProvider("antigravity")
	}

	body := []byte(`{"metadata":{"ideType":"ANTIGRAVITY"}}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL(env)+"/v1internal:loadCodeAssist", strings.NewReader(string(body)))
	if err != nil {
		return "", ir.NewAPIError(ir.ErrCodeInternal, "构造 Antigravity 项目发现请求失败").WithCause(err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", antigravityUserAgent())

	client := env.Client()
	resp, err := client.Do(req)
	if err != nil {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Antigravity 项目发现请求失败").
			WithCause(err).WithProvider("antigravity").WithRetryable(true)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth,
			"Antigravity 项目发现失败（%d）：%s", resp.StatusCode, truncate(string(raw), 300)).
			WithProvider("antigravity")
	}

	var load struct {
		CloudaicompanionProject any `json:"cloudaicompanionProject"`
		ProjectID               any `json:"projectId"`
		Project                 any `json:"project"`
	}
	if err := json.Unmarshal(raw, &load); err != nil {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Antigravity 项目发现响应无法解析").WithCause(err)
	}

	projectID := extractProject(load.CloudaicompanionProject, load.ProjectID, load.Project)
	if projectID == "" {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"Antigravity 账号没有返回 cloudaicompanionProject；该账号可能尚未完成 onboarding").
			WithProvider("antigravity")
	}
	env.SetCredential("project_id", projectID)
	return projectID, nil
}

// extractProject 兼容字符串与 {"id": "..."} 两种返回形态。
func extractProject(values ...any) string {
	for _, value := range values {
		switch v := value.(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		case map[string]any:
			if s, ok := v["id"].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

// RefreshCredential 实现 TokenRefreshCapability，供内核定时保活调用。
func (p *Provider) RefreshCredential(ctx context.Context, env *provider.AccountEnv) (*provider.CredentialDraft, error) {
	refresh := strings.TrimSpace(env.GetCredential("refresh_token"))
	token, _ := p.ensureAccessToken(ctx, env)
	if refresh != "" {
		if err := p.refreshAccessToken(ctx, env, refresh); err != nil {
			return nil, err
		}
		token = env.GetCredential("access_token")
	}
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Antigravity 刷新后仍无 access_token").
			WithProvider("antigravity")
	}

	draft := &provider.CredentialDraft{
		Label: "Antigravity",
		Credentials: map[string]string{
			"access_token":  env.GetCredential("access_token"),
			"refresh_token": env.GetCredential("refresh_token"),
			"expires_at":    env.GetCredential("expires_at"),
			"project_id":    env.GetCredential("project_id"),
			"id_token":      env.GetCredential("id_token"),
			"token_type":    env.GetCredential("token_type"),
		},
	}
	if projectID, err := p.ensureProjectID(ctx, env, token); err == nil && projectID != "" {
		draft.Credentials["project_id"] = projectID
	}
	draft.ExpiresAt = parseExpiry(env.GetCredential("expires_at")).Unix()
	if draft.ExpiresAt < 0 {
		draft.ExpiresAt = 0
	}
	return draft, nil
}

func parseExpiry(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	if n, err := parseInt64(raw); err == nil && n > 0 {
		if n > 1e12 {
			n /= 1000
		}
		return time.Unix(n, 0)
	}
	return time.Time{}
}

func parseInt64(raw string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(strings.TrimSpace(raw), "%d", &n)
	return n, err
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// refreshHint 给网络类失败一句可操作的提示：本机到 Google OAuth 的链路
// 常常需要代理，只回一个 EOF 用户无从下手。
func refreshHint(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	for _, sig := range []string{"eof", "timeout", "timed out", "connection reset", "no such host", "dial tcp", "tls handshake"} {
		if strings.Contains(msg, sig) {
			return "（需要能访问 oauth2.googleapis.com 的网络或全局代理；当前请求在建立连接时失败）"
		}
	}
	return ""
}
