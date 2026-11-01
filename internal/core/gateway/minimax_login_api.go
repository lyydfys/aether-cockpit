package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/minimax"
)

// MiniMax Code 设备登录：前端拿 user_code 去官方页面授权，网关轮询并落账号。
// 会话只存在内存，10 分钟过期；令牌刷新由 minimax.RefreshCredential 负责。

type miniMaxLoginSession struct {
	id      string
	site    string
	flow    *minimax.DeviceFlow
	created time.Time
	mu      sync.Mutex
}

type miniMaxLoginStore struct {
	mu sync.Mutex
	m  map[string]*miniMaxLoginSession
}

func newMiniMaxLoginStore() *miniMaxLoginStore {
	return &miniMaxLoginStore{m: map[string]*miniMaxLoginSession{}}
}

func (s *miniMaxLoginStore) add(sess *miniMaxLoginSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, old := range s.m {
		if now.Sub(old.created) > 20*time.Minute {
			delete(s.m, id)
		}
	}
	s.m[sess.id] = sess
}

func (s *miniMaxLoginStore) get(id string) *miniMaxLoginSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[id]
}

func (s *miniMaxLoginStore) remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
}

func newSessionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// handleMiniMaxDeviceStart POST /api/provider/minimax/device {site}
func (g *Gateway) handleMiniMaxDeviceStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Site string `json:"site"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = json.Unmarshal(raw, &body)
	site := strings.TrimSpace(body.Site)
	if site != "minimax-code-global" {
		site = "minimax-code"
	}
	ctx, cancel := contextWithTimeout(r, 25*time.Second)
	defer cancel()
	client, _ := g.tr.NewClient(provider.NetPolicyProxyFirst, "", "")
	flow, err := minimax.StartDevice(ctx, site, client)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	sess := &miniMaxLoginSession{id: newSessionID(), site: site, flow: flow, created: time.Now()}
	g.mmLogins.add(sess)
	writeJSON(w, http.StatusOK, map[string]any{
		"id": sess.id, "site": site, "user_code": flow.UserCode,
		"verification_uri": flow.URL, "interval": int(flow.Interval.Seconds()),
		"expires_at": flow.Deadline.Unix(),
	})
}

// handleMiniMaxDevicePoll GET /api/provider/minimax/device/{id}
func (g *Gateway) handleMiniMaxDevicePoll(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess := g.mmLogins.get(id)
	if sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"state": "unknown", "error": "登录会话不存在或已过期"})
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if time.Now().After(sess.flow.Deadline) {
		g.mmLogins.remove(id)
		writeJSON(w, http.StatusOK, map[string]any{"state": "expired", "error": "登录码已过期，请重新发起"})
		return
	}
	ctx, cancel := contextWithTimeout(r, 20*time.Second)
	defer cancel()
	client, _ := g.tr.NewClient(provider.NetPolicyProxyFirst, "", "")
	ts, state, err := minimax.PollDevice(ctx, sess.site, sess.flow, client)
	if err != nil {
		g.mmLogins.remove(id)
		writeJSON(w, http.StatusOK, map[string]any{"state": "failed", "error": err.Error()})
		return
	}
	if state != "done" {
		writeJSON(w, http.StatusOK, map[string]any{"state": "pending", "retry_after": int(sess.flow.Interval.Seconds())})
		return
	}

	creds := map[string]string{
		"access_token":  ts.Access,
		"refresh_token": ts.Refresh,
		"expires_at":    time.Unix(ts.ExpiresAt, 0).UTC().Format(time.RFC3339),
	}
	if minimax.AccountBaseOverride != "" {
		// 自建代理/测试：账号 API、聊天、平台都指向同一台假上游。
		creds["account_base_url"] = minimax.AccountBaseOverride
		creds["agent_base_url"] = minimax.AccountBaseOverride
		creds["platform_base_url"] = minimax.AccountBaseOverride
	}
	label := "MiniMax 账号"
	env, apiErr := g.createAccount(sess.site, label, creds, "")
	if apiErr != nil {
		writeJSON(w, apiErr.StatusCode(), map[string]any{"state": "failed", "error": apiErr.Message})
		return
	}
	// 登录后补一次用户信息：账号标签用邮箱/昵称，签到与额度也能直接用 realUserID。
	if p, ok := providerGet(sess.site); ok {
		if mp, ok := p.(*minimax.Provider); ok {
			if uid, email, name, ierr := mp.IdentityInfo(ctx, env); ierr == nil {
				if uid != "" {
					env.SetCredential("user_id", uid)
				}
				who := email
				if who == "" {
					who = name
				}
				if who != "" {
					env.Label = who
					label = who
				}
			}
		}
	}
	g.saveAccountsNow()
	g.mmLogins.remove(id)
	writeJSON(w, http.StatusOK, map[string]any{
		"state": "done", "account_id": env.AccountID, "provider": sess.site, "user": label,
	})
}

// handleMiniMaxDeviceCancel POST /api/provider/minimax/device/{id}/cancel
func (g *Gateway) handleMiniMaxDeviceCancel(w http.ResponseWriter, r *http.Request) {
	g.mmLogins.remove(r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{"state": "canceled"})
}

func contextWithTimeout(r *http.Request, d time.Duration) (ctx context.Context, cancel context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}

func providerGet(id string) (provider.BaseProvider, bool) { return provider.Get(id) }
