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
	"aether-cockpit/internal/provider"

	_ "aether-cockpit/internal/providers/openaicompat"
)

func TestProviderBalanceCustomURL(t *testing.T) {
	var seenAuth, seenMethod, seenKeyQuery, seenKeyHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenMethod = r.Method
		seenKeyQuery = r.URL.Query().Get("key")
		seenKeyHeader = r.Header.Get("X-Acct-Key")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"quota": 2500000, "balance": 12.5},
		})
	}))
	defer srv.Close()

	g := &Gateway{
		pool:         pool.New(),
		settingsPath: filepath.Join(t.TempDir(), "settings.json"),
	}
	env := provider.NewAccountEnv("acc-bal", "openai-compat")
	env.Credentials = map[string]string{"api_key": "sk-account"}
	env.HTTPClient = srv.Client()
	g.pool.Add(env)

	body := `{"id":"openai-compat","balanceURL":"` + srv.URL + `/balance?key={key}","balancePath":"$data.quota / 500000","balanceMethod":"POST","balanceToken":"sys-token","headers":{"X-Acct-Key":"{key}"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/provider/balance", strings.NewReader(body))
	rec := httptest.NewRecorder()
	g.handleProviderBalance(rec, req)

	var resp struct {
		OK      bool    `json:"ok"`
		Amount  string  `json:"amount"`
		Balance float64 `json:"balance"`
		Unit    string  `json:"unit"`
		Error   string  `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析失败: %v: %s", err, rec.Body.String())
	}
	if !resp.OK || resp.Balance != 5 || resp.Amount != "$5" || resp.Unit != "USD" {
		t.Fatalf("resp = %+v", resp)
	}
	if seenAuth != "Bearer sys-token" || seenMethod != http.MethodPost {
		t.Fatalf("request auth=%q method=%q", seenAuth, seenMethod)
	}
	// {key} 用账号自己的 Key（而不是系统令牌）替换，URL 与请求头都生效。
	if seenKeyQuery != "sk-account" || seenKeyHeader != "sk-account" {
		t.Fatalf("{key} = query %q header %q, want sk-account", seenKeyQuery, seenKeyHeader)
	}

	// 配置与令牌落盘：令牌必须是密文。
	g.settingsMu.Lock()
	cfg := g.settings.ProviderBalances["openai-compat"]
	g.settingsMu.Unlock()
	if cfg.URL != srv.URL+"/balance?key={key}" || cfg.Path != "$data.quota / 500000" || cfg.Method != "POST" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.TokenEnc == "" || strings.Contains(cfg.TokenEnc, "sys-token") {
		t.Fatalf("token 未加密保存: %q", cfg.TokenEnc)
	}
	raw, err := base64.StdEncoding.DecodeString(cfg.TokenEnc)
	if err != nil {
		t.Fatalf("token base64: %v", err)
	}
	plain, err := simplecrypt.Decrypt(raw)
	if err != nil || string(plain) != "sys-token" {
		t.Fatalf("token 解密 = %q, %v", plain, err)
	}

	// 第二次不带 URL：用已保存的配置与令牌。
	seenAuth = ""
	req = httptest.NewRequest(http.MethodPost, "/api/provider/balance", strings.NewReader(`{"id":"openai-compat"}`))
	rec = httptest.NewRecorder()
	g.handleProviderBalance(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !resp.OK || resp.Amount != "$5" || seenAuth != "Bearer sys-token" {
		t.Fatalf("saved 配置未生效: %+v auth=%q", resp, seenAuth)
	}
}

func TestProviderBalanceHonestErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
			return
		}
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	defer srv.Close()

	g := &Gateway{pool: pool.New(), settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	env := provider.NewAccountEnv("acc-bal2", "openai-compat")
	env.HTTPClient = srv.Client()
	g.pool.Add(env)

	cases := []struct{ body, want string }{
		{`{"id":"openai-compat","balanceURL":"` + srv.URL + `/ok","balancePath":"data.missing"}`, "余额字段没出现在回复里"},
		{`{"id":"openai-compat","balanceURL":"` + srv.URL + `/bad"}`, "HTTP 500"},
		{`{"id":"openai-compat","balanceURL":"ftp://x/y"}`, "不是有效的 http(s) 地址"},
		{`{"id":"openai-compat","balanceURL":"` + srv.URL + `/ok?key={key}"}`, "没有可用的 Key"},
		{`{"id":"nope"}`, "没有配置 Balance URL"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/api/provider/balance", strings.NewReader(c.body))
		rec := httptest.NewRecorder()
		g.handleProviderBalance(rec, req)
		var resp struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if resp.OK || !strings.Contains(resp.Error, c.want) {
			t.Fatalf("%s → %+v", c.body, resp)
		}
	}
}

// 配置了余额接口后，用量卡直接用它，不再走渠道内置的账单探测。
func TestConfiguredBalanceFeedsQuotaCard(t *testing.T) {
	var billingHits int
	var seenKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/dashboard/billing/") {
			billingHits++
		}
		seenKey = r.URL.Query().Get("key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"credits":{"left":88.5,"used":12.5}}`))
	}))
	defer srv.Close()

	g := &Gateway{pool: pool.New(), settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	env := provider.NewAccountEnv("acc-card", "openai-compat")
	env.Label = "中继"
	env.Credentials = map[string]string{"api_key": "sk-x"}
	env.HTTPClient = srv.Client()
	g.pool.Add(env)

	g.settingsMu.Lock()
	g.settings.ProviderBalances = map[string]ProviderBalanceConfig{
		"openai-compat": {URL: srv.URL + "/balance?key={key}", Path: "$credits.left; pct: credits.used %"},
	}
	g.settingsMu.Unlock()

	if err := g.readQuotaFor(env, 3*time.Second); err != nil {
		t.Fatalf("readQuotaFor: %v", err)
	}
	q := env.Quota()
	if q == nil || q.Balance != 88.5 || q.BalanceUnit != "USD" {
		t.Fatalf("q = %+v", q)
	}
	if billingHits != 0 {
		t.Fatalf("配置余额接口时不应再探测账单接口: %d", billingHits)
	}
	if seenKey != "sk-x" {
		t.Fatalf("用量读取的 {key} = %q, want 账号自己的 Key", seenKey)
	}
	cards, _ := g.usageQuotaCards()
	if len(cards) != 1 || cards[0].Balance != "$88.5" {
		t.Fatalf("cards = %+v", cards)
	}
	if len(cards[0].BalanceParts) != 2 ||
		cards[0].BalanceParts[0].Text != "$88.5" ||
		cards[0].BalanceParts[1].Label != "pct" || cards[0].BalanceParts[1].Text != "12.5%" {
		t.Fatalf("balanceParts = %+v", cards[0].BalanceParts)
	}
}

// 中继类渠道必须能打开「Account balance」区块：它们有内置配额能力，
// 但账单接口经常不存在，需要自定义 Balance URL 兜底。
// 这里直接钉规则函数，避免在 gateway 测试包里再注册一批渠道
// （全局注册会改变其它用例的模型清单）。
func TestProviderBalanceFieldVisibility(t *testing.T) {
	cases := []struct {
		id       string
		hasQuota bool
		want     bool
	}{
		{"codex", true, true},
		{"openai-compat", true, true},
		{"opencode", true, true},
		{"qoder-cn", false, true},
		{"qoder-intl", false, true},
		{"antigravity", true, false},
		{"claude-sub", true, false},
		{"minimax-code", true, false},
		{"kiro", true, false},
	}
	for _, c := range cases {
		if got := balanceTakesToken(c.id, c.hasQuota); got != c.want {
			t.Fatalf("balanceTakesToken(%q,%v) = %v, want %v", c.id, c.hasQuota, got, c.want)
		}
	}
}

// 账号级余额配置优先于渠道级；清除只删指定账号。
func TestAccountScopedBalanceConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		left := 10.0
		if r.URL.Path == "/account" {
			left = 20.0
		} else if r.URL.Path == "/account-two" {
			left = 30.0
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"credits": map[string]any{"left": left}})
	}))
	defer srv.Close()

	g := &Gateway{pool: pool.New(), settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	e1 := provider.NewAccountEnv("acc-one", "openai-compat")
	e1.HTTPClient = srv.Client()
	e2 := provider.NewAccountEnv("acc-two", "openai-compat")
	e2.HTTPClient = srv.Client()
	g.pool.Add(e1)
	g.pool.Add(e2)

	g.settingsMu.Lock()
	g.settings.ProviderBalances = map[string]ProviderBalanceConfig{
		"openai-compat":         {URL: srv.URL + "/provider", Path: "$credits.left"},
		"openai-compat#acc-two": {URL: srv.URL + "/account", Path: "$credits.left"},
	}
	g.settingsMu.Unlock()

	if err := g.readQuotaFor(e1, 3*time.Second); err != nil {
		t.Fatalf("e1: %v", err)
	}
	if q := e1.Quota(); q == nil || q.Balance != 10 {
		t.Fatalf("e1 quota = %+v", q)
	}
	if err := g.readQuotaFor(e2, 3*time.Second); err != nil {
		t.Fatalf("e2: %v", err)
	}
	if q := e2.Quota(); q == nil || q.Balance != 20 {
		t.Fatalf("e2 quota = %+v", q)
	}

	// 清除账号级配置：只删该账号，渠道级保留。
	req := httptest.NewRequest(http.MethodPost, "/api/provider/balance",
		strings.NewReader(`{"id":"openai-compat","account":"acc-two","clearBalance":true}`))
	rec := httptest.NewRecorder()
	g.handleProviderBalance(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"cleared":true`) {
		t.Fatalf("clear = %d %s", rec.Code, rec.Body.String())
	}
	g.settingsMu.Lock()
	_, hasAccount := g.settings.ProviderBalances["openai-compat#acc-two"]
	_, hasProvider := g.settings.ProviderBalances["openai-compat"]
	g.settingsMu.Unlock()
	if hasAccount || !hasProvider {
		t.Fatalf("清除范围不对: account=%v provider=%v", hasAccount, hasProvider)
	}
}
