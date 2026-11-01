package trae

import (
	"context"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// QueryQuota 读取 Trae CN 的剩余积分。
//
// 与签到同一个端点（/checkin_credits/status）：它回 `credits` 与
// `extra_credits`，相加就是账号当前可用的积分数。Trae 没有"窗口"概念，
// 用量页以 Balance 一行显示剩余积分。
func (p *Provider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	if p.region != "CN" {
		return nil, ir.NewAPIError(ir.ErrCodeNoProvider, "Trae 国际版没有公开的积分接口").WithProvider(p.id)
	}
	token := accessToken(env)
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Trae CN 账号缺少 token；请重新导入登录态").WithProvider(p.id)
	}
	var st traeCheckinStatus
	if err := p.checkinCall(ctx, env, token, "/status", &st); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Trae 积分读取失败："+err.Error()).WithProvider(p.id)
	}
	if st.Code != 0 {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "Trae 积分接口返回 %d：%s", st.Code, st.Message).WithProvider(p.id)
	}
	credits := st.Credits + st.ExtraCredits
	q := &provider.QuotaStatus{
		ReadAt:      time.Now().Unix(),
		Balance:     credits,
		BalanceUnit: "credits",
	}
	if !st.Enable && credits == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeNoProvider, "Trae 没有返回积分信息").WithProvider(p.id)
	}
	return q, nil
}
