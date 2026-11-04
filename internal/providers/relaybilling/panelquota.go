package relaybilling

import (
	"context"
	"net/http"
	"strings"

	"aether-cockpit/internal/provider"
)

// panelQuota 依次在候选根路径上尝试面板自身的额度接口。
//
// 顺序说明：
//  1. 先用 /api/status 读 quota_per_unit —— 有的站把 500000 改成别的比例，
//     不看这个值就算不准（这是"读得到但读错"的主要来源）。
//  2. 再用 /api/user/self 读 quota / used_quota。
//  3. access_token 优先于 api_key：面板接口要用户级令牌，拿模型 Key 试
//     只是为了兜住"个别站确实放开"的情况。
func panelQuota(ctx context.Context, env *provider.AccountEnv, client *http.Client, roots []string, modelKey string) (*provider.QuotaStatus, error) {
	token := strings.TrimSpace(env.GetCredential("access_token"))
	if token == "" {
		token = modelKey
	}
	userID := strings.TrimSpace(env.GetCredential("user_id"))
	if userID == "" {
		userID = strings.TrimSpace(env.GetCredential("panel_user_id"))
	}

	var lastErr error
	for _, root := range roots {
		info := detectPanel(ctx, client, root, token)
		res, err := queryRelayPanelUser(ctx, client, root, token, userID, info)
		if err != nil {
			// 鉴权被拒是"令牌类型不对"，换根路径也一样被拒：直接返回，
			// 让调用方给出"需要 access token"的可执行提示。
			if strings.Contains(err.Error(), "鉴权被拒") {
				return nil, err
			}
			lastErr = err
			continue
		}
		q := &provider.QuotaStatus{
			ReadAt:      nowSeconds(),
			BalanceUnit: "USD",
			Message:     res.Source,
		}
		if res.HasBalance {
			q.Balance = res.Balance
		}
		if res.Plan != "" {
			q.Plan = res.Plan
			q.Message += " · 分组 " + res.Plan
		}
		if res.HasUsed {
			// 只有"已用"没有"剩余"时（限额制面板），也把已用写清楚，
			// 免得界面显示一个 0 余额让人误会。
			q.Message += " · 已用 $" + formatUSD(res.UsedUSD)
			if !res.HasBalance {
				q.Message += "（该站只返回已用，不返回剩余）"
			}
		}
		if !res.HasBalance && !res.HasUsed {
			lastErr = &statusError{Status: 200, Body: "响应里没有可用的额度字段"}
			continue
		}
		return q, nil
	}
	if lastErr == nil {
		lastErr = &statusError{Status: 0, Body: "没有候选根路径"}
	}
	return nil, lastErr
}
