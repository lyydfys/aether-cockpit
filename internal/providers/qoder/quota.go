package qoder

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
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
type qoderQuotaBucket struct {
	Unit      string `json:"unit"`
	Total     int64  `json:"total"`
	Used      int64  `json:"used"`
	Remaining int64  `json:"remaining"`
}

type qoderUsageResponse struct {
	ExpiresAt     int64            `json:"expires_at"`
	PlanTier      string           `json:"plan_tier"`
	PlanTierName  string           `json:"plan_tier_name"`
	IsHighestTier bool             `json:"is_highest_tier"`
	UserQuota     qoderQuotaBucket `json:"user_quota"`
	AddOnQuota    qoderQuotaBucket `json:"add_on_quota"`
}

func quotaMillisToSeconds(ms int64) int64 {
	if ms > 1_000_000_000_000 { // 毫秒（Qoder 当前返回 1.796e12）
		return ms / 1000
	}
	return ms
}

func quotaUnit(b qoderQuotaBucket) string {
	if u := strings.TrimSpace(b.Unit); u != "" {
		return u
	}
	return "credits"
}

func quotaWindow(name string, b qoderQuotaBucket, resetsAt int64, aside bool) (provider.QuotaWindow, bool) {
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
func (p *Provider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
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
		h.Set("Cosy-MachineToken", v)
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
func qoderQuotaStatus(out qoderUsageResponse) *provider.QuotaStatus {
	q := &provider.QuotaStatus{
		ReadAt: time.Now().Unix(),
		Plan:   strings.TrimSpace(out.PlanTierName),
	}
	if until := quotaMillisToSeconds(out.ExpiresAt); until > 0 {
		q.Until = until
		q.ExpiresAt = until
	}
	planName := strings.TrimSpace(out.PlanTierName)
	if planName == "" {
		planName = "订阅"
	}
	if w, ok := quotaWindow(planName+" 额度", out.UserQuota, q.Until, false); ok {
		q.Windows = append(q.Windows, w)
	}
	if w, ok := quotaWindow("加量包", out.AddOnQuota, 0, true); ok {
		q.Windows = append(q.Windows, w)
	}
	// 加量包是独立钱包：主额度用尽时它还能继续跑。判定账号可用看的是
	// Remaining，只算 UserQuota 会把"主额度 2000/2000 用尽、加量包还剩 100"
	// 的账号误标成 depleted 直接下线。
	if out.UserQuota.Total > 0 || out.AddOnQuota.Total > 0 {
		q.Total = int(out.UserQuota.Total + out.AddOnQuota.Total)
		q.Used = int(out.UserQuota.Used + out.AddOnQuota.Used)
		q.Remaining = int(out.UserQuota.Remaining + out.AddOnQuota.Remaining)
		unit := out.UserQuota
		if unit.Total == 0 {
			unit = out.AddOnQuota
		}
		q.Unit = quotaUnit(unit)
		q.Balance = float64(q.Remaining)
		q.BalanceUnit = q.Unit
	}
	// used 缺失（0）时用 total-remaining 反推：只信 Used 会让"已用 90%"
	// 的账号显示成 0% 用量。
	if q.Used == 0 && q.Total > q.Remaining {
		q.Used = q.Total - q.Remaining
	}
	if len(q.Windows) == 0 {
		q.Message = "Qoder 暂未返回可读的额度信息"
		return q
	}
	return q
}
