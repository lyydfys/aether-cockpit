package pool

import "aether-cockpit/internal/provider"

// LatencyFor 返回该渠道当前最快的时延估计。
//
// 取该渠道所有账号里 TTFT 最小的那个——路由决策关心的是"这个渠道现在最快能多快"，
// 而不是平均值：一个渠道只要有一个健康账号快，就值得优先尝试。
// 渠道还没有任何样本时 ok=false（策略应把已知样本排在前面，未知的保持原序靠后）。
func (m *Manager) LatencyFor(providerID string) (ttftMS, tps float64, ok bool) {
	m.mu.RLock()
	envs := make([]*provider.AccountEnv, len(m.byProvider[providerID]))
	copy(envs, m.byProvider[providerID])
	m.mu.RUnlock()

	for _, env := range envs {
		t, p, has := env.Latency()
		if !has {
			continue
		}
		if !ok || t < ttftMS {
			ttftMS, tps, ok = t, p, true
		}
	}
	return ttftMS, tps, ok
}
