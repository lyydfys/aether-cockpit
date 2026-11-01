package gateway

import (
	"context"
	"encoding/json"
	"log"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

// StartTokenRefresh 启动凭证保活循环，返回停止函数。
//
// 为什么必须有它：逆向渠道的 access_token 往往只有几小时有效期。
// 如果没有自动刷新，用户每天都要重新登录一次；而如果刷新得太晚，
// 第一批请求会先撞 401 才触发兜底，体验和成功率都会变差。
//
// 刷新时机：距离过期不足 lead 时刷新；处于 Invalid 且手里有 refresh_token 时也尝试。
func (g *Gateway) StartTokenRefresh(ctx context.Context, interval, lead time.Duration) func() {
	if interval <= 0 {
		return func() {}
	}
	if lead <= 0 {
		lead = 30 * time.Minute
	}

	stop := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)

		// 启动后先等一会，避开账号恢复与第一批管理请求。
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-time.After(20 * time.Second):
		}

		g.refreshExpiringTokens(ctx, lead)

		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-t.C:
				g.refreshExpiringTokens(ctx, lead)
			}
		}
	}()

	var closed bool
	return func() {
		if closed {
			return
		}
		closed = true
		close(stop)
		<-done
	}
}

// refreshExpiringTokens 串行检查并刷新所有账号。
func (g *Gateway) refreshExpiringTokens(ctx context.Context, lead time.Duration) {
	envs := g.pool.All()
	if len(envs) == 0 {
		return
	}

	now := time.Now()
	refreshed, failed := 0, 0

	for _, env := range envs {
		select {
		case <-ctx.Done():
			return
		default:
		}

		p, ok := provider.Get(env.ProviderID)
		if !ok {
			continue
		}

		cfg := p.Manifest().OAuth
		if sp := spec.Load(env.ProviderID); sp != nil {
			cfg = mergeOAuthFromSpec(cfg, sp)
		}

		_, hasCap := provider.AsRefresh(p)
		refreshField := "refresh_token"
		expiresField := "expires_at"
		if cfg != nil {
			if cfg.RefreshField != "" {
				refreshField = cfg.RefreshField
			}
			if cfg.ExpiresField != "" {
				expiresField = cfg.ExpiresField
			}
		}

		hasRefreshToken := env.GetCredential(refreshField) != ""
		if !hasCap && !(cfg != nil && cfg.TokenURL != "" && hasRefreshToken) {
			// 既没有刷新能力，也没有可用的 refresh_token：不产生无意义请求。
			continue
		}

		expiresAt := parseUnixValue(env.GetCredential(expiresField))
		state, stateMsg, resetAt := env.StateDetail()

		dueToExpiry := expiresAt > 0 && !now.Add(lead).Before(time.Unix(expiresAt, 0))
		dueToInvalid := state == provider.StateInvalid && (hasCap || hasRefreshToken)
		if !dueToExpiry && !dueToInvalid {
			continue
		}

		// 正在被其他刷新占用 / 请求正在进行时跳过。
		if state == provider.StateRefreshing {
			continue
		}

		env.MarkRefreshing()
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		draft, err := g.RefreshAccount(rctx, env, p)
		cancel()

		if err != nil {
			failed++
			if state == provider.StateInvalid {
				env.MarkInvalid(err.Error())
			} else {
				// 临时失败不应把冷却/耗尽状态冲成 Ready。
				env.RestoreState(state, stateMsg, resetAt)
			}
			log.Printf("[TOKEN] %s/%s 刷新失败: %v", env.ProviderID, env.AccountID, err)
			eventbus.Emit(eventbus.TopicAccount+".refresh_failed", map[string]any{
				"provider_id": env.ProviderID,
				"account_id":  env.AccountID,
				"error":       err.Error(),
			})
		} else {
			refreshed++
			var expiresAt int64
			if draft != nil {
				expiresAt = draft.ExpiresAt
			}
			eventbus.Emit(eventbus.TopicAccount+".refreshed", map[string]any{
				"provider_id": env.ProviderID,
				"account_id":  env.AccountID,
				"automatic":   true,
				"expires_at":  expiresAt,
			})
		}

		// 账号之间插入抖动，避免多个账号同一秒刷新形成固定节奏。
		if !sleepJitter(ctx, 800*time.Millisecond, 2*time.Second) {
			return
		}
	}
	if refreshed > 0 {
		g.saveAccountsNow()
	}

	if refreshed > 0 || failed > 0 {
		log.Printf("[TOKEN] 本轮刷新完成：成功 %d，失败 %d", refreshed, failed)
	}
}

// parseUnixValue 兼容 Unix 秒 / 毫秒、数字字符串与 RFC3339。
func parseUnixValue(raw string) int64 {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n > 1e12 {
			return n / 1000
		}
		return n
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix()
	}
	var num json.Number
	if err := json.Unmarshal([]byte(s), &num); err == nil {
		if n, err := num.Int64(); err == nil {
			if n > 1e12 {
				return n / 1000
			}
			return n
		}
	}
	return 0
}

func sleepJitter(ctx context.Context, lo, hi time.Duration) bool {
	if hi <= lo {
		hi = lo + time.Millisecond
	}
	d := lo + time.Duration(rand.Int63n(int64(hi-lo)))
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
