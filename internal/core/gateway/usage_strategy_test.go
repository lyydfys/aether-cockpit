package gateway

import (
	"testing"
	"time"
)

// usage 与 pace 是两种语义，不能互相替代：
//   - usage：剩余额度多的排前面（先花大号，保住小号做兜底）；
//   - pace ：剩余 / 距到期小时数大的排前面（先花临期额度）。
//
// 旧实现把组级 usage 直接折成 pace，这里锁住两者会产生不同顺序。
func TestUsageAndPaceOrderDifferently(t *testing.T) {
	now := time.Now().Unix()
	quota := func(provider string) (int, int64, bool) {
		switch provider {
		case "big-slow":
			// 剩余多，但 20 小时后才重置：pace 下不急。
			return 1000, now + 20*3600, true
		case "small-urgent":
			// 剩余少，但 10 分钟后就清零：pace 下最急。
			return 100, now + 600, true
		}
		return 0, 0, false
	}
	seats := []modelBinding{{ProviderID: "big-slow"}, {ProviderID: "small-urgent"}}
	ctx := &RouteContext{Quota: quota}

	usage := strategyUsage(ctx, seats)
	if usage[0].ProviderID != "big-slow" {
		t.Errorf("usage 应把剩余最多的排前面，got %s", usage[0].ProviderID)
	}
	pace := strategyPace(ctx, seats)
	if pace[0].ProviderID != "small-urgent" {
		t.Errorf("pace 应把临期额度排前面，got %s", pace[0].ProviderID)
	}
	if usage[0].ProviderID == pace[0].ProviderID {
		t.Fatal("两种策略给出了相同顺序，说明 usage 又被折成 pace 了")
	}
	// 都是输入的一个排列（不可能凭空造/丢候选）。
	if len(usage) != len(seats) || len(pace) != len(seats) {
		t.Fatalf("策略改变了候选数量：usage=%d pace=%d want=%d", len(usage), len(pace), len(seats))
	}
}

// 没有额度数据时保持原序（fail-open），不能因为查不到就把顺序打乱。
func TestUsageKeepsOrderWithoutQuota(t *testing.T) {
	seats := []modelBinding{{ProviderID: "a"}, {ProviderID: "b"}}
	out := strategyUsage(&RouteContext{}, seats)
	if out[0].ProviderID != "a" || out[1].ProviderID != "b" {
		t.Fatalf("无额度数据时应保持原序，got %v", out)
	}
}

// 组级 routing 映射：usage / pace 必须映射到不同策略。
func TestGroupStrategyMapsUsageAndPaceApart(t *testing.T) {
	u, uOK := groupStrategySupport("usage")
	p, pOK := groupStrategySupport("pace")
	if !uOK || !pOK {
		t.Fatalf("usage/pace 都应被支持：usage=%v pace=%v", uOK, pOK)
	}
	if u == p {
		t.Fatalf("usage 与 pace 映射到了同一个策略 %q", u)
	}
	if u != GroupStrategyUsage {
		t.Errorf("usage 应映射到 %q，got %q", GroupStrategyUsage, u)
	}
}
