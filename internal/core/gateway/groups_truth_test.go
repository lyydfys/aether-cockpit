package gateway

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
	_ "aether-cockpit/internal/providers/mock"
)

// TestGroupRoutingUnsupportedIsExplicit 锁住路由组配置的“不静默降级”：
// weight/free/levels/match/fast/off/affinity 未实现时，保存后必须被移除或
// 归一化，并在 /api/groups 里给出 warning，而不是伪装成 priority。
// rules 已接入组规则引擎，因此必须原样持久化，不再出现在 warning 里。
func TestGroupRoutingUnsupportedIsExplicit(t *testing.T) {
	g := New(Config{
		MaxAttempts:             1,
		FirstByteTimeout:        5 * time.Second,
		MaxConcurrentPerAccount: -1,
		SettingsPath:            filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath:        filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:               "-",
		MaxRecords:              20,
	})
	env := provider.NewAccountEnv("mock-native-group-test", "mock-native")
	g.Pool().Add(env)
	g.models.Rebuild()

	def := routingGroupDef{
		ID:       "g-weight",
		Name:     "g-weight",
		Members:  []string{"mock-native/echo-pro"},
		Chain:    []string{"mock-native"},
		Routing:  "weight",
		Affinity: "session",
		Weights:  map[string]int{"mock-native": 5},
		Levels:   []string{"high"},
		Match:    []string{"intent"},
		Rules:    []GroupRule{{Use: []string{"mock-native/echo-pro"}, Effort: "high"}},
		Fast:     []string{"mock-native"},
		Off:      []string{"mock-native"},
	}
	if err := g.persistGroup(def, ""); err != nil {
		t.Fatalf("保存路由组: %v", err)
	}

	raw := g.settings.ModelGroupsOverride["g-weight"]
	if len(raw) == 0 {
		t.Fatal("路由组没有落到 settings")
	}
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("解析已保存的路由组: %v", err)
	}
	if got := stored["routing"]; got != "order" {
		t.Fatalf("未实现的 weight 应归一化为 order，得到 %v", got)
	}
	if got := stored["strategy"]; got != GroupStrategyPriority {
		t.Fatalf("strategy 应为 priority，得到 %v", got)
	}
	for _, key := range []string{"weights", "levels", "match", "fast", "off", "affinity"} {
		if _, ok := stored[key]; ok {
			t.Fatalf("未实现的字段 %q 不应再写回配置", key)
		}
	}
	// rules 已经接入内核：必须持久化，否则"保存成功但不生效"。
	if _, ok := stored["rules"]; !ok {
		t.Fatal("已接入的 rules 必须写回配置")
	}

	warns := routingWarnings(def)
	if len(warns) == 0 {
		t.Fatal("保存前必须为未实现的路由配置生成 warning，不能静默")
	}
	for _, w := range warns {
		if strings.Contains(w, "rules") {
			t.Fatalf("rules 已接入，不应再报未接入: %q", w)
		}
	}

	// 旧配置（升级前保存的 weight 字段）仍要能在页面上看到提示。
	legacy := map[string]any{
		"chain": []string{"mock-native"}, "strategy": GroupStrategyPriority,
		"name": "g-legacy", "members": []string{"mock-native/echo-pro"},
		"routing": "weight", "weights": map[string]int{"mock-native": 5},
	}
	rawLegacy, _ := json.Marshal(legacy)
	g.settings.ModelGroupsOverride["g-legacy"] = rawLegacy
	g.rebuildRoutingGroups()

	view := g.buildGroups()
	list, ok := view["groups"].([]map[string]any)
	if !ok {
		t.Fatalf("groups 类型异常: %T", view["groups"])
	}
	var found map[string]any
	for _, gm := range list {
		if gm["id"] == "g-legacy" {
			found = gm
			break
		}
	}
	if found == nil {
		t.Fatal("buildGroups 里找不到 g-legacy")
	}
	if got := found["routingEffective"]; got != GroupStrategyPriority {
		t.Fatalf("routingEffective 应为 priority，得到 %v", got)
	}
	if got := found["routing"]; got != "order" {
		t.Fatalf("未实现的 routing 不应回显，期望 order，得到 %v", got)
	}
	warnings, _ := found["warnings"].([]string)
	if len(warnings) == 0 {
		t.Fatal("旧配置里的未实现路由必须返回 warning，不能静默")
	}
	for _, key := range []string{"weights", "levels", "match"} {
		if _, ok := found[key]; ok {
			t.Fatalf("groups 响应不应再暴露未实现字段 %q", key)
		}
	}
}
