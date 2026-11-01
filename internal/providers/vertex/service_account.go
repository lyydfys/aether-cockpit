package vertex

// Vertex AI service_account 凭据：用 RS256 JWT-bearer 换 access_token。
// 算法与 gcloud / relay-panel 一致：RS256(header.claims) + JWT bearer grant。

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

func serviceAccountToken(ctx context.Context, env *provider.AccountEnv, adc *ADCFile) (string, error) {
	key, err := parseRSAPrivateKey(adc.PrivateKey)
	if err != nil {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Vertex service_account 私钥无效: "+err.Error()).WithProvider(channelID)
	}
	now := time.Now().Unix()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iss": adc.ClientEmail, "scope": cloudPlatformScope,
		"aud": adc.TokenURI, "iat": now, "exp": now + 3600,
	})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	sum := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", ir.NewAPIError(ir.ErrCodeInternal, "Vertex JWT 签名失败").WithCause(err).WithProvider(channelID)
	}
	assertion := unsigned + "." + base64.RawURLEncoding.EncodeToString(sig)

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", assertion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, adc.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", ir.NewAPIError(ir.ErrCodeInternal, "构造 Vertex service_account 令牌请求失败").WithCause(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := env.Client()
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Vertex service_account 令牌交换失败（需要 oauth2.googleapis.com 网络/代理）").
			WithCause(err).WithProvider(channelID).WithRetryable(true)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "Vertex service_account 令牌交换失败：HTTP %d %s",
			resp.StatusCode, truncate(strings.TrimSpace(string(body)), 300)).WithProvider(channelID)
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil || tr.AccessToken == "" {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Vertex service_account 令牌响应缺少 access_token").WithProvider(channelID)
	}
	if tr.ExpiresIn <= 0 {
		tr.ExpiresIn = 3600
	}
	env.SetCredential("access_token", tr.AccessToken)
	env.SetCredential("expires_at", time.Now().Add(time.Duration(tr.ExpiresIn)*time.Second).Format(time.RFC3339))
	return tr.AccessToken, nil
}

func parseRSAPrivateKey(raw string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, fmt.Errorf("不是 PEM 私钥")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
		return nil, fmt.Errorf("不是 RSA 私钥")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("无法解析 PKCS8/PKCS1 私钥")
}
