package reverse

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/relaybilling"
)

// openCodeGoUsageURL 是 OpenCode Go 订阅的用量端点。
// 聊天走 /zen/v1，而 Go 的滚动窗口在 /zen/go/v1 下（对齐 高并发网关）。
const openCodeGoUsageURL = "https://opencode.ai/zen/go/v1/usage"

// QueryQuota 读取逆向渠道账号的剩余额度。
//
// 两种口径：
//   - relay：中继/聚合面板的账单接口，折算成"剩余 $X"；
//   - opencode_go：Go 订阅的滚动窗口（百分比）。
//
// 读不到就如实报错，绝不编造额度。
func (p *Provider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	switch p.profile.QuotaKind {
	case "codex":
		// ChatGPT 订阅账号（有 account_id）读 Codex 自己的用量；中转账号没有
		// account_id，退回面板账单。若账号带 account_id 但上游不是 ChatGPT
		// （混合/网关账号），wham 失败后再试面板账单，最后如实报 wham 的原因。
		if strings.TrimSpace(env.GetCredential("account_id")) != "" {
			q, err := p.queryCodexWham(ctx, env)
			if err == nil {
				return q, nil
			}
			base := p.usageBase(env)
			if base != "" && !isChatGPTBase(base) {
				if rq, rerr := relaybilling.Query(ctx, env, base); rerr == nil {
					return rq, nil
				}
			}
			return nil, err
		}
		return relaybilling.Query(ctx, env, p.usageBase(env))
	case "relay":
		return relaybilling.Query(ctx, env, p.usageBase(env))
	case "opencode_go":
		return p.queryOpenCodeGo(ctx, env)
	}
	return nil, ir.NewAPIError(ir.ErrCodeBadRequest,
		"渠道 "+p.profile.ID+" 没有可用的用量接口").WithProvider(p.profile.ID)
}

// usageBase 复用聊天路径的 base_url 优先级：账号 → spec → profile 默认。
func (p *Provider) usageBase(env *provider.AccountEnv) string {
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		if sp := spec.Load(p.profile.ID); sp != nil {
			base = strings.TrimRight(strings.TrimSpace(sp.BaseURL), "/")
		}
	}
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(p.profile.DefaultBaseURL), "/")
	}
	return base
}

// applySubscriptionHeaders 给订阅型渠道补账号级请求头（Codex ChatGPT）。
// 只填空缺，spec 里显式配置的头优先。
func applySubscriptionHeaders(r *http.Request, providerID string, env *provider.AccountEnv) {
	if providerID != "codex" {
		return
	}
	accountID := strings.TrimSpace(env.GetCredential("account_id"))
	if accountID == "" {
		return
	}
	if r.Header.Get("chatgpt-account-id") == "" {
		r.Header.Set("chatgpt-account-id", accountID)
	}
	if r.Header.Get("originator") == "" {
		r.Header.Set("originator", "codex_cli_rs")
	}
}

// isChatGPTBase 判断 base 是否官方 ChatGPT 端点（这类账号没有面板账单）。
func isChatGPTBase(base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "chatgpt.com" || strings.HasSuffix(host, ".chatgpt.com") ||
		host == "openai.com" || strings.HasSuffix(host, ".openai.com")
}

// queryCodexWham 读 ChatGPT 订阅的 Codex 用量（aether 的 codexWindows）：
// GET {chatgpt}/backend-api/wham/usage，Bearer + chatgpt-account-id。
func (p *Provider) queryCodexWham(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	token := strings.TrimSpace(env.GetCredential("access_token"))
	if token == "" {
		token = strings.TrimSpace(env.GetCredential("api_key"))
	}
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Codex 账号缺少 access_token").WithProvider(p.profile.ID)
	}
	accountID := strings.TrimSpace(env.GetCredential("account_id"))
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		base = "https://chatgpt.com/backend-api/codex"
	}
	base = strings.TrimSuffix(base, "/codex")

	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/wham/usage", nil)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 Codex 用量请求失败").WithCause(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("chatgpt-account-id", accountID)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", codexUserAgent())

	resp, err := env.Client().Do(req)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "读取 Codex 用量失败："+err.Error()).WithProvider(p.profile.ID)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 160 {
			msg = msg[:160]
		}
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"读取 Codex 用量失败：HTTP "+strconv.Itoa(resp.StatusCode)+"："+msg).WithProvider(p.profile.ID)
	}
	return parseCodexWham(raw)
}

// parseCodexWham 解析 /wham/usage（形状取 aether 的 codexUsage）。
func parseCodexWham(raw []byte) (*provider.QuotaStatus, error) {
	var u struct {
		PlanType  string `json:"plan_type"`
		RateLimit struct {
			Primary   *codexWindow `json:"primary_window"`
			Secondary *codexWindow `json:"secondary_window"`
		} `json:"rate_limit"`
		Credits *struct {
			Has       bool   `json:"has_credits"`
			Unlimited bool   `json:"unlimited"`
			Balance   string `json:"balance"`
		} `json:"credits"`
		ResetCredits *struct {
			Available int `json:"available_count"`
		} `json:"rate_limit_reset_credits"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return nil, err
	}
	q := &provider.QuotaStatus{ReadAt: time.Now().Unix(), Plan: u.PlanType}
	for _, w := range []*codexWindow{u.RateLimit.Primary, u.RateLimit.Secondary} {
		if w == nil {
			continue
		}
		qw := provider.QuotaWindow{Name: codexWindowName(w.LimitSeconds), Used: clampPercent(w.UsedPercent)}
		if w.ResetAt > 0 {
			qw.ResetsAt = w.ResetAt
		} else if w.ResetAfterSeconds > 0 {
			qw.ResetsAt = time.Now().Unix() + w.ResetAfterSeconds
		}
		if qw.Name == "" {
			qw.Name = "Quota"
		}
		q.Windows = append(q.Windows, qw)
	}
	if r := u.ResetCredits; r != nil && r.Available > 0 {
		q.Resets = &provider.QuotaResets{Count: r.Available}
	}
	if c := u.Credits; c != nil && c.Has && !c.Unlimited {
		if n, err := strconv.ParseFloat(strings.TrimSpace(c.Balance), 64); err == nil && n > 0 {
			q.Balance = n
			q.BalanceUnit = "credits"
		}
	} else if c != nil && c.Unlimited {
		q.Message = "额度之外还有不限量积分"
	}
	if len(q.Windows) == 0 && q.Balance <= 0 {
		return nil, &usageParseError{"Codex 用量回复里没有窗口或积分：" + snippet(raw)}
	}
	return q, nil
}

type codexWindow struct {
	UsedPercent       float64 `json:"used_percent"`
	LimitSeconds      int64   `json:"limit_window_seconds"`
	ResetAfterSeconds int64   `json:"reset_after_seconds"`
	ResetAt           int64   `json:"reset_at"`
}

// codexWindowName 把秒数折成用量页认识的名字（5 小时 / 7 天）。
func codexWindowName(seconds int64) string {
	switch {
	case seconds <= 0:
		return ""
	case seconds <= 6*3600:
		return "5 hours"
	case seconds <= 8*24*3600:
		return "7 days"
	default:
		return strconv.FormatInt(seconds/86400, 10) + " days"
	}
}

func (p *Provider) queryOpenCodeGo(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	key := strings.TrimSpace(env.GetCredential("api_key"))
	if key == "" {
		key = strings.TrimSpace(env.GetCredential("access_token"))
	}
	if key == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"账号缺少 api_key，无法读取 OpenCode Go 用量").WithProvider(p.profile.ID)
	}

	endpoint := openCodeGoUsageURL
	if base := p.usageBase(env); base != "" && !strings.HasSuffix(base, "/zen/v1") {
		endpoint = base + "/usage"
	}

	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 OpenCode 用量请求失败").WithCause(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", codexUserAgent())

	resp, err := env.Client().Do(req)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"读取 OpenCode Go 用量失败："+err.Error()).WithProvider(p.profile.ID)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"该 key 不是 OpenCode Go 订阅（HTTP "+strconv.Itoa(resp.StatusCode)+"）").WithProvider(p.profile.ID)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"读取 OpenCode Go 用量失败：HTTP "+strconv.Itoa(resp.StatusCode)).WithProvider(p.profile.ID)
	}

	q, err := parseOpenCodeUsage(raw)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, err.Error()).WithProvider(p.profile.ID)
	}
	q.Plan = "Go"
	q.ReadAt = time.Now().Unix()
	return q, nil
}

// parseOpenCodeUsage 解析 Go 用量回复。
//
// 上游字段名没有公开稳定契约（aether 也只认 rolling 形状），所以这里按
// 结构做宽容匹配：滚动窗口按名字识别，百分比/已用+上限/重置时间都接受
// 多种拼写；一个窗口都认不出来时返回带原文片段的错误，而不是空卡片。
func parseOpenCodeUsage(raw []byte) (*provider.QuotaStatus, error) {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, &usageParseError{"OpenCode Go 的回复不是 JSON：" + snippet(raw)}
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return nil, &usageParseError{"OpenCode Go 的回复不是对象：" + snippet(raw)}
	}

	q := &provider.QuotaStatus{}
	seen := map[string]bool{}
	var windows []provider.QuotaWindow

	var walk func(prefix string, m map[string]any)
	walk = func(prefix string, m map[string]any) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v, ok := m[k].(map[string]any)
			if !ok {
				continue
			}
			name, known := windowNameOf(k)
			if name == "" {
				name = strings.TrimSpace(k)
			}
			if w, ok := windowFrom(name, known, v); ok {
				if !seen[w.Name] {
					seen[w.Name] = true
					windows = append(windows, w)
				}
				continue
			}
			// 容器（rolling / limits / usage …）继续下钻一层。
			walk(prefix+k+".", v)
		}
	}
	walk("", obj)

	if len(windows) > 0 {
		q.Windows = windows
		q.Message = "OpenCode Go 滚动窗口"
	}
	if q.Balance == 0 {
		for _, k := range []string{"balance", "credits", "remaining_credits", "credit_balance"} {
			if f, ok := numberField(obj, k); ok && f > 0 {
				q.Balance = f
				q.BalanceUnit = "credits"
				break
			}
		}
	}
	if len(q.Windows) == 0 && q.Balance == 0 {
		return nil, &usageParseError{"OpenCode Go 的回复里没有可识别的滚动窗口：" + snippet(raw)}
	}
	return q, nil
}

// windowNameOf 把上游 key 归一成前端可读的窗口名。
// 名字必须匹配 "^<数字> (hour|day|week)s?$" 才能参与池线渲染。
func windowNameOf(key string) (string, bool) {
	k := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", "-"), " ", "-"))
	switch {
	case strings.Contains(k, "rolling"), strings.Contains(k, "five-hour"), strings.Contains(k, "5h"), strings.Contains(k, "hourly"):
		return "5 hours", true
	case strings.Contains(k, "weekly"), strings.Contains(k, "seven-day"), strings.Contains(k, "7d"), strings.Contains(k, "week"):
		return "7 days", true
	case strings.Contains(k, "monthly"), strings.Contains(k, "thirty-day"), strings.Contains(k, "30d"), strings.Contains(k, "month"):
		return "30 days", true
	}
	return "", false
}

// windowFrom 从对象里读出窗口读数。
func windowFrom(name string, known bool, m map[string]any) (provider.QuotaWindow, bool) {
	// 停用/未激活的窗口跳过；rate-limited / exhausted 是真实打到上限的窗口
	// （OpenCode Go 的 monthly 就是这种状态），必须保留，否则用户看不到额度已用完。
	if st, ok := m["status"].(string); ok {
		st = strings.ToLower(strings.TrimSpace(st))
		switch st {
		case "", "ok", "active", "rate-limited", "rate_limited", "limited", "exhausted":
		default:
			return provider.QuotaWindow{}, false
		}
	}
	used, ok := windowUsedPercent(m)
	if !ok {
		return provider.QuotaWindow{}, false
	}
	// 未知 key 必须是"像窗口"的对象：显式百分比、已用+上限，或有重置时间。
	if !known && !hasExplicitPercent(m) && !looksLikeWindow(m) {
		return provider.QuotaWindow{}, false
	}
	w := provider.QuotaWindow{Name: name, Used: used}
	if !known {
		// 按模型给出的窗口交给前端放进"模型池"。
		w.Family = modelFamily(name)
		w.Pool = "Models"
	}
	w.ResetsAt = resetOf(m)
	return w, true
}

func hasExplicitPercent(m map[string]any) bool {
	for _, k := range []string{"used_percent", "usedPercent", "usage_percent", "usagePercent",
		"utilization", "percent", "used_pct", "usedPct"} {
		if _, ok := numberField(m, k); ok {
			return true
		}
	}
	return false
}

func looksLikeWindow(m map[string]any) bool {
	return resetOf(m) > 0 || hasAnyNumber(m, "limit", "total", "remaining")
}

func windowUsedPercent(m map[string]any) (float64, bool) {
	for _, k := range []string{"used_percent", "usedPercent", "usage_percent", "usagePercent",
		"utilization", "percent", "used_pct", "usedPct"} {
		if f, ok := numberField(m, k); ok {
			return clampPercent(f), true
		}
	}
	used, okUsed := numberField(m, "used")
	limit, okLimit := firstNumber(m, "limit", "total", "quota")
	if okUsed && okLimit && limit > 0 {
		return clampPercent(used / limit * 100), true
	}
	if rem, okRem := numberField(m, "remaining"); okRem && okLimit && limit > 0 {
		return clampPercent((limit - rem) / limit * 100), true
	}
	if okUsed {
		// 没有上限时按"比例"解释；>1 视为already a percent。
		if used <= 1 {
			return clampPercent(used * 100), true
		}
		return clampPercent(used), true
	}
	return 0, false
}

func clampPercent(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 100 {
		return 100
	}
	return f
}

func hasAnyNumber(m map[string]any, keys ...string) bool {
	_, ok := firstNumber(m, keys...)
	return ok
}

func firstNumber(m map[string]any, keys ...string) (float64, bool) {
	for _, k := range keys {
		if f, ok := numberField(m, k); ok {
			return f, true
		}
	}
	return 0, false
}

func numberField(m map[string]any, key string) (float64, bool) {
	switch v := m[key].(type) {
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	}
	return 0, false
}

// resetOf 从常见的重置时间字段里取 Unix 秒。
func resetOf(m map[string]any) int64 {
	for _, k := range []string{"resets_at", "reset_at", "resetsAt", "resetAt", "next_reset_at", "reset"} {
		if t := parseUnixLike(m[k]); t > 0 {
			return t
		}
	}
	return 0
}

func parseUnixLike(v any) int64 {
	switch x := v.(type) {
	case float64:
		// 毫秒时间戳也接受。
		if x > 1e12 {
			return int64(x / 1000)
		}
		if x > 1e9 {
			return int64(x)
		}
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0
		}
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			return parseUnixLike(n)
		}
		for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02 15:04:05"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.Unix()
			}
		}
	}
	return 0
}

// modelFamily 把模型名归到前端已有的家族分组。
func modelFamily(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "claude"), strings.Contains(m, "opus"), strings.Contains(m, "sonnet"), strings.Contains(m, "haiku"):
		return "Claude"
	case strings.Contains(m, "gemini"):
		return "Gemini"
	case strings.Contains(m, "gpt"), strings.Contains(m, "o1"), strings.Contains(m, "o3"), strings.Contains(m, "o4"):
		return "GPT"
	}
	return ""
}

type usageParseError struct{ msg string }

func (e *usageParseError) Error() string { return e.msg }

func snippet(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	if s == "" {
		return "（空回复）"
	}
	return s
}

// 编译期确认实现的能力。
var _ provider.QuotaCapability = (*Provider)(nil)
