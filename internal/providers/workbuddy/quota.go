package workbuddy

import (
	"context"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// QueryQuota 读取 WorkBuddy 的剩余积分（credits）。
//
// 数据源与 aether / local-vault 相同：
//   - /billing/meter/get-user-resource-summary 的 Packages：本周期总量与已用量；
//   - 企业/团队账号没有 Packages 时，读 /billing/meter/get-enterprise-user-usage
//     （limitNum / credit / cycleStartTime / cycleResetTime）。
//
// 积分类账号没有"窗口"，用量页会把它们画成 Balance 一行 + 一个 Credits 进度。
type wbSummary struct {
	IsPaidUser bool `json:"IsPaidUser"`
	Packages   []struct {
		CycleTotalCapacity wbNum `json:"CycleTotalCapacity"`
		CycleUsedCapacity  wbNum `json:"CycleUsedCapacity"`
	} `json:"Packages"`
}

type wbEnterpriseUsage struct {
	LimitNum       wbNum  `json:"limitNum"`
	Credit         wbNum  `json:"credit"`
	CycleStartTime string `json:"cycleStartTime"`
	CycleResetTime string `json:"cycleResetTime"`
}

type wbUserResource struct {
	Data struct {
		Response struct {
			Data struct {
				TotalCount wbNum               `json:"TotalCount"`
				Accounts   []wbResourceAccount `json:"Accounts"`
			} `json:"Data"`
		} `json:"Response"`
	} `json:"Data"`
}

// wbTime 解析上游的周期结束时间：不同版本的 meter 接口既给过 RFC3339
// 字符串，也给过毫秒/秒级时间戳。统一折算成 Unix 秒，避免"把 RFC3339
// 当数字解析成 0"这种静默丢时间的问题。
type wbTime int64

func (t *wbTime) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(strings.Trim(string(b), `"`))
	if s == "" || s == "null" {
		*t = 0
		return nil
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02 15:04:05"} {
		if ts, err := time.Parse(layout, s); err == nil {
			*t = wbTime(ts.Unix())
			return nil
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		*t = 0
		return nil
	}
	if v > 1e12 { // 毫秒
		*t = wbTime(int64(v / 1000))
		return nil
	}
	*t = wbTime(int64(v))
	return nil
}

func (t wbTime) seconds() int64 { return int64(t) }

type wbResourceAccount struct {
	CapacitySize          wbNum  `json:"CapacitySize"`
	CapacityUsed          wbNum  `json:"CapacityUsed"`
	CapacityRemain        wbNum  `json:"CapacityRemain"`
	CapacitySizePrecise   wbNum  `json:"CapacitySizePrecise"`
	CapacityUsedPrecise   wbNum  `json:"CapacityUsedPrecise"`
	CapacityRemainPrecise wbNum  `json:"CapacityRemainPrecise"`
	CycleCapacitySize     wbNum  `json:"CycleCapacitySize"`
	CycleCapacityUsed     wbNum  `json:"CycleCapacityUsed"`
	CycleCapacityRemain   wbNum  `json:"CycleCapacityRemain"`
	CycleEndTime          wbTime `json:"CycleEndTime"`
	ProductName           string `json:"ProductName"`
	SubProductName        string `json:"SubProductName"`
}

// QueryQuota 读取 WorkBuddy 的剩余积分。
//
// 5.9 官方改用 /billing/meter/get-user-resource（分页资源列表），
// 旧版 get-user-resource-summary 已 404，这里保留它作为老客户端兜底。
func (p *Provider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	token := firstNonEmpty(strings.TrimSpace(env.GetCredential("token")), strings.TrimSpace(env.GetCredential("access_token")))
	userID := strings.TrimSpace(env.GetCredential("user_id"))
	if token == "" || userID == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "WorkBuddy 账号缺少 token 或 user_id；请重新导入登录态").WithProvider(p.profile.id)
	}

	q := &provider.QuotaStatus{ReadAt: time.Now().Unix(), Plan: "WorkBuddy"}
	var res wbUserResource
	body := map[string]any{
		"PageNumber": 1, "PageSize": 100, "ProductCode": "p_tcaca",
		"Status": []int{0, 3}, "OnlyValidPeriod": true,
	}
	if err := p.meterCallJSON(ctx, env, token, userID, "/billing/meter/get-user-resource", body, &res); err == nil {
		var total, used, remain float64
		var resetAt int64
		for _, a := range res.Data.Response.Data.Accounts {
			// 字段优先级：本周期额度（Cycle*）优先于总额度（Capacity*）。
			//
			// 为什么：Capacity* 是"购买总量"，Cycle* 才是"本周期可用量"。
			// 反过来用会让用户看到"还剩 80%"却在下一次请求就被限流——因为
			// 本周期其实已经用完了。
			size, u := a.CycleCapacitySize, a.CycleCapacityUsed
			if size <= 0 {
				size, u = a.CapacitySize, a.CapacityUsed
				if size <= 0 {
					size, u = a.CapacitySizePrecise, a.CapacityUsedPrecise
				}
			}
			// 剩余一律用 total-used 反算：上游的 *Remain 字段在部分账号上
			// 不刷新（仍是上一周期的值），相减才是当前真实剩余。
			//
			// size<=0 只用来跳过"这个资源包没额度"的占位行；真正的"用光"
			// 会以 total>0、used==total 的形式出现，不会被误判成没读到。
			if size <= 0 {
				continue
			}
			total += float64(size)
			used += float64(u)
			remain += float64(size) - float64(u)
			if t := a.CycleEndTime.seconds(); t > resetAt {
				resetAt = t
			}
		}
		if total > 0 {
			if used > total {
				used = total
			}
			if remain < 0 {
				remain = 0
			}
			q.Windows = append(q.Windows, provider.QuotaWindow{
				Name: "Credits", Used: used / total * 100,
				Amount: int64(used), Limit: int64(total), Unit: "credits", ResetsAt: resetAt,
			})
			q.Balance = remain
			q.BalanceUnit = "credits"
			return q, nil
		}
	}

	// 老客户端兜底：summary 的 Packages。
	var sum wbSummary
	if err := p.meterCall(ctx, env, token, userID, "/billing/meter/get-user-resource-summary", &sum); err == nil {
		if sum.IsPaidUser {
			q.Plan = "Pro"
		} else {
			q.Plan = "Free plan"
		}
		var total, used float64
		for _, pkg := range sum.Packages {
			total += float64(pkg.CycleTotalCapacity)
			used += float64(pkg.CycleUsedCapacity)
		}
		if total > 0 {
			if used > total {
				used = total
			}
			q.Windows = append(q.Windows, provider.QuotaWindow{
				Name: "Credits", Used: used / total * 100,
				Amount: int64(used), Limit: int64(total), Unit: "credits",
			})
			q.Balance = total - used
			q.BalanceUnit = "credits"
			return q, nil
		}
	}

	// 企业/团队账号：资源列表为空时改用企业用量。
	var ent wbEnterpriseUsage
	if err := p.meterCall(ctx, env, token, userID, "/billing/meter/get-enterprise-user-usage", &ent); err == nil && float64(ent.LimitNum) > 0 {
		limit, credit := float64(ent.LimitNum), float64(ent.Credit)
		if credit > limit {
			credit = limit
		}
		q.Windows = append(q.Windows, provider.QuotaWindow{
			Name: "Credits", Used: credit / limit * 100,
			Amount: int64(credit), Limit: int64(limit), Unit: "credits",
			ResetsAt: parseBeijingTime(ent.CycleResetTime),
		})
		q.Balance = limit - credit
		q.BalanceUnit = "credits"
		return q, nil
	}
	return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "WorkBuddy 没有返回积分信息").WithProvider(p.profile.id)
}

// parseBeijingTime 解析 "2026-10-28 00:00:00"（WorkBuddy 用 UTC+8）。
func parseBeijingTime(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	loc := time.FixedZone("CST", 8*3600)
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", raw, loc); err == nil {
		return t.Unix()
	}
	return 0
}
