package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/pool"
	"aether-cockpit/internal/provider"
)

// fakeOfficialProvider 只在测试里注册：一个能区分官方账号、带绑定信息与额度接口的渠道。
type fakeOfficialProvider struct{}

func (p *fakeOfficialProvider) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID: "fake-official", DisplayName: "Fake Official", Family: "fake",
		Capabilities: provider.CapabilitySet{Chat: true, OfficialFirst: true},
	}
}

func (p *fakeOfficialProvider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	ch := make(chan ir.UnifiedStreamEvent)
	close(ch)
	return ch, nil
}

func (p *fakeOfficialProvider) IsOfficialAccount(creds map[string]string) bool {
	return creds["account_id"] != ""
}

func (p *fakeOfficialProvider) OfficialBinding(creds map[string]string) *provider.OfficialBinding {
	return &provider.OfficialBinding{Official: creds["account_id"] != "", AccountID: creds["account_id"], Plan: "plus"}
}

func (p *fakeOfficialProvider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	if env.GetCredential("quota") == "fail" {
		return nil, fmt.Errorf("授权校验失败 401")
	}
	return &provider.QuotaStatus{Total: 100, Remaining: 42, Unit: "credits"}, nil
}

var fakeOfficialOnce sync.Once

func registerFakeOfficial() {
	fakeOfficialOnce.Do(func() { provider.Register(&fakeOfficialProvider{}) })
}

func newOfficialGateway(t *testing.T) (*Gateway, *httptest.Server, *provider.AccountEnv) {
	t.Helper()
	registerFakeOfficial()
	dir := t.TempDir()
	g := New(Config{
		MaxAttempts:      1,
		FirstByteTimeout: 5 * time.Second,
		StateTTL:         time.Minute,
		MaxRecords:       50,
		SettingsPath:     filepath.Join(dir, "settings.json"),
	})
	env := provider.NewAccountEnv("fake-1", "fake-official")
	env.Credentials = map[string]string{"account_id": "acc-1", "token": "t"}
	env.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	g.Pool().Add(env)
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	return g, srv, env
}

func officialDo(t *testing.T, srv *httptest.Server, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		buf = bytes.NewReader(raw)
	} else {
		buf = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, srv.URL+path, buf)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestOfficialBindingPauseResume(t *testing.T) {
	_, srv, env := newOfficialGateway(t)

	code, out := officialDo(t, srv, http.MethodGet, "/api/accounts/fake-1/binding", nil)
	if code != 200 || out["official"] != true || out["paused"] != false {
		t.Fatalf("绑定读取不符: %d %v", code, out)
	}

	code, out = officialDo(t, srv, http.MethodPost, "/api/accounts/fake-1/official/pause", map[string]any{"reason": "测试暂停"})
	if code != 200 || out["paused"] != true {
		t.Fatalf("暂停失败: %d %v", code, out)
	}
	if paused, reason, _ := env.OfficialPaused(); !paused || reason != "测试暂停" {
		t.Fatalf("暂停状态未落到账号: paused=%v reason=%q", paused, reason)
	}

	code, out = officialDo(t, srv, http.MethodGet, "/api/accounts/fake-1/binding", nil)
	if out["paused"] != true || out["pause_reason"] != "测试暂停" {
		t.Fatalf("绑定视图未反映暂停: %v", out)
	}

	// 暂停期间账号不参与路由（唯一账号 → 无可用）
	g := srv.Config.Handler.(*Gateway)
	if _, err := g.Pool().Select("fake-official", pool.SelectOptions{}); err == nil {
		t.Fatal("暂停期间不应选出账号")
	}

	code, out = officialDo(t, srv, http.MethodPost, "/api/accounts/fake-1/official/resume", nil)
	if code != 200 || out["paused"] != false {
		t.Fatalf("恢复失败: %d %v", code, out)
	}
	if paused, _, _ := env.OfficialPaused(); paused {
		t.Fatal("恢复后账号仍被暂停")
	}
}

func TestOfficialCheckAuth(t *testing.T) {
	_, srv, env := newOfficialGateway(t)

	// 成功：恢复并标记 ready
	env.SetOfficialPause("先前失败")
	code, out := officialDo(t, srv, http.MethodPost, "/api/accounts/fake-1/official/check", nil)
	if code != 200 || out["ok"] != true || out["paused"] != false {
		t.Fatalf("检查授权应成功: %d %v", code, out)
	}

	// 失败：保持暂停并带原因
	env.SetCredential("quota", "fail")
	env.ResumeOfficial()
	code, out = officialDo(t, srv, http.MethodPost, "/api/accounts/fake-1/official/check", nil)
	if code != 200 || out["ok"] != false || out["paused"] != true {
		t.Fatalf("授权失效时检查应失败并暂停: %d %v", code, out)
	}
	if paused, reason, _ := env.OfficialPaused(); !paused || reason == "" {
		t.Fatal("检查失败后账号未暂停")
	}
}

func TestOfficialAuthFailureAutoPauses(t *testing.T) {
	g, _, env := newOfficialGateway(t)
	g.penalize(env, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "401 unauthorized").WithProvider("fake-official"))
	paused, reason, _ := env.OfficialPaused()
	if !paused || reason == "" {
		t.Fatalf("鉴权失败应自动暂停官方通道: paused=%v reason=%q", paused, reason)
	}
}

func TestOfficialAPIsRejectRelayAccount(t *testing.T) {
	g, srv, _ := newOfficialGateway(t)
	relay := provider.NewAccountEnv("relay-1", "fake-official")
	relay.Credentials = map[string]string{"token": "x"}
	relay.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	g.Pool().Add(relay)

	if code, _ := officialDo(t, srv, http.MethodPost, "/api/accounts/relay-1/official/pause", nil); code != http.StatusConflict {
		t.Fatalf("中转账号暂停应 409: %d", code)
	}
	if code, _ := officialDo(t, srv, http.MethodGet, "/api/accounts/relay-1/binding", nil); code != 200 {
		t.Fatalf("中转账号仍应有绑定视图: %d", code)
	}
}
