package zcode

// ZCode 订阅额度（credits）读取。
//
// 端点、鉴权与字段语义来自 local-vault 的 zcode_account.rs（
// `refresh_account_quota` / `apply_quota_payload`）：
//   GET {balance}?app_version=<v>   Authorization: Bearer <zcode JWT>
//   → data.balances[] 求和；data.plans[] 取有效计划的 name
//
// 两个必须写清的语义：
//  1. **API Key 模式不查询**。这个接口只认 ZCode 客户端的 OAuth JWT
//     （`zcodejwttoken`），不认 Coding Plan 的 API Key；我们先验证凭证是不是
//     JWT，不是就静默返回 nil，绝不拿 API Key 去打这个端点——那只会稳定拿到
//     401/403，既刷日志又让用户以为"额度接口坏了"。
//  2. **业务码非 0 不算网络失败**。上游用 200 + `code != 0` 表达"这个 Key 没有
//     额度/未订阅"，这属于确定性结果，不重试、不当瞬时错误。

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// zcodeBalanceURL 是计划余额接口。做成变量是为了让测试能指向 httptest。
var zcodeBalanceURL = "https://zcode.z.ai/api/v1/zcode-plan/billing/balance"

const (
	// zcodeAppVersion 与参考实现默认的客户端版本一致；上游用它区分协议版本。
	zcodeAppVersion = "3.10.2"
	// zcodeAppOrigin 是客户端 webview 的来源，来源头缺失会被风控降权。
	zcodeAppOrigin = "https://zcode.z.ai"
)

// QueryQuota 读取 ZCode 订阅余额。
//
// 返回 (nil, nil) 表示"该账号模式没有可查询的额度"：网关对 nil+nil 的处理是
// 静默跳过（不写失败、不弹错误），这正是 API Key 模式应有的表现。
func (p *Provider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	token := zcodeJWT(env)
	if token == "" {
		// 没有 JWT：本机导入拿到的都是 Coding Plan 的 API Key，
		// 这个余额接口不接受它，所以直接返回 nil，不发请求。
		return nil, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, zcodeBalanceRequestURL(), nil)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 ZCode 额度请求失败").WithCause(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	applyQuotaHeaders(req)

	resp, err := env.Client().Do(req)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "ZCode 额度接口连接失败").
			WithCause(err).WithProvider(p.id).WithRetryable(true)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	// 401/404 分开给文案：前者要用户重新登录，后者说明该站/该版本没有这个接口。
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"ZCode 登录态已失效（额度接口 401）；请在 ZCode 客户端重新登录后再导入").WithProvider(p.id)
	case http.StatusForbidden:
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"ZCode 额度接口拒绝访问（403）；当前账号可能未订阅 Coding Plan").WithProvider(p.id)
	case http.StatusNotFound:
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed,
			"ZCode 该版本没有额度接口（404：%s）", zcodeBalanceURL).WithProvider(p.id)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "ZCode 额度接口返回 %d：%s",
			resp.StatusCode, zSnippet(body)).WithProvider(p.id)
	}

	var payload zBalanceResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "ZCode 额度响应无法解析：%s",
			zSnippet(body)).WithProvider(p.id)
	}
	// 业务码：用 msg 当错误信息，不重试（未订阅/无权限都是确定性结果）。
	if payload.Code.Valid() && payload.Code.Int() != 0 {
		msg := zFirstNonEmpty(payload.Msg, payload.Message, zSnippet(body))
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "ZCode 额度接口返回 %d：%s",
			payload.Code.Int(), msg).WithProvider(p.id)
	}

	balances := payload.Data.Balances
	if len(balances) == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeNoProvider,
			"ZCode 没有返回任何额度余额（data.balances 为空）；该账号可能未订阅 Coding Plan").
			WithProvider(p.id)
	}

	q := summarizeBalances(balances)
	q.ReadAt = time.Now().Unix()
	q.Unit = "credits"
	q.BalanceUnit = "credits"
	q.Plan = zcodePlanName(payload.Data.Plans)
	q.Balance = float64(q.Remaining)
	return q, nil
}

// summarizeBalances 把多条 balance 累加成一张额度卡。
//
// 为什么要逐条算"剩余"再求和：上游每个 balance 是一个独立的额度池
// （订阅额度、赠送额度、加量包），字段也不统一——有的给 remaining_units，
// 有的只给 available_units，老数据甚至只给 total/used。
// 先把每条归一成 (total, remaining) 再求和，任何一条缺字段都不会把总和拉成 0。
func summarizeBalances(balances []zBalance) *provider.QuotaStatus {
	var total, remain, usedFallback float64
	var reset int64
	for _, b := range balances {
		itemTotal := mathMax(b.TotalUnits.Float(), 0)
		itemRemain, ok := b.remainingUnits()
		if !ok {
			// 两个剩余字段都没有：用 total - used 反推（used 也缺就按 0 已用）。
			itemRemain = itemTotal - mathMax(b.UsedUnits.Float(), 0)
		}
		itemRemain = clampFloat(itemRemain, 0, itemTotal)

		total += itemTotal
		remain += itemRemain
		usedFallback += mathMax(b.UsedUnits.Float(), 0)
		reset = zEarliest(reset, b.resetUnix())
	}

	used := total - remain
	if used <= 0 && usedFallback > 0 {
		// total 与 remaining 恰好相等但上游报了已用：以已用为准，
		// 避免"明明扣了额度却显示 100% 剩余"。
		used = clampFloat(usedFallback, 0, total)
		remain = total - used
	}
	used = clampFloat(used, 0, total)

	percent := 0.0
	if total > 0 {
		percent = clampFloat(used/total*100, 0, 100)
	}
	return &provider.QuotaStatus{
		Total:     int(zRound(total)),
		Used:      int(zRound(used)),
		Remaining: int(zRound(remain)),
		ExpiresAt: reset,
		Windows: []provider.QuotaWindow{{
			Name:     "Credits",
			Used:     percent,
			Amount:   zRound(used),
			Limit:    zRound(total),
			Unit:     "credits",
			ResetsAt: reset,
		}},
	}
}

// zcodeBalanceRequestURL 拼上 app_version：上游按它决定返回哪一版字段。
func zcodeBalanceRequestURL() string {
	u, err := url.Parse(zcodeBalanceURL)
	if err != nil {
		return zcodeBalanceURL
	}
	q := u.Query()
	q.Set("app_version", zcodeAppVersion)
	u.RawQuery = q.Encode()
	return u.String()
}

// applyQuotaHeaders 写入 ZCode 客户端的来源头。
//
// 这些头上游用来判断"是不是官方客户端"，缺了容易吃到 403/风控；
// 但从本机拿不到的值（device_mid、系统小版本）宁可省略也不编造。
func applyQuotaHeaders(req *http.Request) {
	h := req.Header
	h.Set("User-Agent", "ZCode/"+zcodeAppVersion)
	h.Set("http-referer", zcodeAppOrigin)
	h.Set("Referer", zcodeAppOrigin)
	h.Set("x-zcode-app-version", zcodeAppVersion)
	h.Set("x-title", "Z Code@electron")
	h.Set("x-platform", runtime.GOOS+"-"+runtime.GOARCH)
	h.Set("x-release-channel", "production")
	h.Set("x-os-category", runtime.GOOS)
	if tz := timeZoneLabel(); tz != "" {
		h.Set("x-client-timezone", tz)
	}
	if id := requestID(); id != "" {
		h.Set("x-request-id", id)
	}
}

// timeZoneLabel 返回 IANA 时区名；Go 只给到 "Local" 时视为拿不到。
func timeZoneLabel() string {
	tz := time.Now().Location().String()
	if tz == "" || tz == "Local" {
		return ""
	}
	return tz
}

// requestID 生成一个 UUID v4 形态的请求 id（上游要求 x-request-id 唯一）。
func requestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// zcodeJWT 找出可用于余额接口的 ZCode JWT。
//
// 只认 JWT 形态（三段、首段是 `eyJ`）：API Key 形如 `sk-...`/`<id>.<secret>`，
// 拿去请求一定失败，因此宁可不查（见 QueryQuota 顶部注释）。
func zcodeJWT(env *provider.AccountEnv) string {
	for _, key := range []string{"zcode_jwt", "zcode_jwt_token", "jwt", "access_token", "oauth_token"} {
		v := strings.TrimSpace(env.GetCredential(key))
		if looksLikeJWT(v) {
			return v
		}
	}
	return ""
}

func looksLikeJWT(v string) bool {
	if v == "" {
		return false
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return false
	}
	return strings.HasPrefix(parts[0], "eyJ") && parts[1] != "" && parts[2] != ""
}

// zcodePlanName 取有效计划的展示名。
//
// 优先 status=="active"；没有 active 时退回"没有明显失效状态"的计划
// （过期/取消的计划不能拿来当当前套餐名）。
func zcodePlanName(plans []zPlan) string {
	for _, plan := range plans {
		if strings.EqualFold(strings.TrimSpace(plan.Status), "active") {
			if name := zFirstNonEmpty(plan.Name, plan.PlanID); name != "" {
				return name
			}
		}
	}
	for _, plan := range plans {
		switch strings.ToLower(strings.TrimSpace(plan.Status)) {
		case "", "active":
			if name := zFirstNonEmpty(plan.Name, plan.PlanID); name != "" {
				return name
			}
		}
	}
	return ""
}

/* ---------------- 响应结构 ---------------- */

type zBalanceResponse struct {
	Code    zNum   `json:"code"`
	Msg     string `json:"msg"`
	Message string `json:"message"`
	Data    struct {
		Plans    []zPlan    `json:"plans"`
		Balances []zBalance `json:"balances"`
	} `json:"data"`
}

type zPlan struct {
	Name   string `json:"name"`
	PlanID string `json:"plan_id"`
	Status string `json:"status"`
}

type zBalance struct {
	TotalUnits     zNum `json:"total_units"`
	UsedUnits      zNum `json:"used_units"`
	RemainingUnits zNum `json:"remaining_units"`
	AvailableUnits zNum `json:"available_units"`
	PeriodEnd      zNum `json:"period_end"`
	ExpiresAt      zNum `json:"expires_at"`
	ExpireAt       zNum `json:"expire_at"`
	ExpiresTime    zNum `json:"expires_time"`
}

// remainingUnits 按「remaining_units 优先、available_units 次之」取剩余。
func (b zBalance) remainingUnits() (float64, bool) {
	if b.RemainingUnits.Valid() {
		return b.RemainingUnits.Float(), true
	}
	if b.AvailableUnits.Valid() {
		return b.AvailableUnits.Float(), true
	}
	return 0, false
}

// resetUnix 取该额度池的周期结束/到期时间（上游给的是秒）。
func (b zBalance) resetUnix() int64 {
	for _, v := range []zNum{b.PeriodEnd, b.ExpiresAt, b.ExpireAt, b.ExpiresTime} {
		if ts := v.Unix(); ts > 0 {
			return ts
		}
	}
	return 0
}

/* ---------------- 基础类型 ---------------- */

// zNum 兼容 number / 数字字符串，并保留"字段是否存在"。
// 不能用 float64 直接接：0 是合法余额，把 0 当"缺失"会误判成没有数据。
type zNum struct {
	value float64
	ok    bool
}

func (n *zNum) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var text string
		if err := json.Unmarshal(b, &text); err != nil {
			return nil
		}
		s = strings.TrimSpace(text)
		if s == "" {
			return nil
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil // 未知形态按"缺失"处理，不让展示字段拖垮整次读数
	}
	n.value, n.ok = v, true
	return nil
}

func (n zNum) Valid() bool    { return n.ok }
func (n zNum) Float() float64 { return n.value }
func (n zNum) Int() int       { return int(n.value) }

// Unix 把数值当秒；毫秒自动降级（部分账号的周期时间给的是毫秒）。
func (n zNum) Unix() int64 {
	if !n.ok {
		return 0
	}
	v := n.value
	if v > 1e12 {
		v /= 1000
	}
	if v <= 0 {
		return 0
	}
	return int64(v)
}

/* ---------------- 小工具 ---------------- */

func zFirstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func zEarliest(current, candidate int64) int64 {
	if candidate <= 0 {
		return current
	}
	if current == 0 || candidate < current {
		return candidate
	}
	return current
}

func mathMax(v, floor float64) float64 {
	if v < floor {
		return floor
	}
	return v
}

func clampFloat(v, low, high float64) float64 {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func zRound(v float64) int64 {
	if v <= 0 {
		return 0
	}
	return int64(v + 0.5)
}

// zSnippet 截断响应体，错误信息里不放整段 body。
func zSnippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) <= 200 {
		return s
	}
	return s[:200] + "…"
}
