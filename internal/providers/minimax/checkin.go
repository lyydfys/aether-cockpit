package minimax

// MiniMax Code 的每日签到。
//
// 协议来自 aether 社区插件 packages/minimax 的 checkinOf/signed：
//   - GET  <agent>/minimax-cloud/api/v1/signin/status
//   - POST <agent>/minimax-cloud/api/v1/signin/claim  {}
// 参数与签名按 mcode 0.6.12：device_platform=web、is_desktop=1、
// desktop_version=0.6.12，外加 x-timestamp / x-signature / yy。
// 面板 7 天：1 未开始、2 可领、3 已领、4 不可用；claim_result 1 本次领到、2 今天已领。

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/provider"
)

const signSecret = "I*7Cf%WZ#S&%1RlZJ&C2"

type checkinDay struct {
	DayNo   int     `json:"day_no"`
	Points  float64 `json:"points"`
	Status  int     `json:"status"`
	IsToday bool    `json:"is_today"`
}

type checkinEnvelope struct {
	StatusInfo *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"statusInfo"`
	BaseResp *struct {
		Code int    `json:"status_code"`
		Msg  string `json:"status_msg"`
	} `json:"base_resp"`
	Data struct {
		Days        []checkinDay `json:"days"`
		ClaimResult int          `json:"claim_result"`
		Points      float64      `json:"points"`
		DayNo       int          `json:"day_no"`
	} `json:"data"`
}

func (p *Provider) agentBase(env *provider.AccountEnv) string {
	if v := strings.TrimRight(strings.TrimSpace(env.GetCredential("agent_base_url")), "/"); v != "" {
		return v
	}
	return p.profile.agentBase
}

func (p *Provider) userID(env *provider.AccountEnv) string {
	if v := strings.TrimSpace(env.GetCredential("user_id")); v != "" {
		return v
	}
	if v := strings.TrimSpace(env.GetCredential("real_user_id")); v != "" {
		return v
	}
	return ""
}

func jsEncodeComponent(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == '!', r == '~', r == '*', r == '\'', r == '(', r == ')':
			b.WriteRune(r)
		default:
			for _, by := range []byte(string(r)) {
				b.WriteString(fmt.Sprintf("%%%02X", by))
			}
		}
	}
	return b.String()
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// signedRequest 构造 mcode 的签名请求。
func (p *Provider) signedRequest(ctx context.Context, env *provider.AccountEnv, method, path, token, userID, body string) (*http.Request, error) {
	return p.signedRequestExtra(ctx, env, method, path, token, userID, body, nil)
}

// signedRequestExtra 在标准签名参数后追加 query（积分明细的分页参数）。
func (p *Provider) signedRequestExtra(ctx context.Context, env *provider.AccountEnv, method, path, token, userID, body string, extra [][2]string) (*http.Request, error) {
	now := time.Now()
	ms := now.UnixMilli()
	sec := now.Unix()
	_, tz := now.Zone()
	pairs := [][2]string{
		{"device_platform", "web"}, {"biz_id", "3"}, {"app_id", "3001"}, {"version_code", "22201"},
		{"is_desktop", "1"}, {"desktop_version", "0.6.12"},
		{"unix", strconv.FormatInt(ms, 10)}, {"timezone_offset", strconv.Itoa(tz)},
		{"sys_language", p.profile.lang}, {"lang", p.profile.lang},
		{"device_id", "0"}, {"os_name", p.profile.osName}, {"browser_name", "mcode"},
		{"user_id", userID}, {"client", "mcode"},
	}
	pairs = append(pairs, extra...)
	var q strings.Builder
	for i, kv := range pairs {
		if i > 0 {
			q.WriteByte('&')
		}
		q.WriteString(url.QueryEscape(kv[0]))
		q.WriteByte('=')
		q.WriteString(url.QueryEscape(kv[1]))
	}
	query := q.String()
	at := path + "?" + query
	jsonBody := ""
	yyBody := "{}"
	if body != "" {
		jsonBody = body
		yyBody = body
	}
	yy := md5hex(jsEncodeComponent(at) + "_" + yyBody + md5hex(strconv.FormatInt(ms, 10)) + "ooui")
	sigInput := strconv.FormatInt(sec, 10) + signSecret
	if jsonBody != "" {
		sigInput += jsonBody
	}

	var rd io.Reader
	if jsonBody != "" {
		rd = strings.NewReader(jsonBody)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.agentBase(env)+at, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "MiniMaxCode")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("yy", yy)
	req.Header.Set("x-timestamp", strconv.FormatInt(sec, 10))
	req.Header.Set("x-signature", md5hex(sigInput))
	return req, nil
}

// identity 在未填 user_id 时读一次账号信息（签到需要 realUserID）。
func (p *Provider) identity(ctx context.Context, env *provider.AccountEnv, token string) string {
	req, err := p.signedRequest(ctx, env, http.MethodGet, "/v1/api/user/info", token, "0", "")
	if err != nil {
		return ""
	}
	resp, err := env.Client().Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var envl struct {
		Data struct {
			UserInfo map[string]any `json:"userInfo"`
		} `json:"data"`
		UserInfo map[string]any `json:"userInfo"`
	}
	if json.Unmarshal(body, &envl) != nil {
		return ""
	}
	u := envl.Data.UserInfo
	if u == nil {
		u = envl.UserInfo
	}
	for _, k := range []string{"realUserID", "real_user_id"} {
		if v, ok := u[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (p *Provider) checkinCall(ctx context.Context, env *provider.AccountEnv, method, path, token, userID, body string) (*checkinEnvelope, error) {
	req, err := p.signedRequest(ctx, env, method, path, token, userID, body)
	if err != nil {
		return nil, err
	}
	resp, err := env.Client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("MiniMax 登录态已失效，请重新登录")
	}
	var out checkinEnvelope
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("MiniMax 签到返回不是 JSON（HTTP %d）", resp.StatusCode)
	}
	if out.StatusInfo != nil && out.StatusInfo.Code != 0 {
		if out.StatusInfo.Code == 1000048 {
			return nil, fmt.Errorf("MiniMax 登录态已失效，请重新登录")
		}
		return nil, fmt.Errorf("MiniMax: %s", orDefault(out.StatusInfo.Message, "status "+strconv.Itoa(out.StatusInfo.Code)))
	}
	if out.BaseResp != nil && out.BaseResp.Code != 0 {
		return nil, fmt.Errorf("MiniMax: %s", orDefault(out.BaseResp.Msg, "status "+strconv.Itoa(out.BaseResp.Code)))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("MiniMax 签到接口 HTTP %d", resp.StatusCode)
	}
	return &out, nil
}

func orDefault(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

type checkinResult struct {
	Outcome string
	Credit  float64
	Streak  int
	Msg     string
}

func (p *Provider) checkinOnce(ctx context.Context, env *provider.AccountEnv) checkinResult {
	token := strings.TrimSpace(accessToken(env))
	if token == "" {
		return checkinResult{Outcome: provider.CheckinFailed, Msg: "MiniMax Code 账号缺少 access token"}
	}
	uid := p.userID(env)
	if uid == "" {
		uid = p.identity(ctx, env, token)
	}
	if uid == "" {
		uid = "0"
	}
	st, err := p.checkinCall(ctx, env, http.MethodGet, "/minimax-cloud/api/v1/signin/status", token, uid, "")
	if err != nil {
		return checkinResult{Outcome: provider.CheckinFailed, Msg: err.Error()}
	}
	days := st.Data.Days
	if len(days) == 0 {
		return checkinResult{Outcome: provider.CheckinInactive, Msg: "今天没有签到面板"}
	}
	today := -1
	for i, d := range days {
		if d.IsToday {
			today = i
			break
		}
	}
	if today < 0 {
		return checkinResult{Outcome: provider.CheckinInactive}
	}
	streakFrom := func(from int) int {
		n := 0
		for j := from; j >= 0 && days[j].Status == 3; j-- {
			n++
		}
		return n
	}
	switch days[today].Status {
	case 3:
		return checkinResult{Outcome: provider.CheckinDone, Credit: days[today].Points, Streak: streakFrom(today)}
	case 4:
		return checkinResult{Outcome: provider.CheckinIneligible, Msg: "今天不可签到"}
	case 2:
		claim, err := p.checkinCall(ctx, env, http.MethodPost, "/minimax-cloud/api/v1/signin/claim", token, uid, "{}")
		if err != nil {
			return checkinResult{Outcome: provider.CheckinFailed, Msg: err.Error()}
		}
		credit := claim.Data.Points
		if credit == 0 {
			credit = days[today].Points
		}
		streak := streakFrom(today-1) + 1
		switch claim.Data.ClaimResult {
		case 1:
			return checkinResult{Outcome: provider.CheckinClaimed, Credit: credit, Streak: streak}
		case 2:
			return checkinResult{Outcome: provider.CheckinDone, Credit: credit, Streak: streak}
		default:
			return checkinResult{Outcome: provider.CheckinFailed, Msg: fmt.Sprintf("未知 claim_result %d", claim.Data.ClaimResult)}
		}
	default: // 1 未开始
		return checkinResult{Outcome: provider.CheckinInactive, Msg: "今天的签到还没开始"}
	}
}

// ExecuteTasks 执行每日签到。
func (p *Provider) ExecuteTasks(ctx context.Context, env *provider.AccountEnv, taskIDs []string) (*provider.CheckInReport, error) {
	day := provider.CheckinDay(time.Now())
	res := p.checkinOnce(ctx, env)
	report := &provider.CheckInReport{
		Outcome: res.Outcome, Credit: res.Credit, Streak: res.Streak, Day: day, Message: res.Msg,
	}
	report.Success = res.Outcome == provider.CheckinClaimed || res.Outcome == provider.CheckinDone
	report.EarnedPoints = int(res.Credit)
	if len(taskIDs) == 0 {
		taskIDs = []string{"daily_checkin"}
	}
	for _, id := range taskIDs {
		report.Results = append(report.Results, provider.TaskResult{
			TaskID: id, Title: "每日签到", Success: report.Success, Message: res.Msg,
			Points: int(res.Credit), Skipped: res.Outcome == provider.CheckinInactive,
		})
	}
	return report, nil
}
