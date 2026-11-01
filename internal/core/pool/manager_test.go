package pool

import (
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func newEnv(id, providerID string) *provider.AccountEnv {
	env := provider.NewAccountEnv(id, providerID)
	env.Fingerprint = provider.DeviceFingerprint{DeviceID: "dev-" + id}
	return env
}

func TestSelectSkipsUnavailable(t *testing.T) {
	m := New()
	good := newEnv("good", "p")
	bad := newEnv("bad", "p")
	bad.MarkInvalid("expired")
	m.Add(good)
	m.Add(bad)

	got, err := m.Select("p", SelectOptions{})
	if err != nil {
		t.Fatalf("选择失败: %v", err)
	}
	if got.AccountID != "good" {
		t.Errorf("选到了不可用账号: %s", got.AccountID)
	}
}

func TestSelectNoAccount(t *testing.T) {
	m := New()
	if _, err := m.Select("p", SelectOptions{}); err == nil {
		t.Fatal("空池应返回错误")
	}
}

func TestPinnedAccountWins(t *testing.T) {
	m := New()
	a := newEnv("a", "p")
	b := newEnv("b", "p")
	// 让 b 的额度更高，验证 pinned 仍然优先。
	b.SetQuota(&provider.QuotaStatus{Remaining: 10000})
	m.Add(a)
	m.Add(b)

	got, err := m.Select("p", SelectOptions{PinnedAccount: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountID != "a" {
		t.Errorf("pinned 未生效，选中 %s", got.AccountID)
	}
}

func TestPinnedFallbackWhenUnavailable(t *testing.T) {
	m := New()
	a := newEnv("a", "p")
	b := newEnv("b", "p")
	b.SetQuota(&provider.QuotaStatus{Remaining: 100})
	m.Add(a)
	m.Add(b)

	// 被锁定的账号失效时，应降级到其他可用账号（跨账号故障转移）。
	a.MarkInvalid("token revoked")

	got, err := m.Select("p", SelectOptions{PinnedAccount: "a"})
	if err != nil {
		t.Fatalf("应降级而不是报错: %v", err)
	}
	if got.AccountID != "b" {
		t.Errorf("应降级到 b，实际 %s", got.AccountID)
	}
}

func TestStickyBinding(t *testing.T) {
	m := New()
	a := newEnv("a", "p")
	b := newEnv("b", "p")
	a.SetQuota(&provider.QuotaStatus{Remaining: 100})
	b.SetQuota(&provider.QuotaStatus{Remaining: 9999})
	m.Add(a)
	m.Add(b)

	// 没有粘性时会选额度高的 b。
	got, _ := m.Select("p", SelectOptions{SessionKey: "s1"})
	if got.AccountID != "b" {
		t.Fatalf("初始应选 b，实际 %s", got.AccountID)
	}

	m.BindSticky("s1", "a")
	got, _ = m.Select("p", SelectOptions{SessionKey: "s1"})
	if got.AccountID != "a" {
		t.Errorf("粘性未生效，选中 %s", got.AccountID)
	}
}

func TestExcludeOnRetry(t *testing.T) {
	m := New()
	a := newEnv("a", "p")
	b := newEnv("b", "p")
	m.Add(a)
	m.Add(b)

	got, err := m.Select("p", SelectOptions{Exclude: map[string]bool{"a": true}})
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountID != "b" {
		t.Errorf("排除未生效，选中 %s", got.AccountID)
	}
}

func TestWeightedPrefersExpiringQuota(t *testing.T) {
	now := time.Now()
	fresh := newEnv("fresh", "p")
	fresh.SetQuota(&provider.QuotaStatus{Remaining: 1000, ExpiresAt: now.Add(72 * time.Hour).Unix()})

	expiring := newEnv("expiring", "p")
	expiring.SetQuota(&provider.QuotaStatus{Remaining: 1000, ExpiresAt: now.Add(2 * time.Hour).Unix()})

	freshScore := weightedScore(fresh, now)
	expiringScore := weightedScore(expiring, now)

	if expiringScore <= freshScore {
		t.Errorf("临期额度权重应更高: expiring=%.2f fresh=%.2f", expiringScore, freshScore)
	}
}

func TestWeightedPenalizesInflight(t *testing.T) {
	now := time.Now()
	idle := newEnv("idle", "p")
	idle.SetQuota(&provider.QuotaStatus{Remaining: 1000})

	busy := newEnv("busy", "p")
	busy.SetQuota(&provider.QuotaStatus{Remaining: 1000})
	for i := 0; i < 3; i++ {
		busy.Acquire()
	}

	if weightedScore(busy, now) >= weightedScore(idle, now) {
		t.Error("并发高的账号权重应更低")
	}
}

func TestCooldownAutoRecovery(t *testing.T) {
	env := newEnv("a", "p")
	env.MarkCoolingDown(50*time.Millisecond, "429")
	if env.Available() {
		t.Fatal("冷却期内不应可用")
	}
	time.Sleep(60 * time.Millisecond)
	if !env.Available() {
		t.Error("冷却到期应自动恢复")
	}
}

func TestSessionFingerprintDiffers(t *testing.T) {
	r1 := &ir.UnifiedChatRequest{Items: []ir.UnifiedItem{
		{Type: ir.ItemTypeMessage, Role: "user", Text: "问题一"},
	}}
	r2 := &ir.UnifiedChatRequest{Items: []ir.UnifiedItem{
		{Type: ir.ItemTypeMessage, Role: "user", Text: "问题二"},
	}}
	if SessionFingerprint("p", r1) == SessionFingerprint("p", r2) {
		t.Error("不同问题应产生不同指纹")
	}
	if SessionFingerprint("p", r1) != SessionFingerprint("p", r1) {
		t.Error("同一请求应产生稳定指纹")
	}
}

// 缓存价值低的会话，亲和 TTL 应缩短到 5 分钟；缓存量大则保持 15 分钟。
func TestNoteStickyCacheShortensColdAffinity(t *testing.T) {
	m := New()
	m.BindSticky("cold", "a1")
	m.NoteStickyCache("cold", affinityMinCache-1)
	cold := m.sticky["cold"]
	if cold.expiresAt.After(cold.boundAt.Add(stickyTTLCold + time.Second)) {
		t.Fatalf("低缓存会话 TTL 未缩短: %v", cold.expiresAt.Sub(cold.boundAt))
	}

	m.BindSticky("warm", "a2")
	m.NoteStickyCache("warm", 50_000)
	warm := m.sticky["warm"]
	if warm.expiresAt.Sub(warm.boundAt) < stickyTTLCold+time.Minute {
		t.Fatalf("高缓存会话 TTL 被错误缩短: %v", warm.expiresAt.Sub(warm.boundAt))
	}
	if warm.cacheTokens != 50_000 {
		t.Fatalf("缓存量未记录: %d", warm.cacheTokens)
	}
}

// 客户端自报会话 ID 时，指纹优先用它：同一会话即使文本变了也保持稳定。
func TestSessionFingerprintPrefersClientSession(t *testing.T) {
	mk := func(sid, text string) *ir.UnifiedChatRequest {
		r := &ir.UnifiedChatRequest{Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: text}}}
		if sid != "" {
			r.Meta = map[string]any{"client_session": sid}
		}
		return r
	}
	a := SessionFingerprint("p", mk("sess-1", "第一条"))
	b := SessionFingerprint("p", mk("sess-1", "完全不同的第二条"))
	if a != b {
		t.Fatal("同一 client_session 的指纹应稳定")
	}
	c := SessionFingerprint("p", mk("sess-2", "第一条"))
	if a == c {
		t.Fatal("不同 client_session 不应碰撞")
	}
	// 没有 client_session 时退回文本指纹。
	if SessionFingerprint("p", mk("", "x")) == SessionFingerprint("p", mk("", "y")) {
		t.Fatal("无会话 ID 时不同文本应有不同指纹")
	}
}

// 第三级：没有自报会话 ID、也没有文本时，用来源指纹兜底，保证同一客户端仍粘同一账号。
func TestSessionFingerprintClientFallback(t *testing.T) {
	r := &ir.UnifiedChatRequest{Meta: map[string]any{"client_fp": "abc123"}}
	if got := SessionFingerprint("p", r); got == "" {
		t.Fatal("来源指纹兜底未生效")
	}
	a := SessionFingerprint("p", &ir.UnifiedChatRequest{Meta: map[string]any{"client_fp": "abc123"}})
	b := SessionFingerprint("p", &ir.UnifiedChatRequest{Meta: map[string]any{"client_fp": "zzz999"}})
	if a == b {
		t.Fatal("不同来源指纹不应碰撞")
	}
}

func TestEarliestReset(t *testing.T) {
	m := New()
	a := provider.NewAccountEnv("a", "p")
	b := provider.NewAccountEnv("b", "p")
	a.MarkCoolingDown(2*time.Hour, "429")
	b.MarkDepleted(time.Now().Add(time.Hour), "quota")
	m.Add(a)
	m.Add(b)
	d := m.EarliestReset("p")
	if d < 59*time.Minute || d > time.Hour+time.Minute {
		t.Fatalf("EarliestReset = %v, want ≈1h", d)
	}
	if m.EarliestReset("nope") != 0 {
		t.Fatal("未知渠道应为 0")
	}
	ready := provider.NewAccountEnv("c", "q")
	m.Add(ready)
	if m.EarliestReset("q") != 0 {
		t.Fatal("就绪账号不应报冷却")
	}
}
