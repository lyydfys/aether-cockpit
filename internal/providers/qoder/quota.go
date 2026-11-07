package qoder

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// Qoder 的订阅额度接口（CN/Intl 同一路径，openAPI 域名不同）：
//
//	GET {openAPI}/api/v1/me/usage
//
// 返回 user_quota（订阅额度）与 add_on_quota（加量包），单位是 credits，
// expires_at 是毫秒时间戳。两者都是"总量/已用/剩余"，没有 5h/7d 那种窗口，
// 因此映射成两条额度行，而不是滚动窗口。
// Qoder 的额度接口有两代响应形状，字段名风格不同：
//
//	v2 /api/v2/quota/usage  → camelCase：userQuota / addOnQuota / expiresAt /
//	                          totalUsagePercentage（0..1 的小数）
//	v1 /api/v1/me/usage     → snake_case：user_quota / add_on_quota / expires_at /
//	                          total_usage_percentage（0..100 的百分数）
//
// Go 的 encoding/json 对大小写不敏感，但对"有下划线 vs 无下划线"不宽容：
// 拿 `json:"user_quota"` 去解 `userQuota` 会**静默得到零值**。旧实现就踩了这一点：
// v2 解析"成功"但全是 0，于是判定"没有可读额度"直接返回，永远走不到能解出来的 v1。
// 这里显式两种都收，谁有数就用谁。数值一律用 flexFloat：同一字段既可能是整数
// （1400）也可能是小数（0.78），用 int 会截断成 0。

// rawBucket 是上游桶的原始形态。
type rawBucket struct {
	Total     flexFloat `json:"total"`
	Used      flexFloat `json:"used"`
	Remaining flexFloat `json:"remaining"`
	Percent   flexFloat `json:"percentage"`
	Unit      string    `json:"unit"`
}

// flexFloat 容忍 number / string / null 三种写法（含 "1,400" 千分位）。
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(strings.Trim(string(b), "\""))
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	if v, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64); err == nil {
		*f = flexFloat(v)
		return nil
	}
	*f = 0
	return nil
}

// qoderBucket 归一化后的桶（整数计）。
type qoderBucket struct {
	Total     int64
	Used      int64
	Remaining int64
	Percent   float64 // 0..100
	Unit      string
}

// roundHalfUp 四舍五入（避免 1399.9999 被截断成 1399）。
func roundHalfUp(v float64) float64 {
	if v >= 0 {
		return math.Floor(v + 0.5)
	}
	return math.Ceil(v - 0.5)
}

func (b rawBucket) normalize() qoderBucket {
	out := qoderBucket{
		Total:     int64(roundHalfUp(float64(b.Total))),
		Used:      int64(roundHalfUp(float64(b.Used))),
		Remaining: int64(roundHalfUp(float64(b.Remaining))),
		Percent:   float64(b.Percent),
		Unit:      strings.TrimSpace(b.Unit),
	}
	// percentage 两代口径不同：v2 的 addOnQuota.percentage=0.78（78%），
	// v1 的 add_on_quota.percentage=78。统一折算成 0..100。
	if out.Percent > 0 && out.Percent <= 1.0001 {
		out.Percent *= 100
	}
	if out.Used == 0 && out.Total > 0 && out.Remaining > 0 {
		out.Used = out.Total - out.Remaining
	}
	if out.Remaining == 0 && out.Total > 0 && out.Used > 0 && out.Used <= out.Total {
		out.Remaining = out.Total - out.Used
	}
	if out.Total > 0 && out.Used > out.Total {
		out.Used = out.Total
	}
	return out
}

// qoderUsageResponse 同时容纳两代字段。
type qoderUsageResponse struct {
	// v2（camelCase）
	ExpiresAtV2     int64     `json:"expiresAt"`
	PlanTierNameV2  string    `json:"planTierName"`
	TotalUseV2      flexFloat `json:"totalUsagePercentage"`
	IsQuotaExceeded bool      `json:"isQuotaExceeded"`
	UserQuotaV2     rawBucket `json:"userQuota"`
	AddOnQuotaV2    rawBucket `json:"addOnQuota"`

	// v1（snake_case）
	ExpiresAtV1  int64     `json:"expires_at"`
	PlanTier     string    `json:"plan_tier"`
	PlanTierName string    `json:"plan_tier_name"`
	TotalUseV1   flexFloat `json:"total_usage_percentage"`
	UserQuotaV1  rawBucket `json:"user_quota"`
	AddOnQuotaV1 rawBucket `json:"add_on_quota"`
}

// pickBuckets 从两代字段里挑出"真的有数"的那一组。
//
// 判据：先比 userQuota 的 total，再比 addOnQuota；全为 0 说明这一代没给数据
// （典型是字段名不匹配导致的静默零值），换另一代。
func (r qoderUsageResponse) pickBuckets() (user, addOn qoderBucket, ok bool) {
	v2User, v2Add := r.UserQuotaV2.normalize(), r.AddOnQuotaV2.normalize()
	v1User, v1Add := r.UserQuotaV1.normalize(), r.AddOnQuotaV1.normalize()
	score := func(u, a qoderBucket) int64 { return u.Total*2 + a.Total + u.Used + a.Used }
	switch {
	case score(v2User, v2Add) == 0 && score(v1User, v1Add) == 0:
		return qoderBucket{}, qoderBucket{}, false
	case score(v2User, v2Add) >= score(v1User, v1Add):
		return v2User, v2Add, true
	default:
		return v1User, v1Add, true
	}
}

// planName 取计划名（两代任一）。
func (r qoderUsageResponse) planName() string {
	for _, v := range []string{r.PlanTierNameV2, r.PlanTierName, r.PlanTier} {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// expiresAt 取到期时间（秒），兼容毫秒。
func (r qoderUsageResponse) expiresAt() int64 {
	for _, v := range []int64{r.ExpiresAtV2, r.ExpiresAtV1} {
		if v > 0 {
			return quotaMillisToSeconds(v)
		}
	}
	return 0
}

// totalUsagePercent 返回总用量百分比（0..100）；没有则 0。
func (r qoderUsageResponse) totalUsagePercent() float64 {
	for _, v := range []float64{float64(r.TotalUseV2), float64(r.TotalUseV1)} {
		if v <= 0 {
			continue
		}
		if v <= 1.0001 {
			return v * 100
		}
		return v
	}
	return 0
}

func quotaMillisToSeconds(ms int64) int64 {
	if ms > 1_000_000_000_000 { // 毫秒（Qoder 当前返回 1.796e12）
		return ms / 1000
	}
	return ms
}

func quotaUnit(b qoderBucket) string {
	if u := strings.TrimSpace(b.Unit); u != "" {
		return u
	}
	return "credits"
}

func quotaWindow(name string, b qoderBucket, resetsAt int64, aside bool) (provider.QuotaWindow, bool) {
	if b.Total <= 0 {
		return provider.QuotaWindow{}, false
	}
	used := b.Used
	if used < 0 {
		used = 0
	}
	if used > b.Total {
		used = b.Total
	}
	return provider.QuotaWindow{
		Name:     name,
		Used:     float64(used) * 100 / float64(b.Total),
		Amount:   used,
		Limit:    b.Total,
		Unit:     quotaUnit(b),
		Aside:    aside,
		ResetsAt: resetsAt,
	}, true
}

// QueryQuota 实现 provider.QuotaCapability。
//
// 401 时先刷新令牌再重试一次：账号里存的 token 可能已失效，但 refresh_token
// 还有效（实测 expires_at 写着未过期，服务端仍回 TOKEN_INVALID）。不刷新就会
// 把"令牌过期"当成"这个渠道读不到额度"，用户永远看到 0。
func (p *Provider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	q, err := p.queryQuotaOnce(ctx, env)
	if err == nil {
		return q, nil
	}
	ae := ir.AsAPIError(err)
	if ae == nil || ae.Code != ir.ErrCodeUpstreamAuth {
		return nil, err
	}
	if strings.TrimSpace(env.GetCredential("refresh_token")) == "" {
		return nil, err
	}
	if rerr := p.refreshToken(ctx, env); rerr != nil {
		return nil, err
	}
	return p.queryQuotaOnce(ctx, env)
}

// queryQuotaOnce 是一次完整的额度读取（不含刷新重试）。
func (p *Provider) queryQuotaOnce(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	c, err := p.ensureToken(ctx, env)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.Token) == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Qoder 账号缺少 token；请重新导入登录态").WithProvider(p.profile.id)
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	// 额度接口的版本：v2 是当前客户端在用的路径（`/api/v2/quota/usage`），
	// v1 的 `/api/v1/me/usage` 只作为老账号兜底。
	//
	// 另外要带上 Cosy 头：只给 Authorization 时部分账号会被判成"非客户端请求"
	// 而拿不到额度（返回空桶），这与 401 是不同的失败，值得单独试一次。
	paths := []string{"/api/v2/quota/usage", "/api/v1/me/usage"}
	var lastErr error
	for _, path := range paths {
		endpoint := strings.TrimRight(p.profile.openAPI, "/") + path
		req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if rerr != nil {
			return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 Qoder 额度请求失败").WithCause(rerr).WithProvider(p.profile.id)
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Accept", "application/json")
		p.applyQuotaHeaders(req.Header, c)

		resp, derr := env.Client().Do(req)
		if derr != nil {
			lastErr = ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Qoder 额度读取失败："+derr.Error()).WithProvider(p.profile.id)
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "Qoder 额度接口鉴权失败（HTTP %d）", resp.StatusCode).WithProvider(p.profile.id)
		}
		if resp.StatusCode == http.StatusNotFound {
			// 该版本路径不存在：换下一个候选，而不是直接判失败。
			lastErr = ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Qoder 额度路径 %s 不存在", path).WithProvider(p.profile.id)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Qoder 额度读取失败（HTTP %d）：%s",
				resp.StatusCode, truncate(string(raw), 240)).WithProvider(p.profile.id)
			continue
		}
		var out qoderUsageResponse
		if uerr := json.Unmarshal(raw, &out); uerr != nil {
			lastErr = ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Qoder 额度响应无法解析").WithCause(uerr).WithProvider(p.profile.id)
			continue
		}
		if q := qoderQuotaStatus(out); q != nil {
			return q, nil
		}
		lastErr = ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Qoder 额度响应里没有可用额度").WithProvider(p.profile.id)
	}
	return nil, lastErr
}

// applyQuotaHeaders 补上 Qoder 客户端标识头（能拿到就带，拿不到省略）。
func (p *Provider) applyQuotaHeaders(h http.Header, c creds) {
	if strings.TrimSpace(c.Token) == "" {
		return
	}
	if v := strings.TrimSpace(c.MachineID); v != "" {
		h.Set("Cosy-MachineToken", firstNonEmpty(c.MachineToken, v))
		h.Set("Cosy-MachineId", v)
	}
	if v := strings.TrimSpace(c.UserID); v != "" {
		h.Set("Cosy-User", v)
	}
	if v := strings.TrimSpace(p.profile.cosyVer); v != "" {
		h.Set("Cosy-Version", v)
	}
	h.Set("Cosy-MachineType", "5")
	h.Set("Cosy-MachineOS", machineOS())
	h.Set("Cosy-Scene", "app")
}

// qoderQuotaStatus 把响应换算成 QuotaStatus；没有额度信息时返回 nil。
//
// 关键：两代字段名不同，pickBuckets 挑出真正有数的那一组，避免"解析成功但全是零"
// 导致卡片显示 0。
func qoderQuotaStatus(out qoderUsageResponse) *provider.QuotaStatus {
	user, addOn, ok := out.pickBuckets()
	if !ok {
		// 没给额度桶，但可能只给了总用量百分比：至少把这个百分比展示出来。
		if pct := out.totalUsagePercent(); pct > 0 {
			name := strings.TrimSpace(out.planName())
			if name == "" {
				name = "订阅"
			}
			until := out.expiresAt()
			return &provider.QuotaStatus{
				ReadAt:  time.Now().Unix(),
				Plan:    out.planName(),
				Until:   until,
				Windows: []provider.QuotaWindow{{Name: name + " 用量", Used: pct, ResetsAt: until}},
				Message: "上游只返回总用量百分比（未返回额度桶）",
			}
		}
		return nil
	}
	q := &provider.QuotaStatus{
		ReadAt: time.Now().Unix(),
		Plan:   out.planName(),
	}
	if until := out.expiresAt(); until > 0 {
		q.Until = until
		q.ExpiresAt = until
	}
	planName := strings.TrimSpace(q.Plan)
	if planName == "" {
		planName = "订阅"
	}
	if w, ok := quotaWindow(planName+" 额度", user, q.Until, false); ok {
		q.Windows = append(q.Windows, w)
	}
	if w, ok := quotaWindow("加量包", addOn, 0, true); ok {
		q.Windows = append(q.Windows, w)
	}
	// 加量包是独立钱包：主额度用尽时它还能继续跑。判定账号可用看的是 Remaining，
	// 只算 user 会把"主额度用尽、加量包还剩 100"误标成 depleted。
	if total := user.Total + addOn.Total; total > 0 {
		q.Total = int(total)
		q.Used = int(user.Used + addOn.Used)
		q.Remaining = int(user.Remaining + addOn.Remaining)
		unit := user
		if unit.Total == 0 {
			unit = addOn
		}
		q.Unit = quotaUnit(unit)
		q.Balance = float64(q.Remaining)
		q.BalanceUnit = q.Unit
	}
	// used 缺失（0）时用 total-remaining 反推。
	if q.Used == 0 && q.Total > q.Remaining {
		q.Used = q.Total - q.Remaining
	}
	if len(q.Windows) == 0 {
		q.Message = "Qoder 暂未返回可读的额度信息"
		return q
	}
	return q
}
