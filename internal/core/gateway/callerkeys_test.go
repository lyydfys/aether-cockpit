package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newCallerKeyGateway(t *testing.T) *Gateway {
	t.Helper()
	dir := t.TempDir()
	g := &Gateway{
		settingsPath: filepath.Join(dir, "settings.json"),
		callerKeys:   newCallerKeyStore(filepath.Join(dir, "caller-keys.json")),
		ledger:       newLedgerStore(filepath.Join(dir, "ledger")),
	}
	t.Cleanup(func() { _ = g.ledger.Close() })
	return g
}

func TestCallerKeyAuthAndAttribution(t *testing.T) {
	g := newCallerKeyGateway(t)
	_, secret := g.callerKeys.add("Team A")

	var gotKey *callerKeyInfo
	h := g.auth(func(w http.ResponseWriter, r *http.Request) {
		gotKey = callerKeyFrom(r.Context())
		w.WriteHeader(200)
	})

	// 远程 + 有效密钥 → 放行并归因
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.RemoteAddr = "10.0.0.9:3333"
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != 200 || gotKey == nil || gotKey.Name != "Team A" {
		t.Fatalf("有效密钥: code=%d key=%+v", rec.Code, gotKey)
	}

	// 远程 + 无密钥 → 401
	gotKey = nil
	req = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.RemoteAddr = "10.0.0.9:3333"
	rec = httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("远程无密钥应 401，得到 %d", rec.Code)
	}

	// 本机回环无密钥 → 放行，不归因
	gotKey = nil
	req = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	rec = httptest.NewRecorder()
	h(rec, req)
	if rec.Code != 200 || gotKey != nil {
		t.Fatalf("回环: code=%d key=%+v", rec.Code, gotKey)
	}

	// 停用后远程 401
	id := g.callerKeys.keys[0].ID
	g.callerKeys.update(id, func(k *callerKey) bool { k.Off = true; return true })
	req = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.RemoteAddr = "10.0.0.9:3333"
	req.Header.Set("Authorization", "Bearer "+secret)
	rec = httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("停用密钥应 401，得到 %d", rec.Code)
	}
}

func TestCallerKeyForwardedRequestsNeedKey(t *testing.T) {
	g := newCallerKeyGateway(t)
	_, secret := g.callerKeys.add("Tunnel")

	h := g.auth(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	// 回环 + X-Forwarded-For（隧道/反代）→ 视为远程
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("带转发头的无密钥请求应 401，得到 %d", rec.Code)
	}
	req = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("CF-Connecting-IP", "203.0.113.9")
	req.Header.Set("Authorization", "Bearer "+secret)
	rec = httptest.NewRecorder()
	h(rec, req)
	if rec.Code != 200 {
		t.Fatalf("带密钥的隧道请求应放行，得到 %d", rec.Code)
	}
}

func TestCallerKeyLimitEnforced(t *testing.T) {
	g := newCallerKeyGateway(t)
	k, secret := g.callerKeys.add("Limited")
	g.callerKeys.update(k.ID, func(x *callerKey) bool {
		x.Limit = &callerKeyLimit{Period: "days", Days: 1, Tokens: 10}
		return true
	})
	g.ledger.add(ledgerRecord{T: time.Now().UnixMilli(), Agent: "claude-code", Model: "m", CallerKey: k.ID, In: 20, Status: 200})

	h := g.auth(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.RemoteAddr = "10.0.0.9:3333"
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("超限应 429，得到 %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCallerKeyStoreRotateAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "caller-keys.json")
	s := newCallerKeyStore(path)
	k, secret := s.add("A")
	if _, ok := s.match(secret); !ok {
		t.Fatal("新密钥不匹配")
	}
	newSecret, ok := s.rotate(k.ID)
	if !ok || newSecret == secret {
		t.Fatal("rotate 未换密钥")
	}
	if _, ok := s.match(secret); ok {
		t.Fatal("旧密钥仍可用")
	}
	if _, ok := s.match(newSecret); !ok {
		t.Fatal("新密钥不可用")
	}
	// 重新加载（模拟重启）后仍能匹配，明文不落盘
	s2 := newCallerKeyStore(path)
	if _, ok := s2.match(newSecret); !ok {
		t.Fatal("重启后密钥失效")
	}
	raw := readFileString(t, path)
	if contains(raw, newSecret) {
		t.Fatal("密钥明文落盘了")
	}
}

func TestCallerKeyModels(t *testing.T) {
	full := &callerKey{Models: []string{"mock-native/echo-pro"}}
	if !full.servesModel("mock-native/echo-pro") {
		t.Error("精确模型应允许")
	}
	if full.servesModel("other/echo-pro") {
		t.Error("带渠道前缀的名字不应跨渠道匹配")
	}
	bare := &callerKey{Models: []string{"echo-pro"}}
	if !bare.servesModel("other/echo-pro") {
		t.Error("裸模型名应跨渠道匹配")
	}
	if bare.servesModel("mock-native/other") {
		t.Error("不在名单的模型应拒绝")
	}
	all := &callerKey{}
	if !all.servesModel("anything") {
		t.Error("空名单表示全部")
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(b)
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
