package gateway

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/pool"
	"aether-cockpit/internal/core/simplecrypt"
	"aether-cockpit/internal/core/transport"
)

func TestRemoteQuotaEndpoint(t *testing.T) {
	g := &Gateway{pool: pool.New(), settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/remote-quotas",
		strings.NewReader(`{"sources":[{"name":"书房","url":"http://192.168.1.5:7888/","key":"lan-secret","enabled":true}]}`))
	rec := httptest.NewRecorder()
	g.handleRemoteQuotas(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	g.settingsMu.Lock()
	srcs := g.settings.RemoteQuotas
	g.settingsMu.Unlock()
	if len(srcs) != 1 || srcs[0].URL != "http://192.168.1.5:7888" || srcs[0].KeyEnc == "" {
		t.Fatalf("sources = %+v", srcs)
	}
	if strings.Contains(srcs[0].KeyEnc, "lan-secret") {
		t.Fatal("远程 Key 未加密")
	}
	raw, _ := base64.StdEncoding.DecodeString(srcs[0].KeyEnc)
	plain, err := simplecrypt.Decrypt(raw)
	if err != nil || string(plain) != "lan-secret" {
		t.Fatalf("Key 解密 = %q, %v", plain, err)
	}

	// 留空 Key 再保存：沿用旧 Key；URL 必须有效。
	req = httptest.NewRequest(http.MethodPost, "/api/settings/remote-quotas",
		strings.NewReader(`{"sources":[{"name":"书房","url":"http://192.168.1.5:7888","key":"","enabled":true}]}`))
	rec = httptest.NewRecorder()
	g.handleRemoteQuotas(rec, req)
	g.settingsMu.Lock()
	kept := g.settings.RemoteQuotas[0].KeyEnc
	g.settingsMu.Unlock()
	if kept == "" {
		t.Fatal("留空 Key 应沿用旧值")
	}
	req = httptest.NewRequest(http.MethodPost, "/api/settings/remote-quotas",
		strings.NewReader(`{"sources":[{"name":"坏","url":"ftp://x","enabled":true}]}`))
	rec = httptest.NewRecorder()
	g.handleRemoteQuotas(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("坏 URL status = %d", rec.Code)
	}

	// GET 设置：不回显 Key 密文，但要带 hasKey 供编辑器显示"已保存"。
	rawView, _ := json.Marshal(g.settingsView())
	var view struct {
		RemoteQuotas []struct {
			Name   string `json:"name"`
			URL    string `json:"url"`
			HasKey bool   `json:"hasKey"`
			KeyEnc string `json:"key_enc"`
		} `json:"remote_quotas"`
	}
	if err := json.Unmarshal(rawView, &view); err != nil {
		t.Fatalf("settings view: %v", err)
	}
	if len(view.RemoteQuotas) != 1 || !view.RemoteQuotas[0].HasKey || view.RemoteQuotas[0].KeyEnc != "" {
		t.Fatalf("settings remote_quotas = %+v", view.RemoteQuotas)
	}
}

func TestRemoteQuotaFetchAndMerge(t *testing.T) {
	var seenAuth string
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/usage/quotas" {
			http.NotFound(w, r)
			return
		}
		seenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"provider": "claude-sub", "name": "Claude 订阅", "icon": "claude-sub", "user": "Alice",
				"windows": []map[string]any{{"name": "5 hours", "used": 42}}},
			{"provider": "remote-x-123", "name": "别的远程", "windows": []map[string]any{}},
		})
	}))
	defer remote.Close()

	g := &Gateway{pool: pool.New(), settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	enc, _ := simplecrypt.Encrypt([]byte("lan-key"))
	src := RemoteQuotaSource{Name: "书房", URL: remote.URL, KeyEnc: base64.StdEncoding.EncodeToString(enc), Enabled: true}

	cards, errMsg := g.fetchRemoteQuotaCards(src)
	if errMsg != "" {
		t.Fatalf("fetch: %s", errMsg)
	}
	if seenAuth != "Bearer lan-key" {
		t.Fatalf("auth = %q", seenAuth)
	}
	if len(cards) != 1 {
		t.Fatalf("cards = %+v (remote- 前缀的卡片应跳过)", cards)
	}
	c := cards[0]
	if c.Provider != remoteProviderID(src) || c.Name != "书房 · Claude 订阅" || len(c.Windows) != 1 || c.Windows[0].Used != 42 {
		t.Fatalf("card = %+v", c)
	}

	// 集成：首次调用触发后台刷新并返回 reading；稍后重新读取拿到卡片。
	g.settings.RemoteQuotas = []RemoteQuotaSource{src}
	if _, reading := g.remoteQuotaCards(false); !reading {
		t.Fatal("首次应触发后台刷新")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		cards, _ := g.remoteQuotaCards(false)
		if len(cards) == 1 && cards[0].Error == "" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("后台刷新后仍未拿到远程卡片")
}

func TestRemoteQuotaErrorCard(t *testing.T) {
	g := &Gateway{settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	g.settings.RemoteQuotas = []RemoteQuotaSource{{Name: "离线", URL: "http://127.0.0.1:1", Enabled: true}}
	g.remoteQuotaCards(false)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		cards, _ := g.remoteQuotaCards(false)
		if len(cards) == 1 && strings.Contains(cards[0].Error, "连不上远程实例") {
			if cards[0].Windows == nil {
				t.Fatal("错误卡应带空 windows 数组")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("不可达远程没有错误卡")
}

func TestRemoteQuotaConnectionTest(t *testing.T) {
	var seenAuth string
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			http.NotFound(w, r)
			return
		}
		seenAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"accounts":2,"providers":17,"status":"ok"}`))
	}))
	defer remote.Close()

	g := &Gateway{settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/remote-quotas/test",
		strings.NewReader(`{"url":"`+remote.URL+`","key":"lan-key"}`))
	rec := httptest.NewRecorder()
	g.handleRemoteQuotaTest(rec, req)
	var out struct {
		OK        bool   `json:"ok"`
		Accounts  int    `json:"accounts"`
		Providers int    `json:"providers"`
		Error     string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if !out.OK || out.Accounts != 2 || out.Providers != 17 || seenAuth != "Bearer lan-key" {
		t.Fatalf("out = %+v auth = %q", out, seenAuth)
	}

	// 输入框留空时用已保存的 Key（按 URL 匹配）。
	enc, _ := simplecrypt.Encrypt([]byte("saved-key"))
	g.settings.RemoteQuotas = []RemoteQuotaSource{{Name: "A", URL: remote.URL, KeyEnc: base64.StdEncoding.EncodeToString(enc), Enabled: true}}
	seenAuth = ""
	req = httptest.NewRequest(http.MethodPost, "/api/settings/remote-quotas/test",
		strings.NewReader(`{"url":"`+remote.URL+`"}`))
	rec = httptest.NewRecorder()
	g.handleRemoteQuotaTest(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if !out.OK || seenAuth != "Bearer saved-key" {
		t.Fatalf("saved key fallback: out=%+v auth=%q", out, seenAuth)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/settings/remote-quotas/test",
		strings.NewReader(`{"url":"ftp://x"}`))
	rec = httptest.NewRecorder()
	g.handleRemoteQuotaTest(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.OK || !strings.Contains(out.Error, "不是有效的 http(s)") {
		t.Fatalf("bad url out = %+v", out)
	}
}

func TestRemoteUseAsProvider(t *testing.T) {
	g := &Gateway{pool: pool.New(), settingsPath: filepath.Join(t.TempDir(), "settings.json"), tr: transport.NewFactory("")}
	enc, _ := simplecrypt.Encrypt([]byte("lan-key"))
	g.settings.RemoteQuotas = []RemoteQuotaSource{{
		Name: "Study", URL: "http://127.0.0.1:7888",
		KeyEnc: base64.StdEncoding.EncodeToString(enc), Enabled: true,
	}}

	body := `{"name":"Study","url":"http://127.0.0.1:7888"}`
	req := httptest.NewRequest(http.MethodPost, "/api/settings/remote-quotas/use-provider", strings.NewReader(body))
	rec := httptest.NewRecorder()
	g.handleRemoteQuotaUseAsProvider(rec, req)
	var out struct {
		OK      bool   `json:"ok"`
		Created bool   `json:"created"`
		Error   string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if !out.OK || !out.Created {
		t.Fatalf("out = %+v body=%s", out, rec.Body.String())
	}
	envs := g.pool.ListByProvider("openai-compat")
	if len(envs) != 1 || envs[0].GetCredential("base_url") != "http://127.0.0.1:7888/v1" ||
		envs[0].GetCredential("api_key") != "lan-key" {
		t.Fatalf("account = %+v", envs)
	}

	// 重复加入：返回已有账号，不新建。
	req = httptest.NewRequest(http.MethodPost, "/api/settings/remote-quotas/use-provider", strings.NewReader(body))
	rec = httptest.NewRecorder()
	g.handleRemoteQuotaUseAsProvider(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if !out.OK || out.Created || len(g.pool.ListByProvider("openai-compat")) != 1 {
		t.Fatalf("duplicate out = %+v n=%d", out, len(g.pool.ListByProvider("openai-compat")))
	}

	// 没有 Key / 未知实例。
	g.settings.RemoteQuotas[0].KeyEnc = ""
	req = httptest.NewRequest(http.MethodPost, "/api/settings/remote-quotas/use-provider", strings.NewReader(body))
	rec = httptest.NewRecorder()
	g.handleRemoteQuotaUseAsProvider(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "没有保存访问 Key") {
		t.Fatalf("no key status=%d body=%s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/api/settings/remote-quotas/use-provider", strings.NewReader(`{"url":"http://nope:1"}`))
	rec = httptest.NewRecorder()
	g.handleRemoteQuotaUseAsProvider(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown status = %d", rec.Code)
	}
}

func TestRemoteQuotaRenameRefetches(t *testing.T) {
	g := &Gateway{settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	old := RemoteQuotaSource{Name: "Old", URL: "http://127.0.0.1:1", Enabled: true}
	g.remoteCache = map[string]remoteQuotaEntry{
		remoteQuotaCacheKey(old): {at: time.Now(), cards: []quotaCard{{Name: "旧名字卡片", Windows: []quotaCardWindow{}}}},
	}
	g.remoteBusy = map[string]bool{}
	g.settings.RemoteQuotas = []RemoteQuotaSource{{Name: "New", URL: old.URL, Enabled: true}}

	cards, reading := g.remoteQuotaCards(false)
	if len(cards) != 0 {
		t.Fatalf("改名后不应复用旧缓存: %+v", cards)
	}
	if !reading {
		t.Fatal("改名后应触发重新拉取")
	}
}
