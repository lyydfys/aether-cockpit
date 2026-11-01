package qoder

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// Qoder 桌面客户端内置的公开 RSA 公钥。
const qoderRSAPublicKey = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDA8iMH5c02LilrsERw9t6Pv5Nc
4k6Pz1EaDicBMpdpxKduSZu5OANqUq8er4GM95omAGIOPOh+Nx0spthYA2BqGz+l
6HRkPJ7S236FZz73In/KVuLnwI8JJ2CbuJap8kvheCCZpmAWpb/cPx/3Vr/J6I17
XcW+ML9FoCI6AOvOzwIDAQAB
-----END PUBLIC KEY-----`

// 注意：上面的 PEM 必须与官方客户端一致；如果甲方更换公钥，可通过 spec 覆盖。
var qoderRSAKeyOnce sync.Once
var qoderRSAKey *rsa.PublicKey
var qoderRSAKeyErr error

func loadRSAKey() (*rsa.PublicKey, error) {
	qoderRSAKeyOnce.Do(func() {
		block, _ := pem.Decode([]byte(qoderRSAPublicKey))
		if block == nil {
			qoderRSAKeyErr = errors.New("qoder: RSA public key PEM 解析失败")
			return
		}
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			qoderRSAKeyErr = err
			return
		}
		rsaKey, ok := key.(*rsa.PublicKey)
		if !ok {
			qoderRSAKeyErr = errors.New("qoder: RSA 公钥类型错误")
			return
		}
		qoderRSAKey = rsaKey
	})
	return qoderRSAKey, qoderRSAKeyErr
}

type creds struct {
	UserID    string
	Token     string
	Name      string
	Email     string
	MachineID string
}

func (p *Provider) credentials(env *provider.AccountEnv) creds {
	return creds{
		UserID:    strings.TrimSpace(firstNonEmpty(env.GetCredential("user_id"), env.GetCredential("uid"))),
		Token:     strings.TrimSpace(firstNonEmpty(env.GetCredential("token"), env.GetCredential("access_token"))),
		Name:      env.GetCredential("user_name"),
		Email:     env.GetCredential("user_email"),
		MachineID: strings.TrimSpace(env.GetCredential("machine_id")),
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ensureToken 保证账号有可用令牌；过期时用 refresh_token 续期。
func (p *Provider) ensureToken(ctx context.Context, env *provider.AccountEnv) (creds, error) {
	c := p.credentials(env)
	if c.Token == "" {
		return c, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"Qoder 账号缺少 token；请重新导入桌面登录态或粘贴 Personal Access Token").
			WithProvider(p.profile.id)
	}
	if c.MachineID == "" {
		return c, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"Qoder 账号缺少 machine_id；COSY 签名必需，请重新导入桌面登录态").
			WithProvider(p.profile.id)
	}
	if c.UserID == "" {
		return c, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"Qoder 账号缺少 user_id；请从桌面登录态重新导入").
			WithProvider(p.profile.id)
	}
	return c, nil
}

// refreshToken 用设备刷新令牌换新令牌。
func (p *Provider) refreshToken(ctx context.Context, env *provider.AccountEnv) error {
	refresh := strings.TrimSpace(env.GetCredential("refresh_token"))
	machine := strings.TrimSpace(env.GetCredential("machine_id"))
	if refresh == "" {
		return ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Qoder 账号缺少 refresh_token").
			WithProvider(p.profile.id)
	}
	body, _ := json.Marshal(map[string]string{"refresh_token": refresh, "machine_id": machine})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.profile.openAPI+"/api/v1/deviceToken/refresh", bytes.NewReader(body))
	if err != nil {
		return ir.NewAPIError(ir.ErrCodeInternal, "构造 Qoder 刷新请求失败").WithCause(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "qoder/1.1.77")

	resp, err := p.doHTTP(env, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth,
			"Qoder 刷新令牌失败（%d）：%s", resp.StatusCode, truncate(string(raw), 300)).
			WithProvider(p.profile.id)
	}
	var tr struct {
		DeviceToken  string `json:"device_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresAt    string `json:"expires_at"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Qoder 刷新响应无法解析").WithCause(err)
	}
	if strings.TrimSpace(tr.DeviceToken) == "" {
		return ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Qoder 刷新响应缺少 device_token").
			WithProvider(p.profile.id)
	}
	env.SetCredential("token", tr.DeviceToken)
	if tr.RefreshToken != "" {
		env.SetCredential("refresh_token", tr.RefreshToken)
	}
	if tr.ExpiresAt != "" {
		env.SetCredential("expires_at", tr.ExpiresAt)
	}
	return nil
}

// RefreshCredential 实现 TokenRefreshCapability。
func (p *Provider) RefreshCredential(ctx context.Context, env *provider.AccountEnv) (*provider.CredentialDraft, error) {
	if strings.TrimSpace(env.GetCredential("refresh_token")) != "" {
		if err := p.refreshToken(ctx, env); err != nil {
			return nil, err
		}
	}
	c := p.credentials(env)
	if c.Token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Qoder 刷新后仍无 token").
			WithProvider(p.profile.id)
	}
	return &provider.CredentialDraft{
		Label: env.Label,
		Credentials: map[string]string{
			"token":         c.Token,
			"refresh_token": env.GetCredential("refresh_token"),
			"user_id":       c.UserID,
			"user_name":     c.Name,
			"user_email":    c.Email,
			"machine_id":    c.MachineID,
			"expires_at":    env.GetCredential("expires_at"),
		},
	}, nil
}

func (p *Provider) doHTTP(env *provider.AccountEnv, req *http.Request) (*http.Response, error) {
	client := env.Client()
	resp, err := client.Do(req)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Qoder 上游连接失败").
			WithCause(err).WithProvider(p.profile.id).WithRetryable(true)
	}
	return resp, nil
}

// --- COSY 签名 + 自定义 Base64 ---

const qoderAlphabet = "_doRTgHZBKcGVjlvpC,@aFSx#DPuNJme&i*MzLOEn)sUrthbf%Y^w.(kIQyXqWA!"

// encodeQoderBody 把 JSON 字节编码成 Qoder 网关要求的 ASCII 字节。
func encodeQoderBody(plain []byte) []byte {
	seg := segmentEncode(plain)
	return []byte(swapThirds(seg))
}

func segmentEncode(bytes []byte) string {
	var b strings.Builder
	b.Grow((len(bytes)*8 + 5) / 6)
	acc := 0
	nb := 0
	for _, v := range bytes {
		acc = ((acc << 8) | int(v)) & 0xffffff
		nb += 8
		for nb >= 6 {
			nb -= 6
			b.WriteByte(qoderAlphabet[(acc>>nb)&63])
		}
	}
	if nb > 0 {
		b.WriteByte(qoderAlphabet[(acc<<(6-nb))&63])
	}
	for b.Len()%4 != 0 {
		b.WriteByte('$')
	}
	return b.String()
}

func swapThirds(s string) string {
	t := len(s) / 3
	if t == 0 {
		return s
	}
	return s[len(s)-t:] + s[t:len(s)-t] + s[:t]
}

// buildCOSYHeaders 生成签名头；wireBody 是已经编码后的请求体。
func (p *Provider) buildCOSYHeaders(wireBody []byte, requestURL string, c creds) (map[string]string, error) {
	pub, err := loadRSAKey()
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "Qoder COSY 公钥加载失败").WithCause(err)
	}
	if c.UserID == "" || c.Token == "" || c.MachineID == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Qoder COSY 缺少 uid/token/machine_id").
			WithProvider(p.profile.id)
	}

	key := randomHex(8) // 16 个 ASCII 字符
	userBlob, err := aesCBCEncryptBase64(userInfoJSON(c), key)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "Qoder COSY 用户信息加密失败").WithCause(err)
	}
	wrapped, err := rsaEncryptBase64(key, pub)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "Qoder COSY 密钥加密失败").WithCause(err)
	}

	payloadBytes, _ := json.Marshal(map[string]string{
		"version":     "v1",
		"requestId":   randomHex(16),
		"info":        userBlob,
		"cosyVersion": p.profile.cosyVer,
		"ideVersion":  "",
	})
	payload := base64.StdEncoding.EncodeToString(payloadBytes)

	ts := fmt.Sprintf("%d", time.Now().Unix())
	parsed, err := url.Parse(requestURL)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "Qoder 请求 URL 无效").WithCause(err)
	}
	sigPath := parsed.Path
	if strings.HasPrefix(sigPath, "/algo") {
		sigPath = strings.TrimPrefix(sigPath, "/algo")
	}
	digest := md5.New()
	digest.Write([]byte(payload))
	digest.Write([]byte("\n"))
	digest.Write([]byte(wrapped))
	digest.Write([]byte("\n"))
	digest.Write([]byte(ts))
	digest.Write([]byte("\n"))
	digest.Write(wireBody)
	digest.Write([]byte("\n"))
	digest.Write([]byte(sigPath))
	sig := hex.EncodeToString(digest.Sum(nil))

	bodyHash := md5.Sum(wireBody)
	headers := map[string]string{
		"Accept":                "text/event-stream",
		"Accept-Encoding":       "identity",
		"Content-Type":          "application/json",
		"Authorization":         "Bearer COSY." + payload + "." + sig,
		"Cosy-Business-Product": "app",
		"Cosy-Business-Type":    "agent",
		"Cosy-ClientIp":         c.MachineID,
		"Cosy-ClientType":       "10",
		"Cosy-Data-Policy":      "disagree",
		"Cosy-Date":             ts,
		"Cosy-Key":              wrapped,
		"Cosy-MachineId":        c.MachineID,
		"Cosy-MachineToken":     c.MachineID,
		"Cosy-MachineType":      "5",
		"Cosy-MachineOS":        machineOS(),
		"Cosy-Scene":            "app",
		"Cosy-User":             c.UserID,
		"Cosy-Version":          p.profile.cosyVer,
		"Login-Version":         "v2",
		"X-Request-Id":          randomHex(16),
		"Cosy-Bodyhash":         hex.EncodeToString(bodyHash[:]),
		"Cosy-Bodylength":       fmt.Sprintf("%d", len(wireBody)),
		"Cosy-Sigpath":          sigPath,
	}
	return headers, nil
}

func userInfoJSON(c creds) string {
	b, _ := json.Marshal(map[string]string{
		"uid":                  c.UserID,
		"aid":                  "",
		"name":                 c.Name,
		"email":                c.Email,
		"security_oauth_token": c.Token,
	})
	return string(b)
}

func aesCBCEncryptBase64(plaintext, key string) (string, error) {
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return "", err
	}
	data := pkcs7Pad([]byte(plaintext), block.BlockSize())
	out := make([]byte, len(data))
	cipher.NewCBCEncrypter(block, []byte(key)).CryptBlocks(out, data)
	return base64.StdEncoding.EncodeToString(out), nil
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+pad)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

func rsaEncryptBase64(data string, pub *rsa.PublicKey) (string, error) {
	enc, err := rsa.EncryptPKCS1v15(rand.Reader, pub, []byte(data))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(enc), nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func machineOS() string {
	// 官方客户端在 Windows 上上报 x86_64_windows；其他平台做同样映射。
	if os.PathSeparator == '\\' {
		return "x86_64_windows"
	}
	if strings.Contains(strings.ToLower(os.Getenv("OSTYPE")), "darwin") {
		return "x86_64_darwin"
	}
	return "x86_64_linux"
}

// readMachineID 从官方客户端目录读取 machine_id。
func (p *Provider) readMachineID() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, name := range []string{p.profile.authDir, ".qoder-cn", ".qoder"} {
		raw, err := os.ReadFile(filepath.Join(home, name, ".auth", "machine_id"))
		if err == nil {
			if v := strings.TrimSpace(string(raw)); v != "" {
				return v
			}
		}
	}
	return ""
}

func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
