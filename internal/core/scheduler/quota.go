package scheduler

import (
	"context"
	"time"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/provider"
)

// quotaLoop 周期性刷新所有账号的额度。
//
// 三个必须做对的点：
//  1. **串行**：刷新是向每个上游发真实请求，并发会被识别成异常流量；
//  2. **有抖动**：账号之间插入 0.8~2 秒随机间隔，避免固定节奏；
//  3. **失败静默**：某个账号查询失败不影响其他账号，也不改变其状态
//     （额度查不到 ≠ 账号失效，不能因此熔断）。
func (s *Scheduler) quotaLoop() {
	defer s.wg.Done()

	// 首次刷新延迟启动，避开启动瞬间的账号状态检查。
	select {
	case <-s.ctx.Done():
		return
	case <-time.After(s.opts.QuotaRefreshInitialDelay):
	}
	s.refreshQuotas()

	t := time.NewTicker(s.opts.QuotaRefresh)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.refreshQuotas()
		}
	}
}

// refreshQuotas 遍历全部账号刷新额度。
func (s *Scheduler) refreshQuotas() {
	envs := s.pool.All()
	if len(envs) == 0 {
		return
	}

	refreshed, failed := 0, 0
	for _, env := range envs {
		if s.ctx.Err() != nil {
			return
		}
		// 失效 / 冷却中的账号不刷：既浪费请求，也可能加重风控。
		if !env.Available() {
			continue
		}

		p, ok := provider.Get(env.ProviderID)
		if !ok {
			continue
		}
		cap, ok := provider.AsQuota(p)
		if !ok {
			continue // 渠道没实现额度查询能力
		}

		ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
		q, err := cap.QueryQuota(ctx, env)
		cancel()

		if err != nil {
			failed++
		} else if q != nil {
			env.SetQuota(q)
			// 额度确实耗尽且知道恢复时间时，迁移到 Depleted 状态，
			// 避免后续请求继续去上游白撞 429/403。Total=0 视为"上游不暴露额度"，不判死。
			if q.Total > 0 && q.Remaining <= 0 && q.ExpiresAt > time.Now().Unix() {
				env.MarkDepleted(time.Unix(q.ExpiresAt, 0), "额度已用尽，等待恢复")
				eventbus.Emit(eventbus.TopicAccount+".state_changed", env.Snapshot())
			} else if q.Remaining > 0 && env.State() == provider.StateDepleted {
				// 充值 / 加量包 / 新计费窗口：额度回来了就解除耗尽，不等旧 resetAt。
				env.MarkReady()
				eventbus.Emit(eventbus.TopicAccount+".state_changed", env.Snapshot())
			}
			refreshed++
		}

		// 账号之间的抖动，同样用裁剪正态分布。
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(jitterDuration(800*time.Millisecond, 2*time.Second)):
		}
	}

	if refreshed > 0 || failed > 0 {
		s.logf("[QUOTA] 额度刷新完成：成功 %d，失败 %d", refreshed, failed)
	}
}

// RefreshQuotasNow 手动触发一次额度刷新（供 API 调用）。
func (s *Scheduler) RefreshQuotasNow() {
	go s.refreshQuotas()
}
