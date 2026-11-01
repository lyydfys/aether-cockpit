package qoder

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/provider"
)

// Qoder 的订阅登录是"设备选择页 + 轮询"：浏览器打开 selectAccounts 完成登录，
// 本机按 nonce/verifier 轮询 deviceToken/poll 拿 token。它不属于标准 RFC 8628，
// 因此放在渠道包内实现，网关只负责创建账号。
//
// 参考 local-vault 的 qoder_oauth：PKCE S256、redirect qoder://…、
// 轮询 404 表示仍等待，200 且带 token 表示完成。

var (
	qoderPortalLoginBase = "https://qoder.com/device/selectAccounts"
	// qoderPortalPollBase 仅测试注入；空串时使用渠道 profile 的 openAPI。
	qoderPortalPollBase = ""
)

const (
	qoderRedirectURI = "qoder://aicoding.aicoding-agent/login-success"
	qoderPollPath    = "/api/v1/deviceToken/poll"
)

// PortalSession 是一次进行中的 Qoder 登录。
type PortalSession struct {
	ID         string `json:"id"`
	ProviderID string `json:"provider_id"`
	LoginURL   string `json:"login_url"`
	State      string `json:"state"` // pending | done | failed | expired
	Err        string `json:"error,omitempty"`
	Interval   int    `json:"interval"`
	CreatedAt  int64  `json:"created_at"`
	ExpiresAt  int64  `json:"expires_at"`
	Account    string `json:"account,omitempty"`

	nonce     string
	verifier  string
	machineID string
	userID    string
	token     string
	refresh   string

	committed bool
}

var (
	portalMu       sync.Mutex
	portalSessions = map[string]*PortalSession{}
)

func randomURL(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// StartPortalLogin 创建一次 Qoder 登录会话（providerID 为 qoder-cn / qoder-intl）。
func StartPortalLogin(providerID string) (*PortalSession, error) {
	base, ok := provider.Get(providerID)
	if !ok {
		return nil, fmt.Errorf("渠道未注册: %s", providerID)
	}
	p, ok := base.(*Provider)
	if !ok || p.profile.family != "qoder" {
		return nil, fmt.Errorf("渠道 %s 不是 Qoder", providerID)
	}
	machineID := p.readMachineID()
	if machineID == "" {
		// 未安装桌面端时生成一个稳定值随账号保存；COSY 签名与登录会话使用同一个值。
		machineID = randomHex(16)
	}
	verifier := randomURL(32)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	nonce := randomURL(16)

	u, err := url.Parse(qoderPortalLoginBase)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("nonce", nonce)
	q.Set("challenge", challenge)
	q.Set("challenge_method", "S256")
	q.Set("redirect_uri", qoderRedirectURI)
	q.Set("machine_id", machineID)
	u.RawQuery = q.Encode()

	s := &PortalSession{
		ID:         "qoder_" + randomHex(8),
		ProviderID: providerID,
		LoginURL:   u.String(),
		State:      "pending",
		Interval:   1,
		CreatedAt:  time.Now().Unix(),
		ExpiresAt:  time.Now().Add(10 * time.Minute).Unix(),
		nonce:      nonce,
		verifier:   verifier,
		machineID:  machineID,
	}
	portalMu.Lock()
	portalSessions[s.ID] = s
	portalMu.Unlock()

	go pollPortal(s, p.profile.openAPI)
	return s, nil
}

// GetPortalLogin 返回会话副本。
func GetPortalLogin(id string) *PortalSession {
	portalMu.Lock()
	defer portalMu.Unlock()
	s := portalSessions[id]
	if s == nil {
		return nil
	}
	cp := *s
	return &cp
}

// CancelPortalLogin 取消并删除会话。
func CancelPortalLogin(id string) {
	portalMu.Lock()
	if s := portalSessions[id]; s != nil && s.State == "pending" {
		s.State = "failed"
		s.Err = "用户取消"
	}
	portalMu.Unlock()
}

// MarkPortalCommitted 标记账号已创建，避免重复入库。
func MarkPortalCommitted(id, accountID string) bool {
	portalMu.Lock()
	defer portalMu.Unlock()
	s := portalSessions[id]
	if s == nil || s.State != "done" || s.committed {
		return false
	}
	s.committed = true
	s.Account = accountID
	return true
}

// PortalCredentials 返回完成后可用于建号的凭证（仅当 state=done）。
func PortalCredentials(id string) map[string]string {
	portalMu.Lock()
	defer portalMu.Unlock()
	s := portalSessions[id]
	if s == nil || s.State != "done" || s.token == "" {
		return nil
	}
	return map[string]string{
		"token":         s.token,
		"refresh_token": s.refresh,
		"user_id":       s.userID,
		"machine_id":    s.machineID,
	}
}

func pollPortal(s *PortalSession, openAPI string) {
	if qoderPortalPollBase != "" {
		openAPI = qoderPortalPollBase
	}
	client := &http.Client{Timeout: 15 * time.Second}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		portalMu.Lock()
		if s.State != "pending" {
			portalMu.Unlock()
			return
		}
		if time.Now().Unix() > s.ExpiresAt {
			s.State = "expired"
			s.Err = "登录超时"
			portalMu.Unlock()
			return
		}
		portalMu.Unlock()

		u := openAPI + qoderPollPath + "?" + url.Values{
			"nonce":            {s.nonce},
			"verifier":         {s.verifier},
			"challenge_method": {"S256"},
		}.Encode()
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		cancel()
		if err != nil {
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			continue
		}
		var out struct {
			Token     string `json:"token"`
			UserID    string `json:"user_id"`
			Refresh   string `json:"refresh_token"`
			ExpiresAt string `json:"expires_at"`
		}
		if json.Unmarshal(raw, &out) != nil || strings.TrimSpace(out.Token) == "" {
			continue
		}
		portalMu.Lock()
		if s.State != "pending" {
			portalMu.Unlock()
			return
		}
		s.token = strings.TrimSpace(out.Token)
		s.refresh = strings.TrimSpace(out.Refresh)
		s.userID = strings.TrimSpace(out.UserID)
		s.State = "done"
		portalMu.Unlock()
		return
	}
}
