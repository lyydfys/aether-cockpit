package pool

import (
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func modeAccounts(t *testing.T) (*Manager, *provider.AccountEnv, *provider.AccountEnv) {
	t.Helper()
	m := New()
	a := provider.NewAccountEnv("a", "p")
	a.Label = "a"
	b := provider.NewAccountEnv("b", "p")
	b.Label = "b"
	m.Add(a)
	m.Add(b)
	return m, a, b
}

// Routing=order：严格按首选/加入顺序取第一个。
func TestSelectOrder(t *testing.T) {
	m, a, _ := modeAccounts(t)
	for i := 0; i < 3; i++ {
		got, err := m.Select("p", SelectOptions{Mode: "order"})
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		if got.AccountID != a.AccountID {
			t.Fatalf("order 应固定第一个，got %s", got.AccountID)
		}
	}
}

// Routing=rotate：逐次轮转。
func TestSelectRotate(t *testing.T) {
	m, a, b := modeAccounts(t)
	seen := map[string]int{}
	for i := 0; i < 4; i++ {
		got, err := m.Select("p", SelectOptions{Mode: "rotate"})
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		seen[got.AccountID]++
	}
	if seen[a.AccountID] != 2 || seen[b.AccountID] != 2 {
		t.Fatalf("rotate 应各 2 次: %v", seen)
	}
}

// Routing=usage：剩余额度多的优先。
func TestSelectUsage(t *testing.T) {
	m, a, b := modeAccounts(t)
	a.SetQuota(&provider.QuotaStatus{Remaining: 10, ReadAt: time.Now().Unix()})
	b.SetQuota(&provider.QuotaStatus{Remaining: 900, ReadAt: time.Now().Unix()})
	got, err := m.Select("p", SelectOptions{Mode: "usage"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.AccountID != b.AccountID {
		t.Fatalf("usage 应选剩余多的 b，got %s", got.AccountID)
	}
}

// Routing=pace：距到期每小时的剩余额度高的优先。
func TestSelectPace(t *testing.T) {
	m, a, b := modeAccounts(t)
	now := time.Now()
	// a：100 额度、1 小时后到期 → 100/h；b：200 额度、10 小时后到期 → 20/h。
	a.SetQuota(&provider.QuotaStatus{Remaining: 100, ExpiresAt: now.Add(time.Hour).Unix(), ReadAt: now.Unix()})
	b.SetQuota(&provider.QuotaStatus{Remaining: 200, ExpiresAt: now.Add(10 * time.Hour).Unix(), ReadAt: now.Unix()})
	got, err := m.Select("p", SelectOptions{Mode: "pace"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.AccountID != a.AccountID {
		t.Fatalf("pace 应选每小时额度多的 a，got %s", got.AccountID)
	}
}

// Routing=fast：首字快的优先；没有样本时不报错。
func TestSelectFast(t *testing.T) {
	m, a, b := modeAccounts(t)
	a.NoteLatency(900, 100, 4000)
	b.NoteLatency(120, 100, 4000)
	got, err := m.Select("p", SelectOptions{Mode: "fast"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.AccountID != b.AccountID {
		t.Fatalf("fast 应选 TTFT 低的 b，got %s", got.AccountID)
	}
}

// Routing=weight：按权重分流（10:1 时大头明显更多）。
func TestSelectWeight(t *testing.T) {
	m, a, _ := modeAccounts(t)
	a.SetWeight(100)
	counts := map[string]int{}
	for i := 0; i < 400; i++ {
		got, err := m.Select("p", SelectOptions{Mode: "weight"})
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		counts[got.AccountID]++
	}
	if counts[a.AccountID] < 300 {
		t.Fatalf("weight 应偏向权重高的 a: %v", counts)
	}
}

// Stays=strict：绑定的账号不可用时返回错误，绝不换号。
func TestSelectAffinityStrict(t *testing.T) {
	m, a, _ := modeAccounts(t)
	if _, err := m.Select("p", SelectOptions{SessionKey: "s1"}); err != nil {
		t.Fatalf("首次 Select: %v", err)
	}
	m.BindSticky("s1", a.AccountID)
	a.MarkCoolingDown(time.Minute, "429")
	_, err := m.Select("p", SelectOptions{SessionKey: "s1", Affinity: "strict"})
	if err == nil {
		t.Fatal("Strict 下绑定账号冷却时应返回错误")
	}
	// 非 strict 时应换到另一个账号。
	got, err2 := m.Select("p", SelectOptions{SessionKey: "s1", Affinity: "auto"})
	if err2 != nil || got.AccountID == a.AccountID {
		t.Fatalf("auto 应换号: %v %v", got, err2)
	}
}

// Stays=off：不认粘性。
func TestSelectAffinityOff(t *testing.T) {
	m, a, b := modeAccounts(t)
	m.BindSticky("s1", a.AccountID)
	// b 的额度明显更高：smart 会选 b；off 时应确实选 b。
	b.SetQuota(&provider.QuotaStatus{Remaining: 1000, ReadAt: time.Now().Unix()})
	got, err := m.Select("p", SelectOptions{SessionKey: "s1", Affinity: "off"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.AccountID != b.AccountID {
		t.Fatalf("off 应忽略粘性选 b，got %s", got.AccountID)
	}
}

// Sink：沉底的账号排到队尾（order 模式下先被别人接）。
func TestSelectSinkGoesLast(t *testing.T) {
	m, a, b := modeAccounts(t)
	m.SetSink(a.AccountID, true)
	got, err := m.Select("p", SelectOptions{Mode: "order"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.AccountID != b.AccountID {
		t.Fatalf("沉底的 a 应排到最后，got %s", got.AccountID)
	}
	// 沉底的账号只有在前面都不可用（或被排除）时才会被选中，选中即解除沉底。
	got2, err := m.Select("p", SelectOptions{Mode: "order", Exclude: map[string]bool{b.AccountID: true}})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got2.AccountID != a.AccountID {
		t.Fatalf("排除 b 后应轮到 a，got %s", got2.AccountID)
	}
	got3, err := m.Select("p", SelectOptions{Mode: "order"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got3.AccountID != a.AccountID {
		t.Fatalf("a 被选中后应解除沉底、回到原位，got %s", got3.AccountID)
	}
}

var _ = ir.ErrCodeNoAccount
