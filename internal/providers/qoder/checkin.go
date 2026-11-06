package qoder

// Qoder 的每日积分（CLAIM_BENEFIT 活动）。
//
// 协议来自 aether 的 provider/qoder_checkin.go 与官方插件：
//   - GET  {openapi}/sash/api/v1/me/campaigns 列出账号的活动；
//   - POST {openapi}/sash/api/v1/me/campaigns/{id}/claim 领取。
// 活动按设备判定，官方插件会用本机 Qoder 客户端 runtime-info 的设备身份；
// 这里用账号导入时保存的机器标识，拿不到活动时如实回报，不伪造设备。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aether-cockpit/internal/provider"
)

type qoderCampaign struct {
	ID          string  `json:"campaignId"`
	ActionType  string  `json:"actionType"`
	ClaimStatus string  `json:"claimStatus"`
	StartAt     float64 `json:"startAt"`
	EndAt       float64 `json:"endAt"`
	Benefit     struct {
		Amount float64 `json:"amount"`
	} `json:"benefit"`
}

type qoderClaim struct {
	Status  string `json:"status"`
	Benefit struct {
		Amount float64 `json:"amount"`
	} `json:"benefit"`
	Data *qoderClaim `json:"data"`
}

func (p *Provider) ExecuteTasks(ctx context.Context, env *provider.AccountEnv, taskIDs []string) (*provider.CheckInReport, error) {
	day := provider.CheckinDay(time.Now())
	res := p.checkinOnce(ctx, env, time.Now())
	report := &provider.CheckInReport{
		Outcome: res.Outcome,
		Credit:  res.Credit,
		Streak:  res.Streak,
		Day:     day,
		Message: res.Msg,
	}
	report.Success = res.Outcome == provider.CheckinClaimed || res.Outcome == provider.CheckinDone
	report.EarnedPoints = int(res.Credit)
	if len(taskIDs) == 0 {
		taskIDs = []string{"daily_checkin"}
	}
	for _, id := range taskIDs {
		report.Results = append(report.Results, provider.TaskResult{
			TaskID:  id,
			Title:   "每日积分",
			Success: report.Success,
			Message: res.Msg,
			Points:  int(res.Credit),
			Skipped: res.Outcome == provider.CheckinInactive,
		})
	}
	return report, nil
}

type qoderCheckinResult struct {
	Outcome string
	Credit  float64
	Streak  int
	Msg     string
}

func (p *Provider) checkinOnce(ctx context.Context, env *provider.AccountEnv, now time.Time) qoderCheckinResult {
	c := p.credentials(env)
	if c.Token == "" {
		return qoderCheckinResult{Outcome: provider.CheckinFailed, Msg: "Qoder 账号缺少 token；请重新导入登录态"}
	}
	var list struct {
		Campaigns []qoderCampaign `json:"campaigns"`
	}
	if err := p.campaignCall(ctx, env, c, http.MethodGet, p.profile.openAPI+"/sash/api/v1/me/campaigns", &list); err != nil {
		return qoderCheckinResult{Outcome: provider.CheckinFailed, Msg: err.Error()}
	}
	var daily []qoderCampaign
	for _, camp := range list.Campaigns {
		if camp.ActionType == "CLAIM_BENEFIT" {
			daily = append(daily, camp)
		}
	}
	var credit float64
	claimed, failed := 0, 0
	var why string
	for _, camp := range daily {
		if camp.ClaimStatus != "CLAIMABLE" || camp.ID == "" {
			continue
		}
		var got qoderClaim
		err := p.campaignCall(ctx, env, c, http.MethodPost, p.profile.openAPI+"/sash/api/v1/me/campaigns/"+url.PathEscape(camp.ID)+"/claim", &got)
		if got.Data != nil {
			got = *got.Data
		}
		if err == nil && got.Status != "CLAIMED" {
			err = fmt.Errorf("领取返回 %q", got.Status)
		}
		if err != nil {
			failed++
			why = err.Error()
			continue
		}
		claimed++
		if got.Benefit.Amount > 0 {
			credit += got.Benefit.Amount
		} else {
			credit += camp.Benefit.Amount
		}
	}
	switch {
	case failed > 0:
		return qoderCheckinResult{Outcome: provider.CheckinFailed, Msg: why}
	case claimed > 0:
		return qoderCheckinResult{Outcome: provider.CheckinClaimed, Credit: credit}
	case len(daily) == 0:
		return qoderCheckinResult{Outcome: provider.CheckinInactive}
	}
	for _, camp := range daily {
		if camp.ClaimStatus == "CLAIMED" && qoderActive(camp, now) {
			return qoderCheckinResult{Outcome: provider.CheckinDone, Credit: camp.Benefit.Amount}
		}
	}
	return qoderCheckinResult{Outcome: provider.CheckinFailed, Msg: "今天的积分还没有开放领取"}
}

func qoderActive(c qoderCampaign, now time.Time) bool {
	return !now.Before(qoderWhen(c.StartAt)) && now.Before(qoderWhen(c.EndAt))
}

func qoderWhen(v float64) time.Time {
	if v > 1e12 {
		return time.UnixMilli(int64(v))
	}
	return time.Unix(int64(v), 0)
}

func (p *Provider) campaignCall(ctx context.Context, env *provider.AccountEnv, c creds, method, rawURL string, dst any) error {
	var rd io.Reader
	if method == http.MethodPost {
		rd = strings.NewReader("{}")
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Cosy-ClientType", "10")
	req.Header.Set("User-Agent", "qoder/"+p.profile.cosyVer)
	req.Header.Set("Cosy-MachineId", c.MachineID)
	req.Header.Set("Cosy-MachineToken", firstNonEmpty(env.GetCredential("machine_token"), c.MachineID))
	req.Header.Set("Cosy-MachineType", firstNonEmpty(env.GetCredential("machine_type"), "5"))
	req.Header.Set("Cosy-MachineOS", qoderOSName())

	client := env.Client()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("Qoder 登录态已失效，请重新登录")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &e) == nil {
			if m := firstNonEmpty(e.Error.Message, e.Message); m != "" {
				return fmt.Errorf("HTTP %d: %s", resp.StatusCode, m)
			}
		}
		return fmt.Errorf("Qoder 积分接口 HTTP %d", resp.StatusCode)
	}
	return json.Unmarshal(body, dst)
}
