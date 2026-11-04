package trae

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// Trae 的"额度"有两个互不相干的东西，早期我们把它们搞混了：
//
//	1. 签到积分（/ug/checkin_credits/status）：每天签到攒的额外积分，
//	   是"额外奖励"，不是套餐额度。
//	2. 套餐额度（/pay/ide_user_ent_usage）：订阅里真正被消耗的用量，
//	   字段是 basic_usage_amount（已用）与 basic_usage_limit（总量）。
//
// 用量卡要显示的是 2；1 只该留在签到卡片上。之前读的是 1，于是用户看到的
// "剩余积分"是签到累计值，与套餐实际剩余完全对不上。

// traeQuotaHostsForTest 让测试把额度根地址换成本地服务器；
// 生产代码始终按 region 拼域名。
var traeQuotaHostsForTest []string

// traeQuotaHosts 返回该账号可用的额度接口根地址。
//
// 国际版与国内版是**不同域名**，但接口路径同构；solo 变体沿用所在区域的域名。
func (p *Provider) traeQuotaHosts() []string {
	if len(traeQuotaHostsForTest) > 0 {
		return traeQuotaHostsForTest
	}
	if p.region == "CN" {
		return []string{"https://api.trae.cn"}
	}
	return []string{"https://api.trae.ai"}
}

// traeEntUsage 是 /pay/ide_user_ent_usage 的响应。
type traeEntUsage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Packs []struct {
			BaseInfo struct {
				Quota struct {
					BasicUsageLimit float64 `json:"basic_usage_limit"`
				} `json:"quota"`
				EndTime string `json:"end_time"`
			} `json:"entitlement_base_info"`
			Usage struct {
				BasicUsageAmount float64 `json:"basic_usage_amount"`
			} `json:"usage"`
		} `json:"user_entitlement_pack_list"`
	} `json:"data"`
}

// QueryQuota 读取 Trae 的套餐额度用量。
//
// 顺序：套餐额度 → 签到积分兜底。套餐接口读不到时（老账号/接口变更）
// 至少还能显示签到积分，而不是一片空白。
func (p *Provider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	token := accessToken(env)
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Trae 账号缺少 token；请重新导入登录态").WithProvider(p.id)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	q, err := p.queryEntUsage(ctx, env, token)
	if err == nil && q != nil {
		return q, nil
	}
	// 套餐接口失败不直接报错：签到积分仍可展示，把失败原因放进文案。
	if q2, err2 := p.queryCheckinCredits(ctx, env, token); err2 == nil {
		if err != nil {
			q2.Message = "套餐额度读取失败（" + err.Error() + "），以下为签到积分"
		}
		return q2, nil
	}
	if err == nil {
		err = fmt.Errorf("响应里没有额度包")
	}
	return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Trae 额度读取失败："+err.Error()).WithProvider(p.id)
}

// queryEntUsage 读套餐额度。CN 先试 v2 再回落 v1（国际版同构）。
func (p *Provider) queryEntUsage(ctx context.Context, env *provider.AccountEnv, token string) (*provider.QuotaStatus, error) {
	var lastErr error
	for _, host := range p.traeQuotaHosts() {
		for _, ver := range []string{"v2", "v1"} {
			endpoint := host + "/trae/api/" + ver + "/pay/ide_user_ent_usage"
			var out traeEntUsage
			if err := p.postJSON(ctx, env, token, endpoint, `{"require_usage":true}`, &out); err != nil {
				lastErr = err
				continue
			}
			if out.Code != 0 {
				lastErr = fmt.Errorf("接口返回 %d：%s", out.Code, strings.TrimSpace(out.Message))
				continue
			}
			if q := entUsageToQuota(out); q != nil {
				return q, nil
			}
			lastErr = fmt.Errorf("响应里没有额度包")
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的额度接口")
	}
	return nil, lastErr
}

// entUsageToQuota 把额度包列表换算成"总量/已用/剩余"。
//
// 上游只给 limit 与 amount，不给剩余，所以剩余靠减法并夹到 0；
// 多个包（月付 + 加量）按"总量相加、已用相加"合并，重置时间取最早的一个。
func entUsageToQuota(out traeEntUsage) *provider.QuotaStatus {
	var total, used float64
	var resetAt int64
	seen := false
	for _, pk := range out.Data.Packs {
		limit := pk.BaseInfo.Quota.BasicUsageLimit
		if limit <= 0 {
			continue
		}
		seen = true
		total += limit
		used += pk.Usage.BasicUsageAmount
		if ts := parseTraeTime(pk.BaseInfo.EndTime); ts > 0 && (resetAt == 0 || ts < resetAt) {
			resetAt = ts
		}
	}
	if !seen {
		return nil
	}
	if used < 0 {
		used = 0
	}
	remain := total - used
	if remain < 0 {
		remain = 0
	}
	q := &provider.QuotaStatus{
		ReadAt:      time.Now().Unix(),
		Total:       int(total),
		Used:        int(used),
		Remaining:   int(remain),
		Unit:        "credits",
		Balance:     remain,
		BalanceUnit: "credits",
		Until:       resetAt,
	}
	q.Windows = append(q.Windows, provider.QuotaWindow{
		Name:     "套餐额度",
		Used:     used * 100 / total,
		Limit:    int64(total),
		Amount:   int64(used),
		Unit:     "credits",
		ResetsAt: resetAt,
	})
	return q
}

// queryCheckinCredits 是兜底：签到累计积分（不是套餐额度）。
func (p *Provider) queryCheckinCredits(ctx context.Context, env *provider.AccountEnv, token string) (*provider.QuotaStatus, error) {
	var st traeCheckinStatus
	if err := p.checkinCall(ctx, env, token, "/status", &st); err != nil {
		return nil, err
	}
	if st.Code != 0 {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "Trae 签到接口返回 %d：%s", st.Code, st.Message).WithProvider(p.id)
	}
	// 只取 credits：参考实现里没有 extra_credits，凭空相加会放大数字。
	credits := st.Credits
	q := &provider.QuotaStatus{
		ReadAt:      time.Now().Unix(),
		Balance:     credits,
		BalanceUnit: "credits",
		Message:     "签到积分（套餐额度不可读）",
	}
	if !st.Enable && credits == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeNoProvider, "Trae 没有返回积分信息").WithProvider(p.id)
	}
	return q, nil
}

// postJSON 发一个带 Bearer 的 JSON POST 并解析。
func (p *Provider) postJSON(ctx context.Context, env *provider.AccountEnv, token, endpoint, body string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}
	p.applyHeaders(req.Header, env, token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := env.Client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("登录态已失效，请重新登录")
	case http.StatusForbidden:
		return fmt.Errorf("无权访问该接口（可能需要企业权限）")
	case http.StatusNotFound:
		return fmt.Errorf("该接口不存在")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("响应无法解析")
	}
	return nil
}

// parseTraeTime 解析 Trae 的时间：既可能是 RFC3339 字符串，也可能是秒级时间戳。
func parseTraeTime(v string) int64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.Unix()
	}
	if t, err := time.Parse("2006-01-02 15:04:05", v); err == nil {
		return t.Unix()
	}
	var n int64
	if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
		if n > 1_000_000_000_000 {
			return n / 1000
		}
		return n
	}
	return 0
}
