package gateway

// 用量页的每日签到：卡片标注 + 手动「立即签到」。
//
// 自动签到由 scheduler 按渠道 cron 跑；这里负责：
//   - 把渠道级签到开关（设置页的「每日签到」）接进调度器；
//   - 在额度卡片上标出该账号今天签没签、领到多少；
//   - POST /api/usage/<vendor>-checkin 立即为今天还没签的账号签到。
// 只有真正实现 CheckInCapability 且声明了任务的渠道才会出现签到行，
// 不做"看起来支持、点了没反应"的假开关。

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"aether-cockpit/internal/core/scheduler"
	"aether-cockpit/internal/provider"
)

// quotaCheckin 是卡片上的签到状态（前端 checkinRow 直接消费）。
type quotaCheckin struct {
	Day     string  `json:"day,omitempty"`
	Outcome string  `json:"outcome,omitempty"`
	Credit  float64 `json:"credit,omitempty"`
	Streak  int     `json:"streak,omitempty"`
	Msg     string  `json:"msg,omitempty"`
	Again   bool    `json:"again,omitempty"`
}

// checkinVendor 把渠道映射到前端认识的签到厂商：
// "" 是 WorkBuddy（默认），"trae" / "minimax" / "qoder" 各自一组开关。
func checkinVendor(providerID string) (string, bool) {
	switch providerID {
	case "workbuddy-cn":
		return "", true
	case "trae-cn", "trae-solo-cn":
		return "trae", true
	case "qoder-cn", "qoder-intl":
		return "qoder", true
	case "minimax-code", "minimax-code-global":
		return "minimax", true
	}
	return "", false
}

// vendorProviders 是一个签到开关覆盖的渠道列表。
var vendorProviders = map[string][]string{
	"workbuddy": {"workbuddy-cn"},
	"trae":      {"trae-cn", "trae-solo-cn"},
	"qoder":     {"qoder-cn", "qoder-intl"},
	"minimax":   {"minimax-code", "minimax-code-global"},
}

var vendorSettingField = map[string]string{
	"workbuddy": "workbuddyCheckin",
	"trae":      "traeCheckin",
	"qoder":     "qoderCheckin",
	"minimax":   "minimaxCheckin",
}

// checkinSupported 判断渠道是否真的能签到（实现了能力且声明了任务）。
func checkinSupported(p provider.BaseProvider) bool {
	if _, ok := provider.AsCheckIn(p); !ok {
		return false
	}
	return len(p.Manifest().Tasks) > 0
}

// checkinEnabled 是调度器的渠道级开关：未映射的渠道（mock）不受影响。
func (g *Gateway) checkinEnabled(providerID string) bool {
	vendor, ok := checkinVendor(providerID)
	if !ok {
		return true
	}
	return g.checkinPrefOn(vendor)
}

// checkinPrefOn 读设置页保存的开关。默认关：签到会以用户身份请求上游，
// 必须由用户显式打开。
func (g *Gateway) checkinPrefOn(vendor string) bool {
	field := vendorSettingField[vendor]
	if field == "" {
		return false
	}
	g.settingsMu.Lock()
	raw := g.settings.UI[field]
	if raw == nil {
		raw = g.settings.UI[vendor+"-checkin"]
	}
	g.settingsMu.Unlock()
	return settingTruthy(raw)
}

func settingTruthy(raw any) bool {
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		return v == "1" || v == "true" || v == "on"
	case map[string]any:
		return settingTruthy(v["on"])
	}
	return false
}

// checkinProvisional 标记"也许是厂商前一天的回答"：北京时间中午前，
// 上游说已签/没有活动时，它的日界线可能还没过，稍后要再问一次。
func checkinProvisional(outcome string, at int64) bool {
	switch outcome {
	case provider.CheckinDone, provider.CheckinInactive, provider.CheckinIneligible:
	default:
		return false
	}
	if at == 0 {
		return false
	}
	return time.Unix(at, 0).In(time.FixedZone("CST", 8*3600)).Hour() < 12
}

// todayCheckinCard 取该账号今天已记录的签到结果（自动或手动）。
func (g *Gateway) todayCheckinCard(accountID string) *quotaCheckin {
	s := g.sched.Load()
	if s == nil {
		return nil
	}
	r, ok := s.State().TodayResults(accountID)["daily_checkin"]
	if !ok {
		return nil
	}
	c := &quotaCheckin{
		Day:     r.Day,
		Outcome: r.Outcome,
		Credit:  r.Credit,
		Streak:  r.Streak,
		Msg:     r.Message,
	}
	if c.Day == "" {
		c.Day = scheduler.DayKey(time.Now())
	}
	if c.Outcome == "" {
		if r.Success {
			c.Outcome = provider.CheckinClaimed
		} else {
			c.Outcome = provider.CheckinFailed
		}
	}
	c.Again = checkinProvisional(c.Outcome, r.At)
	return c
}

// annotateCheckin 给卡片补上签到信息；不支持的渠道原样返回。
func (g *Gateway) annotateCheckin(card *quotaCard, env *provider.AccountEnv, p provider.BaseProvider) {
	if !checkinSupported(p) {
		return
	}
	vendor, ok := checkinVendor(env.ProviderID)
	if !ok {
		return
	}
	card.Checkins = true
	card.CheckinBy = vendor
	card.CheckinOff = env.Disabled()
	card.Checkin = g.todayCheckinCard(env.AccountID)
}

// checkinPressResult 是「立即签到」单个账号的结果（前端 checkinPressSaid）。
type checkinPressResult struct {
	User    string  `json:"user,omitempty"`
	Outcome string  `json:"outcome"`
	Asked   bool    `json:"asked,omitempty"`
	Credit  float64 `json:"credit,omitempty"`
	Streak  int     `json:"streak,omitempty"`
	Msg     string  `json:"msg,omitempty"`
	Again   bool    `json:"again,omitempty"`
}

// handleUsageCheckIn 生成厂商的「立即签到」处理器。
func (g *Gateway) handleUsageCheckIn(vendor string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.pool == nil {
			writeJSON(w, http.StatusOK, []checkinPressResult{})
			return
		}
		providers := vendorProviders[vendor]
		if len(providers) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": fmt.Sprintf("没有可签到的 %s 渠道：本项目未内置该厂商", vendor),
			})
			return
		}
		out := make([]checkinPressResult, 0, 8)
		for _, providerID := range providers {
			p, ok := provider.Get(providerID)
			if !ok {
				continue
			}
			cap, ok := provider.AsCheckIn(p)
			if !ok {
				continue
			}
			for _, env := range g.pool.ListByProvider(providerID) {
				if !env.Available() || env.Disabled() {
					continue
				}
				user := env.Label
				if user == "" {
					user = env.AccountID
				}
				// 今天已经确定领过的不再打一次；done 在中午前可能还是
				// 厂商前一天的答案，允许再问一次。
				if prev := g.todayCheckinCard(env.AccountID); prev != nil {
					settled := prev.Outcome == provider.CheckinClaimed ||
						(prev.Outcome == provider.CheckinDone && !prev.Again)
					if settled {
						out = append(out, checkinPressResult{
							User: user, Outcome: prev.Outcome, Credit: prev.Credit,
							Streak: prev.Streak, Msg: prev.Msg, Again: prev.Again,
						})
						continue
					}
				}

				ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
				report, err := cap.ExecuteTasks(ctx, env, []string{"daily_checkin"})
				cancel()

				res := checkinPressResult{User: user, Asked: true}
				if err != nil {
					res.Outcome = provider.CheckinFailed
					res.Msg = err.Error()
				} else if report != nil {
					res.Outcome = report.Outcome
					if res.Outcome == "" {
						if report.Success {
							res.Outcome = provider.CheckinClaimed
						} else {
							res.Outcome = provider.CheckinFailed
						}
					}
					res.Credit = report.Credit
					res.Streak = report.Streak
					res.Msg = report.Message
				} else {
					res.Outcome = provider.CheckinFailed
					res.Msg = "上游没有返回签到结果"
				}
				res.Again = checkinProvisional(res.Outcome, time.Now().Unix())
				g.recordCheckin(env, res)
				out = append(out, res)

				// 同渠道多账号串行且留出间隔：同一出口 IP 上固定间隔
				// 连续打卡是最容易被风控识别的形状。
				select {
				case <-r.Context().Done():
					return
				case <-time.After(300 * time.Millisecond):
				}
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// recordCheckin 把手动结果写进调度器状态，卡片刷新后立即可见。
func (g *Gateway) recordCheckin(env *provider.AccountEnv, res checkinPressResult) {
	s := g.sched.Load()
	if s == nil {
		return
	}
	s.State().Record(env.AccountID, "daily_checkin", scheduler.Result{
		Success: res.Outcome == provider.CheckinClaimed || res.Outcome == provider.CheckinDone,
		Points:  int(res.Credit),
		Message: res.Msg,
		Outcome: res.Outcome,
		Credit:  res.Credit,
		Streak:  res.Streak,
		Day:     provider.CheckinDay(time.Now()),
		Reason:  "manual",
		At:      time.Now().Unix(),
	}, scheduler.DayKey(time.Now()))
}
