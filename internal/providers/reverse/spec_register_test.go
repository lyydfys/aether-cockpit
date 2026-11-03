package reverse

import (
	"encoding/json"
	"testing"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

// newSpec 造一条声明式渠道 spec。
func newSpec(id string, extra string) *spec.Spec {
	sp := &spec.Spec{
		ProviderID:  id,
		SpecVersion: "test",
		BaseURL:     "https://relay.example/v1",
		Models: map[string]string{
			"relay/auto": "auto",
			"relay/echo": "echo",
		},
		Endpoints: map[string]string{"chat": "/chat/completions"},
	}
	if extra != "" {
		sp.Extra = json.RawMessage(extra)
	}
	return sp
}

// 一条 spec 就能注册出一个可路由渠道，Manifest 的关键字段来自 spec。
func TestRegisterFromSpecCreatesProvider(t *testing.T) {
	sp := newSpec("spec-relay", `{"reverse_profile":{"kind":"reverse","display_name":"My Relay","family":"relay","region_tag":"CN"}}`)

	if err := RegisterFromSpec(sp); err != nil {
		t.Fatalf("RegisterFromSpec: %v", err)
	}
	defer provider.Unregister("spec-relay")

	p, ok := provider.Get("spec-relay")
	if !ok {
		t.Fatal("注册后 provider.Get 应该能拿到")
	}
	m := p.Manifest()
	if m.ID != "spec-relay" || m.DisplayName != "My Relay" {
		t.Fatalf("Manifest = %+v", m)
	}
	if !hasModel(m.Models, "spec-relay/auto") {
		t.Errorf("模型清单缺少 spec-relay/auto：%+v", m.Models)
	}
	if m.RegionTag != "CN" {
		t.Errorf("RegionTag = %q", m.RegionTag)
	}
}

// 同 ID 重复注册必须幂等（spec 热更新会反复触发），并以最新 spec 为准。
func TestRegisterFromSpecIdempotent(t *testing.T) {
	sp := newSpec("spec-relay-2", `{"reverse_profile":{"kind":"reverse","display_name":"V1"}}`)
	if err := RegisterFromSpec(sp); err != nil {
		t.Fatalf("第一次注册: %v", err)
	}
	defer provider.Unregister("spec-relay-2")

	sp2 := newSpec("spec-relay-2", `{"reverse_profile":{"kind":"reverse","display_name":"V2"}}`)
	sp2.BaseURL = "https://relay2.example/v1"
	if err := RegisterFromSpec(sp2); err != nil {
		t.Fatalf("重复注册不该报错: %v", err)
	}
	p, ok := provider.Get("spec-relay-2")
	if !ok {
		t.Fatal("重复注册后 provider 应仍在")
	}
	if got := p.Manifest().DisplayName; got != "V2" {
		t.Fatalf("应以最新 spec 为准，DisplayName = %q", got)
	}
}

// spec 消失后对应渠道必须下线：否则路由表还会把请求派给一个已删除的配置。
func TestReconcileSpecsRemovesGone(t *testing.T) {
	sp := newSpec("spec-relay-3", `{"reverse_profile":{"kind":"reverse"}}`)
	added, _ := ReconcileSpecs([]*spec.Spec{sp})
	if added != 1 {
		t.Fatalf("首次同步应新增 1 个，实际 %d", added)
	}
	if _, ok := provider.Get("spec-relay-3"); !ok {
		t.Fatal("应已注册")
	}

	// 下一轮 spec 列表里没有它了 → 下线。
	// 注意 specRegistered 是包级状态，前面用例可能留下别的条目，
	// 所以这里只断言"确实下线了它"，不断言精确个数。
	_, removed := ReconcileSpecs(nil)
	if removed < 1 {
		t.Fatalf("应至少下线 1 个，实际 %d", removed)
	}
	if _, ok := provider.Get("spec-relay-3"); ok {
		t.Fatal("下线后 provider.Get 应返回 false")
	}
	_ = added
}

// 只写"参数覆盖"的 spec（没有 reverse_profile）不能被当成渠道。
func TestProfileFromSpecRejectsNonReverse(t *testing.T) {
	if _, err := profileFromSpec(newSpec("plain", "")); err == nil {
		t.Fatal("没有 extra 的 spec 不该被当成渠道")
	}
	if _, err := profileFromSpec(newSpec("wrong-kind", `{"reverse_profile":{"kind":"js"}}`)); err == nil {
		t.Fatal("非 reverse 的 kind 必须拒绝")
	}
}

// 不允许 spec 覆盖内置渠道：那是"配置改一下就把 codex 换成别的实现"。
func TestRegisterFromSpecRefusesBuiltinID(t *testing.T) {
	SnapshotBuiltins()
	if _, ok := provider.Get("codex"); !ok {
		t.Skip("本测试环境没有 codex 内置渠道")
	}
	sp := newSpec("codex", `{"reverse_profile":{"kind":"reverse"}}`)
	if err := RegisterFromSpec(sp); err == nil {
		t.Fatal("覆盖内置渠道必须被拒绝")
	}
	// 内置实现不能被替换。
	if p, ok := provider.Get("codex"); !ok || p.Manifest().ID != "codex" {
		t.Fatal("内置 codex 渠道被破坏了")
	}
}

func hasModel(list []ir.ModelMeta, want string) bool {
	for _, m := range list {
		if m.ID == want {
			return true
		}
	}
	return false
}
