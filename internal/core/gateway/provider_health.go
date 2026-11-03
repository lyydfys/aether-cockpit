package gateway

// 渠道健康巡检：某个渠道的账号全部失效或耗尽时自动下线，恢复额度后自动上线。
//
// 为什么要做：路由按模型名把请求分给候选渠道，候选渠道里的账号如果全灭，
// 每次请求都要白走一遍选号、最后返回一条 503。更糟的是前端看到渠道还是亮的，
// 用户以为"能用"，只有点进去才发现账号全挂了。
//
// 边界（刻意为之）：
//   - 只处理 depleted / invalid。cooling_down 与 refreshing 是临时状态，等得起，
//     不能因为一次 429 就把整条渠道关掉。
//   - 一个账号都没有的渠道不碰：那是"还没配置"，关掉会让用户找不到入口去加账号。
//   - 手动停用（DisabledProviders）优先级更高，自动状态不会覆盖它，也不会替它恢复。

import (
	"context"
	"time"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/provider"
)

// AutoDisableState 记录一次自动下线。
type AutoDisableState struct {
	// Reason：depleted（额度耗尽）/ invalid（凭证失效）。
	Reason string `json:"reason"`
	// Until 是预计恢复时间（unix 秒），0 表示只能等人工处理。
	Until int64 `json:"until,omitempty"`
	// At 是下线时间，供前端显示"已自动关闭多久"。
	At int64 `json:"at"`
}

// providerHealthInterval 是巡检周期。纯内存读取，代价可以忽略。
const providerHealthInterval = time.Minute

// StartProviderHealthWatch 启动后台巡检，返回停止函数。
func (g *Gateway) StartProviderHealthWatch(ctx context.Context, interval time.Duration) func() {
	if interval <= 0 {
		interval = providerHealthInterval
	}
	g.evaluateProviderHealth()
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
				g.evaluateProviderHealth()
			}
		}
	}()
	return func() { close(stop) }
}

// evaluateProviderHealth 重新判定每个渠道该不该自动下线。
//
// 双向：全灭就下线，出现可用账号就上线——这就是"额度恢复后自动开启"。
// 只有集合发生变化时才落盘，避免每分钟写一次 settings.json。
func (g *Gateway) evaluateProviderHealth() {
	if g.pool == nil {
		return
	}
	next := make(map[string]AutoDisableState, 8)
	now := time.Now().Unix()

	for _, m := range provider.AllManifests() {
		envs := g.pool.ListByProvider(m.ID)
		if len(envs) == 0 {
			continue // 未配置账号的渠道不参与
		}
		alive, reason, until := judgeProviderHealth(envs, now)
		if alive || reason == "" {
			continue
		}
		next[m.ID] = AutoDisableState{Reason: reason, Until: until, At: now}
	}

	g.settingsMu.Lock()
	changed := !sameAutoDisabled(g.settings.AutoDisabledProviders, next)
	if changed {
		g.settings.AutoDisabledProviders = next
	}
	g.settingsMu.Unlock()
	if !changed {
		return
	}
	if err := g.saveSettings(); err != nil {
		return
	}

	// 广播变化，前端不用轮询也能看到渠道变灰/变亮。
	for pid, st := range next {
		eventbus.Emit(eventbus.TopicAccount+".provider_auto_off", map[string]any{
			"provider_id": pid, "reason": st.Reason, "until": st.Until,
		})
	}
	for pid := range g.autoDisabledSnapshot() {
		if _, still := next[pid]; !still {
			eventbus.Emit(eventbus.TopicAccount+".provider_auto_on", map[string]any{"provider_id": pid})
		}
	}
}

// judgeProviderHealth 判定一个渠道是否"全灭"，以及最早什么时候能缓过来。
//
// alive=true 表示还能接活：ready 直接算活；cooling_down / refreshing 是临时状态，
// 等一会儿就回来，也不能算死（否则一次 429 就把整条渠道关掉）。
func judgeProviderHealth(envs []*provider.AccountEnv, now int64) (alive bool, reason string, until int64) {
	for _, e := range envs {
		if e.Disabled() {
			continue // 手动停用的账号不影响渠道判定
		}
		state, _, reset := e.StateDetail()
		switch state {
		case provider.StateReady:
			return true, "", 0
		case provider.StateCoolingDown, provider.StateRefreshing:
			return true, "", 0
		case provider.StateDepleted:
			if reason == "" {
				reason = "depleted"
			}
			if r := reset.Unix(); r > now && (until == 0 || r < until) {
				until = r
			}
		case provider.StateInvalid:
			if reason == "" {
				reason = "invalid"
			}
		}
	}
	return false, reason, until
}

// sameAutoDisabled 比较两份自动下线表，避免无变化时写盘。
func sameAutoDisabled(a, b map[string]AutoDisableState) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || va.Reason != vb.Reason || va.Until != vb.Until {
			return false
		}
	}
	return true
}

// autoDisabledSnapshot 返回当前自动下线表的副本（调用方在锁外读）。
func (g *Gateway) autoDisabledSnapshot() map[string]AutoDisableState {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	out := make(map[string]AutoDisableState, len(g.settings.AutoDisabledProviders))
	for k, v := range g.settings.AutoDisabledProviders {
		out[k] = v
	}
	return out
}

// autoDisableOf 返回某渠道的自动下线信息。
//
// 已过 Until 的记录视为"应该恢复但巡检还没跑到"，这里按未下线处理：
// 账号的 resetAt 到期后 Available() 会自己转 ready，路由此刻放行不会白撞。
func (g *Gateway) autoDisableOf(pid string) (AutoDisableState, bool) {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	st, ok := g.settings.AutoDisabledProviders[pid]
	if !ok {
		return AutoDisableState{}, false
	}
	if st.Until > 0 && time.Now().Unix() >= st.Until {
		return st, false
	}
	return st, true
}

// clearAutoDisable 在用户手动启用渠道时清掉自动状态，
// 否则"刚点开又被自动关回去"会让人以为开关坏了。
func (g *Gateway) clearAutoDisable(pid string) {
	g.settingsMu.Lock()
	if _, ok := g.settings.AutoDisabledProviders[pid]; !ok {
		g.settingsMu.Unlock()
		return
	}
	delete(g.settings.AutoDisabledProviders, pid)
	g.settingsMu.Unlock()
	_ = g.saveSettings()
}
