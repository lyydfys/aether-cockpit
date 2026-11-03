package gateway

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"time"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/provider"
)

// 额度巡检：在订阅窗口"快到期但还剩很多"时提醒，在"快用完"时告警。
//
// 为什么这件事值钱：用户花钱买的窗口额度到期就作废。数据本来就全在
// QuotaWindow.ResetsAt / Used 里，缺的只是一个定时判定 + 去重 + 通知。
//
// 去重策略：同一 (渠道, 账号, 窗口) 的同一类告警在 quotaAlertCooldown 内
// 只报一次；类型变了（低额度 → 即将重置）立刻重报。去重状态落盘，重启后
// 不会把已经报过的再刷一遍。

const (
	// quotaAlertInterval 是巡检周期：5 分钟足够及时，代价也低。
	quotaAlertInterval = 5 * time.Minute
	// quotaAlertLowPercent 低于该剩余比例时告警。
	quotaAlertLowPercent = 15.0
	// quotaAlertExpiringPercent 剩余高于该比例才值得提醒"别浪费"。
	quotaAlertExpiringPercent = 50.0
	// quotaAlertExpiringWindow 距重置小于该时长才提醒。
	quotaAlertExpiringWindow = 12 * time.Hour
	// quotaAlertCooldown 同类告警的最短间隔。
	quotaAlertCooldown = 6 * time.Hour
)

// QuotaAlert 是一条额度提醒。
type QuotaAlert struct {
	// Key 是去重键：provider|account|window|kind。
	Key        string  `json:"key"`
	Kind       string  `json:"kind"` // low | expiring | exhausted
	ProviderID string  `json:"provider_id"`
	Provider   string  `json:"provider,omitempty"`
	AccountID  string  `json:"account_id"`
	Account    string  `json:"account,omitempty"`
	Window     string  `json:"window,omitempty"`
	Used       float64 `json:"used,omitempty"`
	ResetsAt   int64   `json:"resets_at,omitempty"`
	Message    string  `json:"message"`
	At         int64   `json:"at"`
}

// quotaAlertState 是落盘的去重状态（每个 key 一条）。
type quotaAlertState struct {
	Kind string `json:"kind"`
	At   int64  `json:"at"`
}

// StartQuotaAlertWatch 启动额度巡检，返回停止函数。
func (g *Gateway) StartQuotaAlertWatch(ctx context.Context, interval time.Duration) func() {
	if interval <= 0 {
		interval = quotaAlertInterval
	}
	// 启动时先跑一次：否则刚开机的十几个小时里什么都收不到。
	g.evaluateQuotaAlerts(time.Now())
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-t.C:
				g.evaluateQuotaAlerts(time.Now())
			}
		}
	}()
	return func() { close(stop) }
}

// evaluateQuotaAlerts 扫一遍账号额度，产出需要提醒的条目。
func (g *Gateway) evaluateQuotaAlerts(now time.Time) {
	if g.pool == nil {
		return
	}
	alerts := g.collectQuotaAlerts(now)
	// 去重：同类告警在冷却期内不重复。
	kept := make([]QuotaAlert, 0, len(alerts))
	g.settingsMu.Lock()
	if g.settings.QuotaAlerts == nil {
		g.settings.QuotaAlerts = map[string]quotaAlertState{}
	}
	for _, a := range alerts {
		if prev, ok := g.settings.QuotaAlerts[a.Key]; ok && prev.Kind == a.Kind {
			if now.Unix()-prev.At < int64(quotaAlertCooldown/time.Second) {
				continue
			}
		}
		g.settings.QuotaAlerts[a.Key] = quotaAlertState{Kind: a.Kind, At: a.At}
		kept = append(kept, a)
	}
	g.settingsMu.Unlock()
	if len(kept) == 0 {
		return
	}
	if err := g.saveSettings(); err != nil {
		log.Printf("[WARN] 额度提醒状态落盘失败: %v", err)
	}
	for _, a := range kept {
		eventbus.Emit(eventbus.TopicAccount+".quota_alert", map[string]any{
			"key": a.Key, "kind": a.Kind, "provider_id": a.ProviderID,
			"account_id": a.AccountID, "message": a.Message,
		})
	}
}

// collectQuotaAlerts 是纯函数：给定账号池与时间，算出告警列表（不做去重）。
//
// 遍历账号池而不是渠道清单：有账号才有额度可提醒，而且不依赖具体渠道是否
// 在当前构建里注册（测试与精简构建都可能没有）。
func (g *Gateway) collectQuotaAlerts(now time.Time) []QuotaAlert {
	out := make([]QuotaAlert, 0, 8)
	nowUnix := now.Unix()
	for _, env := range g.pool.All() {
		st := env.Quota()
		if st == nil {
			continue
		}
		display := env.ProviderID
		if m, ok := provider.Get(env.ProviderID); ok {
			if name := m.Manifest().DisplayName; name != "" {
				display = name
			}
		}
		for _, w := range st.Windows {
			if w.Name == "" || w.ResetsAt <= 0 {
				continue
			}
			remainingPercent := 100 - w.Used
			base := QuotaAlert{
				ProviderID: env.ProviderID, Provider: display,
				AccountID: env.AccountID, Account: env.Label,
				Window: w.Name, Used: w.Used, ResetsAt: w.ResetsAt, At: nowUnix,
			}
			switch {
			case remainingPercent <= 0.5:
				base.Kind = "exhausted"
				base.Message = fmt.Sprintf("%s · %s 的 %s 额度已用尽（%.0f%%），%s重置",
					base.Provider, base.Account, w.Name, w.Used, humanUntil(w.ResetsAt, nowUnix))
			case remainingPercent <= quotaAlertLowPercent:
				base.Kind = "low"
				base.Message = fmt.Sprintf("%s · %s 的 %s 额度只剩 %.0f%%，%s重置",
					base.Provider, base.Account, w.Name, remainingPercent, humanUntil(w.ResetsAt, nowUnix))
			case remainingPercent >= quotaAlertExpiringPercent && w.ResetsAt > nowUnix && w.ResetsAt-nowUnix <= int64(quotaAlertExpiringWindow/time.Second):
				// 最值钱的一条：还剩一大半，但窗口马上到期。
				base.Kind = "expiring"
				base.Message = fmt.Sprintf("%s · %s 的 %s 还剩 %.0f%% 没用，%s就重置了",
					base.Provider, base.Account, w.Name, remainingPercent, humanUntil(w.ResetsAt, nowUnix))
			default:
				continue
			}
			base.Key = fmt.Sprintf("%s|%s|%s|%s", env.ProviderID, env.AccountID, w.Name, base.Kind)
			out = append(out, base)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			// expiring 排最前：它直接关系到"钱要作废了"。
			return alertRank(out[i].Kind) < alertRank(out[j].Kind)
		}
		return out[i].Used > out[j].Used
	})
	return out
}

func alertRank(kind string) int {
	switch kind {
	case "expiring":
		return 0
	case "exhausted":
		return 1
	default:
		return 2
	}
}

// humanUntil 把重置时间写成"约 N 小时/分钟"。
func humanUntil(resetsAt, now int64) string {
	d := time.Duration(resetsAt-now) * time.Second
	switch {
	case d <= 0:
		return "即将"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟后", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%.0f 小时后", d.Hours())
	default:
		return fmt.Sprintf("%.0f 天后", d.Hours()/24)
	}
}

// ActiveQuotaAlerts 返回此刻仍然成立的告警（不看去重记录）。
//
// 与 evaluateQuotaAlerts 的区别：这里给页面读，所以不写去重状态、不发事件，
// 但要求"告警条件此刻仍然成立"——已经重置的窗口不该继续显示。
func (g *Gateway) ActiveQuotaAlerts() []QuotaAlert {
	if g.pool == nil {
		return nil
	}
	now := time.Now()
	out := make([]QuotaAlert, 0, 8)
	for _, a := range g.collectQuotaAlerts(now) {
		if a.ResetsAt <= now.Unix() {
			continue // 已经重置，告警自然失效
		}
		out = append(out, a)
	}
	return out
}

// handleQuotaAlerts GET /api/alerts —— 当前仍然成立的额度提醒。
func (g *Gateway) handleQuotaAlerts(w http.ResponseWriter, r *http.Request) {
	alerts := g.ActiveQuotaAlerts()
	if alerts == nil {
		alerts = []QuotaAlert{}
	}
	// rejected 让页面能回答"今天被限流拒了多少次"。
	rejected := map[string]int64{}
	if g.keyQuota != nil {
		for _, k := range g.callerKeyView() {
			if id, _ := k["id"].(string); id != "" {
				if n := g.keyQuota.rejectedCount(id); n > 0 {
					rejected[id] = n
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": alerts, "rejected": rejected})
}
