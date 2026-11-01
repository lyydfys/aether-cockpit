package trae

// Trae CN 的每日签到（签到送积分）。
//
// 协议来自 magpie 的 provider/trae_checkin.go：POST
// https://api.trae.cn/trae/api/v2/ug/checkin_credits/{status|claim}，
// 用与聊天相同的 Cloud-IDE-JWT / 设备头。结论码：1001 登录失效、
// 9074 领取被拒（官方 App 才能签）、9095 本设备今天已签。
// 只有 Trae CN 有签到活动；国际版没有。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/provider"
)

var traeCheckinURL = "https://api.trae.cn/trae/api/v2/ug/checkin_credits"

type traeCheckinStatus struct {
	Code         int     `json:"code"`
	Message      string  `json:"message"`
	Enable       bool    `json:"enable"`
	CheckedIn    bool    `json:"checked_in"`
	Credits      float64 `json:"credits"`
	ExtraCredits float64 `json:"extra_credits"`
}

func (p *Provider) ExecuteTasks(ctx context.Context, env *provider.AccountEnv, taskIDs []string) (*provider.CheckInReport, error) {
	day := provider.CheckinDay(time.Now())
	res := p.checkinOnce(ctx, env)
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
			Title:   "每日签到",
			Success: report.Success,
			Message: res.Msg,
			Points:  int(res.Credit),
			Skipped: res.Outcome == provider.CheckinInactive,
		})
	}
	return report, nil
}

type traeCheckinResult struct {
	Outcome string
	Credit  float64
	Streak  int
	Msg     string
}

func (p *Provider) checkinOnce(ctx context.Context, env *provider.AccountEnv) traeCheckinResult {
	token := accessToken(env)
	if token == "" {
		return traeCheckinResult{Outcome: provider.CheckinFailed, Msg: "Trae CN 账号缺少 token；请重新导入登录态"}
	}
	var st traeCheckinStatus
	if err := p.checkinCall(ctx, env, token, "/status", &st); err != nil {
		return traeCheckinResult{Outcome: provider.CheckinFailed, Msg: err.Error()}
	}
	if st.Code != 0 {
		return traeRefused(st.Code, st.Message)
	}
	switch {
	case !st.Enable:
		return traeCheckinResult{Outcome: provider.CheckinInactive}
	case st.CheckedIn:
		return traeCheckinResult{Outcome: provider.CheckinDone, Credit: st.Credits}
	}
	var got traeCheckinStatus
	if err := p.checkinCall(ctx, env, token, "/claim", &got); err != nil {
		return traeCheckinResult{Outcome: provider.CheckinFailed, Msg: err.Error()}
	}
	if got.Code == 9074 {
		return traeCheckinResult{Outcome: provider.CheckinOwnApp, Msg: orDefault(got.Message, "Trae CN 拒绝了这次签到（9074：参与人数过多），请在 Trae CN 客户端签到")}
	}
	if got.Code != 0 {
		return traeRefused(got.Code, got.Message)
	}
	credit := got.Credits
	if credit == 0 {
		credit = st.Credits
	}
	return traeCheckinResult{Outcome: provider.CheckinClaimed, Credit: credit + got.ExtraCredits}
}

func traeRefused(code int, msg string) traeCheckinResult {
	switch code {
	case 9095:
		return traeCheckinResult{Outcome: provider.CheckinIneligible, Msg: orDefault(msg, "本设备今天已经签到过")}
	case 1001:
		return traeCheckinResult{Outcome: provider.CheckinFailed, Msg: "Trae CN 登录态已失效，请重新登录"}
	case 9074:
		return traeCheckinResult{Outcome: provider.CheckinFailed, Msg: orDefault(msg, "请求太频繁")}
	}
	return traeCheckinResult{Outcome: provider.CheckinFailed, Msg: fmt.Sprintf("code %d: %s", code, msg)}
}

func orDefault(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func (p *Provider) checkinCall(ctx context.Context, env *provider.AccountEnv, token, path string, dst *traeCheckinStatus) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, traeCheckinURL+path, bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	p.applyHeaders(req.Header, env, token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	client := env.Client()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("Trae CN 登录态已失效，请重新登录")
	}
	if err := json.Unmarshal(body, dst); err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if err == nil && dst.Code != 0 {
			return nil // 上游用自己的错误码回答了非 2xx，交给结论码处理
		}
		if resp.StatusCode == http.StatusBadRequest {
			return fmt.Errorf("Trae CN 签到接口返回 400: %s", strings.TrimSpace(string(body)))
		}
		return fmt.Errorf("Trae CN 签到接口 HTTP %d", resp.StatusCode)
	}
	return nil
}
