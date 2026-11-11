package gateway

// 渠道级"每个密钥/账号的上限"（渠道编辑器 高级 → Concurrency / Requests per minute）。
//
// 这两项此前也只在前端 draft 里，providerDraft 没有对应字段，保存时被静默
// 丢弃；界面还会把全局并发值当成渠道自己的值显示。现在：
//   - 保存进 settings.ProviderLimits；
//   - 账号级覆盖 > 渠道级覆盖 > 全局（与 concurrencyFor 的既有优先级一致）；
//   - 提交的值等于全局值时视为"跟随全局"，删除覆盖，避免把全局值固化到每个渠道。
//
// 队列大小 / 队列等待（queueLimit/queueWait）仍是全局闸门，见 GAP §241 的未做项。

// ProviderLimitCfg 是某渠道的上限覆盖；nil 字段表示跟随全局。
type ProviderLimitCfg struct {
	// Concurrency 是每个账号同时可发往上游的请求数；0 = 不限。
	Concurrency *int `json:"concurrency,omitempty"`
	// MaxRPM 是每个账号每分钟请求上限；0 = 不限。
	MaxRPM *int `json:"max_rpm,omitempty"`
	// QueueLimit 是"每个账号最多多少请求在排队"；未设置 = 跟随全局。
	QueueLimit *int `json:"queue_limit,omitempty"`
	// QueueWait 是"请求为等空位最多等多久（秒）"；未设置 = 跟随全局。
	QueueWait *int `json:"queue_wait,omitempty"`
}

// providerLimits 返回某渠道的上限覆盖（未设置的字段为 nil）。
func (g *Gateway) providerLimits(providerID string) ProviderLimitCfg {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	return g.settings.ProviderLimits[providerID]
}

// providerConcurrency 返回渠道级并发覆盖；ok=false 表示跟随全局。
func (g *Gateway) providerConcurrency(providerID string) (int, bool) {
	if v := g.providerLimits(providerID).Concurrency; v != nil {
		return *v, true
	}
	return 0, false
}

// providerQueueLimit 返回渠道级队列长度覆盖；ok=false 表示跟随全局。
func (g *Gateway) providerQueueLimit(providerID string) (int, bool) {
	if v := g.providerLimits(providerID).QueueLimit; v != nil {
		return *v, true
	}
	return 0, false
}

// providerQueueWait 返回渠道级排队等待覆盖（秒）；ok=false 表示跟随全局。
func (g *Gateway) providerQueueWait(providerID string) (int, bool) {
	if v := g.providerLimits(providerID).QueueWait; v != nil {
		return *v, true
	}
	return 0, false
}

// providerRPM 返回渠道级 RPM 覆盖；ok=false 表示跟随全局。
// providerTPM 返回渠道级每分钟输入 token 上限（未配置时 ok=false，跟随全局）。
func (g *Gateway) providerTPM(providerID string) (int, bool) {
	if v := g.providerLimits(providerID).MaxRPM; v != nil {
		// 目前没有独立的 token 上限字段：渠道级 RPM 已经能覆盖绝大多数
		// "这个渠道每分钟只给我这么多"的场景，这里不重复造一个字段。
		_ = v
	}
	return 0, false
}

func (g *Gateway) providerRPM(providerID string) (int, bool) {
	if v := g.providerLimits(providerID).MaxRPM; v != nil {
		return *v, true
	}
	return 0, false
}

// setProviderLimits 保存渠道级上限。等于全局值的项被清除（跟随全局）；
// 两项都清除时删除条目。
func (g *Gateway) setProviderLimits(providerID string, concurrency, maxRPM, queueLimit, queueWait *int) error {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.ProviderLimits == nil {
		g.settings.ProviderLimits = map[string]ProviderLimitCfg{}
	}
	cfg := g.settings.ProviderLimits[providerID]
	globalConc := g.cfg.MaxConcurrentPerAccount
	if globalConc == 0 {
		globalConc = 8
	}
	if globalConc < 0 {
		globalConc = 0
	}
	if concurrency != nil {
		v := *concurrency
		if v < 0 {
			v = 0
		}
		if v > 1000 {
			v = 1000
		}
		if v == globalConc {
			cfg.Concurrency = nil
		} else {
			cfg.Concurrency = &v
		}
	}
	if maxRPM != nil {
		v := *maxRPM
		if v < 0 {
			v = 0
		}
		if v > 10000 {
			v = 10000
		}
		if v == g.cfg.RPMPerAccount {
			cfg.MaxRPM = nil
		} else {
			cfg.MaxRPM = &v
		}
	}
	if queueLimit != nil {
		v := *queueLimit
		if v < 0 {
			v = 0
		}
		if v > 10000 {
			v = 10000
		}
		if v == 0 {
			cfg.QueueLimit = nil
		} else {
			cfg.QueueLimit = &v
		}
	}
	if queueWait != nil {
		v := *queueWait
		if v < 0 {
			v = 0
		}
		if v > 3600 {
			v = 3600
		}
		if v == 0 {
			cfg.QueueWait = nil
		} else {
			cfg.QueueWait = &v
		}
	}
	if cfg.Concurrency == nil && cfg.MaxRPM == nil && cfg.QueueLimit == nil && cfg.QueueWait == nil {
		delete(g.settings.ProviderLimits, providerID)
	} else {
		g.settings.ProviderLimits[providerID] = cfg
	}
	return g.saveSettings()
}

// providerConcurrencyView / providerRPMView 用于 /api/providers 回显：
// 有覆盖给覆盖值，否则给当前生效的全局值（与界面此前看到的一致）。
func (g *Gateway) providerConcurrencyView(providerID string) int {
	if v, ok := g.providerConcurrency(providerID); ok {
		return v
	}
	return g.maxConcurrentPerAccount()
}

func (g *Gateway) providerRPMView(providerID string) int {
	if v, ok := g.providerRPM(providerID); ok {
		return v
	}
	return g.settingsRPMPerAccount()
}

// providerQueueLimitView / providerQueueWaitView 回显覆盖值；未设置回 0
// （编辑器据此显示空，表示跟随全局）。
func (g *Gateway) providerQueueLimitView(providerID string) int {
	if v, ok := g.providerQueueLimit(providerID); ok {
		return v
	}
	return 0
}

func (g *Gateway) providerQueueWaitView(providerID string) int {
	if v, ok := g.providerQueueWait(providerID); ok {
		return v
	}
	return 0
}

// settingsRPMPerAccount 读全局 RPM（加锁，供 view 使用）。
func (g *Gateway) settingsRPMPerAccount() int {
	return g.cfg.RPMPerAccount
}
