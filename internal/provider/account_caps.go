package provider

import (
	"math"
	"strings"
	"time"
)

// WindowCaps 是"用到窗口的多少就按住这个账号"的配额闸。
//
// 为什么需要它：订阅型渠道的限流按窗口（Claude 5h/7d、Codex primary/secondary）
// 计，一旦打满，接下来整个窗口的所有请求都会被上游拒绝。等到 429 再反应已经
// 晚了——那一击本身会失败，还可能让账号进入更长的冷却。按百分比提前收手，
// 可以把"用满即挂"变成"留一点余量给突发"。
//
// 语义（对齐 aether 的 WindowCaps）：
//   - All：对该账号的**所有**窗口生效的默认份额，0 或 100 表示不限制；
//   - Windows：按窗口名覆盖，键做小写去空白归一（见 WindowCapID）；
//   - 份额 1..99 表示"用到这个百分比就按住"，100 表示该窗口免 cap。
//
// fail-open：读数缺失（渠道没有额度接口、还没查过）时**不算按住**。
// 否则没有额度接口的渠道会被永久按住——那是把"看不见"当成"用满了"。
type WindowCaps struct {
	All     int            `json:"all,omitempty"`
	Windows map[string]int `json:"windows,omitempty"`
}

// WindowCapID 把窗口名归一成 cap 的键：去空白、转小写。
// 上游改大小写（"5 Hours" / "5 hours"）不该让 cap 失效。
func WindowCapID(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// Of 返回某个窗口生效的份额（0 或 100 都表示不限制）。
//
// 先按归一化键直接查，未命中再线性扫一遍比较归一化后的键：
// 配置可能来自手写 JSON（键带空格或大小写不一致），而窗口只有几个，
// 一次线性扫描的代价可以忽略，换来的是一致行为。
func (c WindowCaps) Of(name string) int {
	if c.Windows != nil {
		key := WindowCapID(name)
		if v, ok := c.Windows[key]; ok && v > 0 {
			return v
		}
		for k, v := range c.Windows {
			if WindowCapID(k) == key && v > 0 {
				return v
			}
		}
	}
	if c.All > 0 {
		return c.All
	}
	return 0
}

// Holds 判断这组 cap 是否会真的按住任何窗口（用于 UI 与快速短路）。
func (c WindowCaps) Holds() bool {
	if c.All > 0 && c.All < 100 {
		return true
	}
	for _, v := range c.Windows {
		if v > 0 && v < 100 {
			return true
		}
	}
	return false
}

// SetWindowCaps 设置账号的窗口 cap（管理端/落盘恢复用）。
func (e *AccountEnv) SetWindowCaps(c WindowCaps) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.windowCaps = c
}

// WindowCaps 返回账号当前的窗口 cap。
func (e *AccountEnv) WindowCaps() WindowCaps {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.windowCaps
}

// CapHeld 判断账号是否已经触到某个窗口的 cap。
//
// 返回命中的窗口名与份额，供路由 trace 说明"为什么排在后面"。
// 只读不做状态迁移：cap 是"排序偏好"，不是"不可用"——
// 全池都被 cap 时仍然要能发请求（否则等于自己把自己锁死）。
func (e *AccountEnv) CapHeld(now time.Time) (held bool, window string, share int) {
	e.mu.RLock()
	caps := e.windowCaps
	quota := e.quota
	e.mu.RUnlock()

	if !caps.Holds() || quota == nil {
		return false, "", 0
	}
	for _, w := range quota.Windows {
		cap := caps.Of(w.Name)
		if cap <= 0 || cap >= 100 {
			continue
		}
		used, known := windowUsedPercent(w, now)
		if !known {
			// 没有读数：不算按住（fail-open）。
			continue
		}
		if used >= float64(cap) {
			return true, w.Name, cap
		}
	}
	return false, "", 0
}

// windowUsedPercent 计算窗口已用百分比。
//
// 优先用上游给的 Used；上游只给 Limit/Amount 时用 Amount/Limit 反推；
// 两者都没有就返回 known=false。
func windowUsedPercent(w QuotaWindow, now time.Time) (float64, bool) {
	// 窗口已经过期（重置时间早于现在）：视为新一轮，已用 0。
	if w.ResetsAt > 0 && now.Unix() >= w.ResetsAt {
		return 0, true
	}
	if w.Used > 0 {
		return math.Min(100, w.Used), true
	}
	if w.Limit > 0 && w.Amount >= 0 {
		return math.Min(100, float64(w.Amount)*100/float64(w.Limit)), true
	}
	return 0, false
}
