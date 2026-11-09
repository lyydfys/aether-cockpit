package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/simplecrypt"
	"aether-cockpit/internal/provider"
)

// providerBalanceHTTPTimeout 给余额接口一个短上限：它只用于展示。
const providerBalanceHTTPTimeout = 12 * time.Second

// balanceAccountKey 是账号级余额配置的键：渠道#账号（小写）。
// 账号为空时就是渠道级配置。
func balanceAccountKey(pid, accountID string) string {
	pid = strings.TrimSpace(pid)
	accountID = strings.ToLower(strings.TrimSpace(accountID))
	if accountID == "" {
		return pid
	}
	return pid + "#" + accountID
}

// providerBalanceConfigFor 账号级优先，缺失时回落到渠道级。
func (g *Gateway) providerBalanceConfigFor(pid, accountID string) (ProviderBalanceConfig, bool) {
	if strings.TrimSpace(accountID) != "" {
		if cfg, ok := g.providerBalanceConfig(balanceAccountKey(pid, accountID)); ok {
			return cfg, true
		}
	}
	return g.providerBalanceConfig(pid)
}

// providerBalanceConfig 读取渠道的余额配置。
func (g *Gateway) providerBalanceConfig(id string) (ProviderBalanceConfig, bool) {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	cfg, ok := g.settings.ProviderBalances[id]
	return cfg, ok && strings.TrimSpace(cfg.URL) != ""
}

// providerBalanceConfigView 返回渠道的余额配置（token 仅给"已保存"标记）。
func (g *Gateway) providerBalanceConfigView(id string) (urlStr, path, method string, tokenSet bool) {
	cfg, ok := g.providerBalanceConfig(id)
	if !ok {
		return "", "", "", false
	}
	return cfg.URL, cfg.Path, cfg.Method, cfg.TokenEnc != ""
}

// queryConfiguredBalance 用保存的余额接口读一次，折算成 QuotaStatus。
// 没有渠道用量 API 的供应商（Qoder 等）也能在用量页显示余额。
func (g *Gateway) queryConfiguredBalance(ctx context.Context, env *provider.AccountEnv, cfg ProviderBalanceConfig) (*provider.QuotaStatus, error) {
	accountKey := strings.TrimSpace(env.GetCredential("api_key"))
	if accountKey == "" {
		accountKey = strings.TrimSpace(env.GetCredential("access_token"))
	}
	token := ""
	if cfg.TokenEnc != "" {
		if raw, err := base64.StdEncoding.DecodeString(cfg.TokenEnc); err == nil {
			if plain, err := simplecrypt.Decrypt(raw); err == nil {
				token = string(plain)
			}
		}
	}
	if token == "" {
		token = strings.TrimSpace(env.GetCredential("api_key"))
		if token == "" {
			token = strings.TrimSpace(env.GetCredential("access_token"))
		}
	}
	amount, unit, _, parts, errMsg := g.fetchProviderBalance(ctx, env.Client(), cfg.Method, cfg.URL, cfg.Path, token, accountKey, nil)
	if errMsg != "" {
		return nil, &balanceConfigError{msg: errMsg}
	}
	if unit == "" {
		// 自定义余额接口没有货币/百分比标记时，按积分展示（本应用的常见口径）。
		unit = "credits"
	}
	// 哨兵值：很多中转站的 billing 接口用 `hard_limit_usd: 1e8` 之类的大数表示"无限额度"，
	// 真实用量只有几块钱。照原样展示会得到"剩余 $99999999.04 / 上限 $100000000.00"这种
	// 明显是假的读数，用户会以为余额读错了。这类数值没有任何信息量，直接判为"读不到"。
	if implausibleBalanceSentinel(amount) {
		return nil, &balanceConfigError{msg: "余额接口返回的是「无限额度」哨兵值，没有真实余额可读；" +
			"可以改一个能返回真实余额的字段表达式（如 $data.total_available）"}
	}
	q := &provider.QuotaStatus{
		Balance: amount, BalanceUnit: unit, ReadAt: time.Now().Unix(),
	}
	for _, p := range parts {
		q.BalanceParts = append(q.BalanceParts, provider.QuotaBalancePart{Label: p.label, Text: p.text})
	}
	return q, nil
}

type balanceConfigError struct{ msg string }

// implausibleBalanceSentinel 判断余额数值是不是"无限额度"占位而不是真实余额。
//
// 判据是量级而不是具体数字：中转站常用的哨兵是 100000000（1e8）、9999999、1e9 等，
// 真实个人账号的余额不会到 1e6 美元/积分这个量级。取 1e6 作为分界：
//   - 中转站/个人用户真实余额在个位到几百区间；
//   - 团队充值或自建部署的极端情况也远低于 1e6；
//   - 而 1e8 一类的哨兵一定 >= 1e6。
//
// 同时把非有限值（NaN/Inf）也算作不可信：表达式算错会得到这种值，不能让它们进 UI。
func implausibleBalanceSentinel(v float64) bool {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return true
	}
	return v >= 1e6
}

func (e *balanceConfigError) Error() string { return e.msg }

// saveProviderBalanceLocked 写入余额配置；token 本机绑定加密。
// 调用方必须已持有 settingsMu。
func (g *Gateway) saveProviderBalanceLocked(id string, cfg ProviderBalanceConfig, token string, clearToken bool) error {
	if g.settings.ProviderBalances == nil {
		g.settings.ProviderBalances = map[string]ProviderBalanceConfig{}
	}
	if cfg.TokenEnc == "" {
		if old, ok := g.settings.ProviderBalances[id]; ok {
			cfg.TokenEnc = old.TokenEnc
		}
	}
	if clearToken {
		cfg.TokenEnc = ""
	}
	if token != "" {
		enc, err := simplecrypt.Encrypt([]byte(token))
		if err != nil {
			return err
		}
		cfg.TokenEnc = base64.StdEncoding.EncodeToString(enc)
	}
	if cfg.URL == "" && cfg.Path == "" && cfg.TokenEnc == "" {
		delete(g.settings.ProviderBalances, id)
	} else {
		g.settings.ProviderBalances[id] = cfg
	}
	return g.saveSettings()
}

// handleProviderBalance POST /api/provider/balance
//
// 两条路：
//  1. 用户给了 Balance URL（aether 的 balanceURL/balancePath）：按表达式从
//     接口回复里取余额/积分，支持 5h: a.used / a.cap %; $credits.left 这类写法；
//  2. 没配置：退回渠道自己的用量接口（QuotaCapability）。
//
// 读不到就如实报错，不把 0 当余额。
func (g *Gateway) handleProviderBalance(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID       string            `json:"id"`
		URL      string            `json:"balanceURL"`
		Path     string            `json:"balancePath"`
		Method   string            `json:"balanceMethod"`
		Headers  map[string]string `json:"headers"`
		Token    string            `json:"balanceToken"`
		ClearTok bool              `json:"clearBalanceToken"`
		Account  string            `json:"account"`
		ClearCfg bool              `json:"clearBalance"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	id := strings.TrimSpace(body.ID)

	// 账号（用于代理与默认密钥）；渠道未注册时也可能有自定义配置。
	var env *provider.AccountEnv
	if g.pool != nil {
		for _, e := range g.pool.ListByProvider(id) {
			if e.Available() {
				env = e
				break
			}
		}
		if env == nil {
			if list := g.pool.ListByProvider(id); len(list) > 0 {
				env = list[0]
			}
		}
		// 指定账号时优先用它（可能是停用/耗尽账号，用户主动查它）。
		if acc := strings.TrimSpace(body.Account); acc != "" {
			if e, ok := g.pool.Get(acc); ok {
				env = e
			}
		}
	}

	urlStr := strings.TrimSpace(body.URL)
	path := strings.TrimSpace(body.Path)
	method := strings.ToUpper(strings.TrimSpace(body.Method))

	cfgKey := balanceAccountKey(id, body.Account)

	// 显式清除该账号（或渠道）的余额配置。
	if body.ClearCfg {
		g.settingsMu.Lock()
		delete(g.settings.ProviderBalances, cfgKey)
		if strings.TrimSpace(body.Account) == "" {
			// 清渠道级时顺带清掉该渠道所有账号级覆盖，避免“清了还在”。
			for k := range g.settings.ProviderBalances {
				if strings.HasPrefix(k, id+"#") {
					delete(g.settings.ProviderBalances, k)
				}
			}
		}
		err := g.saveSettings()
		g.settingsMu.Unlock()
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "清除余额配置失败：" + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared": true})
		return
	}

	// 本次没给 URL：用已保存的配置（账号级优先）。
	if urlStr == "" {
		if cfg, ok := g.providerBalanceConfigFor(id, body.Account); ok {
			urlStr, path, method = cfg.URL, cfg.Path, cfg.Method
		}
	}

	if urlStr == "" {
		g.providerQuotaBalance(w, r, id, env)
		return
	}

	u, err := url.Parse(urlStr)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "Balance URL 不是有效的 http(s) 地址"})
		return
	}

	// 令牌：本次输入的优先，其次已保存的，最后用账号密钥。
	token := strings.TrimSpace(body.Token)
	if token == "" && !body.ClearTok {
		g.settingsMu.Lock()
		cfg := g.settings.ProviderBalances[cfgKey]
		if cfg.TokenEnc == "" {
			cfg = g.settings.ProviderBalances[id]
		}
		g.settingsMu.Unlock()
		if cfg.TokenEnc != "" {
			if raw, err := base64.StdEncoding.DecodeString(cfg.TokenEnc); err == nil {
				if plain, err := simplecrypt.Decrypt(raw); err == nil {
					token = string(plain)
				}
			}
		}
	}
	if token == "" && env != nil {
		token = strings.TrimSpace(env.GetCredential("api_key"))
		if token == "" {
			token = strings.TrimSpace(env.GetCredential("access_token"))
		}
	}
	accountKey := ""
	if env != nil {
		accountKey = strings.TrimSpace(env.GetCredential("api_key"))
		if accountKey == "" {
			accountKey = strings.TrimSpace(env.GetCredential("access_token"))
		}
	}
	client := http.DefaultClient
	if env != nil {
		client = env.Client()
	}

	ctx, cancel := context.WithTimeout(r.Context(), providerBalanceHTTPTimeout)
	defer cancel()
	amount, unit, text, _, fetchErr := g.fetchProviderBalance(ctx, client, method, urlStr, path, token, accountKey, body.Headers)
	if fetchErr != "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": fetchErr})
		return
	}

	// 检查成功：把 URL / 字段 / 令牌存下来，刷新页面后仍可复现。
	g.settingsMu.Lock()
	if err := g.saveProviderBalanceLocked(cfgKey, ProviderBalanceConfig{
		URL: urlStr, Path: path, Method: method,
	}, strings.TrimSpace(body.Token), body.ClearTok); err != nil {
		g.settingsMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "保存余额配置失败：" + err.Error()})
		return
	}
	g.settingsMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "amount": text, "balance": amount, "unit": unit,
	})
}

// fetchProviderBalance 发请求并按表达式求值。error 文案直接给用户看。
func (g *Gateway) fetchProviderBalance(ctx context.Context, client *http.Client, method, urlStr, path, token, accountKey string, headers map[string]string) (float64, string, string, []balanceSegment, string) {
	// aether 的 {key} 占位符：地址与请求头里替换为该账号自己的 Key
	// （系统令牌只用于 Authorization，不能替代 per-key 余额接口的 key 参数）。
	if strings.Contains(urlStr, "{key}") || hasKeyPlaceholder(headers) {
		if accountKey == "" {
			accountKey = token
		}
		if accountKey == "" {
			return 0, "", "", nil, "余额地址需要 {key}，但这个账号没有可用的 Key"
		}
		urlStr = strings.ReplaceAll(urlStr, "{key}", url.QueryEscape(accountKey))
	}
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if method == http.MethodPost {
		body = bytes.NewReader([]byte("{}"))
	}
	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return 0, "", "", nil, "构造余额请求失败：" + err.Error()
	}
	req.Header.Set("Accept", "application/json, text/plain;q=0.9")
	req.Header.Set("User-Agent", "AetherCockpit/balance")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// 用户自定义头优先（可以覆盖 Authorization）。
	for k, v := range headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		req.Header.Set(k, strings.ReplaceAll(v, "{key}", accountKey))
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", "", nil, "余额接口连接失败：" + err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 160 {
			msg = msg[:160]
		}
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return 0, "", "", nil, "余额接口返回 HTTP " + strconv.Itoa(resp.StatusCode) + "：" + msg
	}

	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		trimmed := strings.TrimSpace(string(raw))
		if len(trimmed) > 120 {
			trimmed = trimmed[:120]
		}
		return 0, "", "", nil, "余额接口回复不是 JSON：" + trimmed
	}
	v, ok := evalBalanceExpr(path, root, "data.balance")
	if !ok {
		return 0, "", "", nil, "余额字段没出现在回复里：" + defaultIfEmpty(path, "data.balance") + "（可换一个字段表达式，如 $data.quota / 500000）"
	}
	text := v.all
	if text == "" {
		text = v.text
	}
	return v.amount, v.unit, text, v.parts, ""
}

// providerQuotaBalance 是旧的"渠道自带用量接口"路径。
func (g *Gateway) providerQuotaBalance(w http.ResponseWriter, r *http.Request, id string, env *provider.AccountEnv) {
	p, ok := provider.Get(id)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "该渠道没有配置 Balance URL，也不是已注册渠道"})
		return
	}
	cap, ok := provider.AsQuota(p)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "该渠道不提供余额；可以填 Balance URL 让网关去问余额接口"})
		return
	}
	if env == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "没有可用账号可查询余额"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	q, err := cap.QueryQuota(ctx, env)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if q == nil || (q.Balance <= 0 && q.Total <= 0) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "该渠道没有返回余额"})
		return
	}
	amount := q.Balance
	unit := q.BalanceUnit
	if amount <= 0 {
		amount = float64(q.Remaining)
		unit = q.Unit
	}
	if implausibleBalanceSentinel(amount) {
		// 与自定义余额接口同一判据：1e8 一类的"无限额度"占位不是真实余额。
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "渠道返回的是「无限额度」哨兵值，没有真实余额可读"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "amount": trimFloat(amount, 2), "unit": unit})
}

func hasKeyPlaceholder(headers map[string]string) bool {
	for _, v := range headers {
		if strings.Contains(v, "{key}") {
			return true
		}
	}
	return false
}

func defaultIfEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
