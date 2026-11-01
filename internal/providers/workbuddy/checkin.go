package workbuddy

// WorkBuddy 的每日签到（签到活动）。
//
// 协议来自 aether 的 provider/workbuddy_checkin.go 与官方插件的 meter 调用：
//   - POST {base}/billing/meter/checkin-activity-status 问活动状态；
//   - POST {base}/billing/meter/daily-checkin 领当天积分。
// 活动只在 WorkBuddy 国内版存在；国际版没有签到事件。
// 结论码：1001 今天已领、1002 不能参加、1003 活动结束。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/provider"
)

// wbNum 兼容上游把数字写成 number 或 string 两种形态。
type wbNum float64

func (n *wbNum) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(strings.Trim(string(b), `"`))
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil // 未知形态按 0 处理，签到结果不应因为展示字段解析失败而丢
	}
	*n = wbNum(v)
	return nil
}

type wbCheckinStatus struct {
	Active         bool
	TodayCheckedIn bool
	StreakDays     wbNum
	DailyCredit    wbNum
	TodayCredit    wbNum
	Code           int
	Msg            string
	Message        string
}

// UnmarshalJSON 兼容官方 5.9 的 {code,message,data:{todayCheckedIn,...}} 形状；
// data 里缺省 active 视为 true（与 local-vault 一致）。
func (s *wbCheckinStatus) UnmarshalJSON(raw []byte) error {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		return err
	}
	s.Code = int(numOf(top["code"]))
	s.Msg = strOf(top["msg"])
	s.Message = strOf(top["message"])
	data, _ := top["data"].(map[string]any)
	if data == nil {
		data = top
	}
	s.Active = true
	if v, ok := boolOf(data, "active", "Active"); ok {
		s.Active = v
	}
	s.TodayCheckedIn, _ = boolOf(data, "today_checked_in", "todayCheckedIn")
	s.StreakDays = wbNum(numOf(firstKey(data, "streak_days", "streakDays")))
	s.DailyCredit = wbNum(numOf(firstKey(data, "daily_credit", "dailyCredit")))
	s.TodayCredit = wbNum(numOf(firstKey(data, "today_credit", "todayCredit")))
	return nil
}

type wbCheckinClaim struct {
	Credit     wbNum
	StreakDays wbNum
	Message    string
	Msg        string
	Code       int
}

// UnmarshalJSON 兼容 {code,data:{success,credit,streakDays,message}}。
func (c *wbCheckinClaim) UnmarshalJSON(raw []byte) error {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		return err
	}
	c.Code = int(numOf(top["code"]))
	c.Msg = strOf(top["msg"])
	c.Message = strOf(top["message"])
	data, _ := top["data"].(map[string]any)
	if data == nil {
		data = top
	}
	c.Credit = wbNum(numOf(firstKey(data, "credit", "Credit")))
	c.StreakDays = wbNum(numOf(firstKey(data, "streak_days", "streakDays")))
	if m := strOf(data["message"]); m != "" {
		c.Message = m
	}
	if data["success"] == false {
		c.Code = 1002
	}
	return nil
}

// ExecuteTasks 执行签到任务。WorkBuddy 只有一个每日签到节点。
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

type wbCheckinResult struct {
	Outcome string
	Credit  float64
	Streak  int
	Msg     string
}

func (p *Provider) checkinOnce(ctx context.Context, env *provider.AccountEnv) wbCheckinResult {
	token := firstNonEmpty(
		strings.TrimSpace(env.GetCredential("token")),
		strings.TrimSpace(env.GetCredential("access_token")),
	)
	userID := strings.TrimSpace(env.GetCredential("user_id"))
	if token == "" || userID == "" {
		return wbCheckinResult{Outcome: provider.CheckinFailed, Msg: "WorkBuddy 账号缺少 token 或 user_id；请重新导入登录态"}
	}

	var st wbCheckinStatus
	if err := p.meterCall(ctx, env, token, userID, "/billing/meter/checkin-activity-status", &st); err != nil {
		return p.checkinRefused(ctx, env, token, userID, err)
	}
	if st.Code != 0 {
		return wbRefused(st.Code, st.Msg, st.Message)
	}
	switch {
	case !st.Active:
		return wbCheckinResult{Outcome: provider.CheckinInactive}
	case st.TodayCheckedIn:
		return wbCheckinResult{Outcome: provider.CheckinDone, Credit: float64(st.TodayCredit), Streak: int(st.StreakDays)}
	}

	var got wbCheckinClaim
	if err := p.meterCall(ctx, env, token, userID, "/billing/meter/daily-checkin", &got); err != nil {
		r := p.checkinRefused(ctx, env, token, userID, err)
		if r.Outcome == provider.CheckinDone && r.Streak == 0 {
			r.Streak = int(st.StreakDays)
		}
		return r
	}
	if got.Code != 0 {
		return wbRefused(got.Code, got.Msg, got.Message)
	}
	return wbCheckinResult{Outcome: provider.CheckinClaimed, Credit: float64(got.Credit), Streak: int(got.StreakDays), Msg: got.Message}
}

// checkinRefused 把一次失败折成结论。401 时用同一个 token 读一次积分：
// 积分能读说明登录态没问题、只是签到接口要求官方 App 的设备令牌（X-Device-Token），
// 这本身就是一个对用户有意义的答案，而不是没完没了地重试。
func (p *Provider) checkinRefused(ctx context.Context, env *provider.AccountEnv, token, userID string, err error) wbCheckinResult {
	if he, ok := err.(*wbHTTPError); ok && he.Status == http.StatusUnauthorized {
		if p.meterCall(ctx, env, token, userID, "/billing/meter/get-user-resource-summary", nil) == nil {
			return wbCheckinResult{Outcome: provider.CheckinOwnApp, Msg: "WorkBuddy 签到接口要求官方 App 的设备令牌（积分读取正常）；请在 WorkBuddy 客户端里签到"}
		}
		return wbCheckinResult{Outcome: provider.CheckinFailed, Msg: "WorkBuddy 拒绝了这个账号的登录态（401）；请重新登录"}
	}
	return wbCheckinResult{Outcome: provider.CheckinFailed, Msg: err.Error()}
}

func numOf(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case string:
		var f float64
		_, _ = fmt.Sscanf(strings.TrimSpace(x), "%f", &f)
		return f
	}
	return 0
}

func strOf(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func boolOf(m map[string]any, keys ...string) (bool, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if b, ok := v.(bool); ok {
				return b, true
			}
			if f, ok := v.(float64); ok {
				return f != 0, true
			}
		}
	}
	return false, false
}

func firstKey(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

func wbRefused(code int, msgs ...string) wbCheckinResult {
	msg := firstNonEmpty(msgs...)
	switch code {
	case 1001:
		return wbCheckinResult{Outcome: provider.CheckinDone, Msg: msg}
	case 1002:
		return wbCheckinResult{Outcome: provider.CheckinIneligible, Msg: msg}
	case 1003:
		return wbCheckinResult{Outcome: provider.CheckinInactive, Msg: msg}
	}
	return wbCheckinResult{Outcome: provider.CheckinFailed, Msg: fmt.Sprintf("code %d: %s", code, msg)}
}

type wbHTTPError struct{ Status int }

func (e *wbHTTPError) Error() string { return fmt.Sprintf("WorkBuddy HTTP %d", e.Status) }

// meterCall 向 WorkBuddy 的计费接口发一次空 JSON 请求。
func (p *Provider) meterCall(ctx context.Context, env *provider.AccountEnv, token, userID, path string, dst any) error {
	return p.meterCallJSON(ctx, env, token, userID, path, map[string]any{}, dst)
}

// meterCallJSON 发送自定义 JSON 体；新版权限接口需要分页/产品参数。
func (p *Provider) meterCallJSON(ctx context.Context, env *provider.AccountEnv, token, userID, path string, payload map[string]any, dst any) error {
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL(env)+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-User-Id", userID)
	req.Header.Set("X-Domain", p.domain(env))
	req.Header.Set("X-Product", "SaaS")
	req.Header.Set("X-IDE-Type", "WorkBuddy")
	req.Header.Set("X-IDE-Name", "WorkBuddy")
	req.Header.Set("X-IDE-Version", ideVersion)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := env.Client()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if json.Unmarshal(body, &e) == nil && e.Code != 0 {
			return fmt.Errorf("code %d: %s", e.Code, e.Msg)
		}
		return &wbHTTPError{Status: resp.StatusCode}
	}
	if dst == nil {
		return nil
	}
	return json.Unmarshal(body, dst)
}
