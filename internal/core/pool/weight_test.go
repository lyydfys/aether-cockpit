package pool

import (
	"os"
	"path/filepath"
	"testing"

	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/mock"
)

func TestMain(m *testing.M) {
	mock.Register()
	os.Exit(m.Run())
}

func TestWeightedSelectionSplitsTraffic(t *testing.T) {
	m := New()
	heavy := newEnv("heavy", "p")
	light := newEnv("light", "p")
	heavy.SetWeight(9)
	light.SetWeight(1)
	m.Add(heavy)
	m.Add(light)

	const rounds = 400
	counts := map[string]int{}
	for i := 0; i < rounds; i++ {
		env, err := m.Select("p", SelectOptions{})
		if err != nil {
			t.Fatalf("选择失败: %v", err)
		}
		counts[env.AccountID]++
	}
	if counts["heavy"] < rounds*7/10 {
		t.Fatalf("权重 9 的账号分到太少: %v", counts)
	}
	if counts["light"] == 0 {
		t.Fatalf("权重 1 的账号一次都没分到: %v", counts)
	}
}

func TestDefaultWeightsStayDeterministic(t *testing.T) {
	m := New()
	a := newEnv("a", "p")
	b := newEnv("b", "p")
	a.SetQuota(&provider.QuotaStatus{Remaining: 100})
	b.SetQuota(&provider.QuotaStatus{Remaining: 50})
	m.Add(a)
	m.Add(b)

	first, _ := m.Select("p", SelectOptions{})
	for i := 0; i < 50; i++ {
		env, _ := m.Select("p", SelectOptions{})
		if env.AccountID != first.AccountID {
			t.Fatalf("默认权重下不应随机分流: %s vs %s", env.AccountID, first.AccountID)
		}
	}
	if first.AccountID != "a" {
		t.Fatalf("额度高者应先选: %s", first.AccountID)
	}
}

func TestWeightPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	m := New()
	m.SetStorePath(path, nil)
	env := newEnv("acc", "mock-native")
	env.SetWeight(7)
	m.Add(env)
	if err := m.SaveAccounts(); err != nil {
		t.Fatal(err)
	}

	m2 := New()
	if err := m2.SetStorePath(path, nil); err != nil {
		t.Fatal(err)
	}
	got, ok := m2.Get("acc")
	if !ok {
		t.Fatal("账号未恢复")
	}
	if got.Weight() != 7 {
		t.Fatalf("权重未恢复: %d", got.Weight())
	}
	if s := got.Snapshot(); s.Weight != 7 {
		t.Fatalf("快照 weight = %d", s.Weight)
	}
}
