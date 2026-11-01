package pool

import (
	"sync"
	"testing"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// officialStubProvider 同时声明 OfficialFirst 并区分官方/中转凭证。
type officialStubProvider struct{}

func (officialStubProvider) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID:          "pool-official-stub",
		DisplayName: "Official Stub",
		Models: []ir.ModelMeta{
			{ID: "pool-official-stub/m", UpstreamModel: "m"},
		},
		Capabilities: provider.CapabilitySet{Chat: true, OfficialFirst: true},
	}
}

func (officialStubProvider) IsOfficialAccount(creds map[string]string) bool {
	return creds["official"] == "1"
}

var officialStubOnce sync.Once

func ensureOfficialStub() {
	officialStubOnce.Do(func() { provider.Register(officialStubProvider{}) })
}

func TestSelectPrefersOfficialAccount(t *testing.T) {
	ensureOfficialStub()
	m := New()
	relay := newEnv("relay", "pool-official-stub")
	relay.Credentials = map[string]string{"official": "0"}
	official := newEnv("official", "pool-official-stub")
	official.Credentials = map[string]string{"official": "1"}
	m.Add(relay)
	m.Add(official)

	for i := 0; i < 20; i++ {
		got, err := m.Select("pool-official-stub", SelectOptions{})
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		if got.AccountID != "official" {
			t.Fatalf("第 %d 次选到 %s，应优先官方账号", i, got.AccountID)
		}
	}

	// 官方账号不可用 → 落到中转。
	official.MarkInvalid("额度用尽")
	got, err := m.Select("pool-official-stub", SelectOptions{})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.AccountID != "relay" {
		t.Fatalf("官方不可用时应落到中转，实际 %s", got.AccountID)
	}

	// 官方账号被 Exclude（本次重试范围内）→ 落到中转。
	official.MarkSuccess()
	got, err = m.Select("pool-official-stub", SelectOptions{Exclude: map[string]bool{"official": true}})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.AccountID != "relay" {
		t.Fatalf("排除官方后应选中转，实际 %s", got.AccountID)
	}
}

func TestSelectExplicitPriorityBeatsOfficial(t *testing.T) {
	ensureOfficialStub()
	m := New()
	relay := newEnv("relay", "pool-official-stub")
	relay.Credentials = map[string]string{"official": "0"}
	relay.SetPriority(1) // 用户显式首选
	official := newEnv("official", "pool-official-stub")
	official.Credentials = map[string]string{"official": "1"}
	official.SetPriority(2)
	m.Add(relay)
	m.Add(official)

	got, err := m.Select("pool-official-stub", SelectOptions{})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.AccountID != "relay" {
		t.Fatalf("显式 priority 应优先，实际 %s", got.AccountID)
	}
}
