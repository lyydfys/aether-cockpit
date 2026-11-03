package gateway

import (
	"testing"

	"aether-cockpit/internal/core/ir"
)

// DeepSeek V4 Flash 与 V4.1 Flash 是两个模型：归一化、目录合并、
// canonical 别名和路由都不能把它们自动串成一条。
func TestDeepSeekFlashVersionsStayDistinct(t *testing.T) {
	if normalizeModelKey("deepseek-v4-flash") == normalizeModelKey("deepseek-v4.1-flash") {
		t.Fatal("V4 Flash 与 V4.1 Flash 的归一化键不能相同")
	}

	got := mergeCatalogModels(
		[]ir.ModelMeta{{ID: "p/deepseek-v4.1-flash", UpstreamModel: "deepseek-v4.1-flash"}},
		[]ir.ModelMeta{{ID: "p/deepseek-v4-flash", UpstreamModel: "deepseek-v4-flash"}},
	)
	if len(got) != 2 {
		t.Fatalf("合并后应保留两个模型: %+v", got)
	}
}

// models.dev 个别渠道把旧 V4 Flash 标成 V4.1 的 legacy alias；
// 路由别名不能因此把 V4.1 渠道塞进 V4 Flash 的候选链。
func TestAliasesForGuardsDeepSeekFlashVersions(t *testing.T) {
	g := catalogGateway(t, t.TempDir())
	g.catalog.metaMu.Lock()
	g.catalog.meta[normalizeModelKey("deepseek-v4-flash")] = metaEntry{
		Name:      "DeepSeek V4 Flash",
		Canonical: "deepseek/deepseek-v4.1-flash",
	}
	g.catalog.meta[normalizeModelKey("deepseek-v4.1-flash")] = metaEntry{
		Name:      "DeepSeek V4.1 Flash",
		Canonical: "deepseek/deepseek-v4.1-flash",
	}
	g.catalog.metaMu.Unlock()

	wantV41 := normalizeModelKey("deepseek-v4.1-flash")
	for _, alias := range g.catalog.aliasesFor("qoder-cn", "deepseek-v4-flash") {
		if alias == wantV41 {
			t.Fatalf("V4 Flash 不应获得 V4.1 的 canonical 别名: %v", g.catalog.aliasesFor("qoder-cn", "deepseek-v4-flash"))
		}
	}
	found := false
	for _, alias := range g.catalog.aliasesFor("qoder-cn", "deepseek-v4.1-flash") {
		if alias == wantV41 {
			found = true
		}
	}
	if !found {
		t.Fatal("V4.1 Flash 应保留自己的 canonical 别名")
	}
}

// 家族兜底不能把带版本号的请求接到完全不标版本的泛槽位上。
func TestFamilyFallbackSkipsUnversionedCandidates(t *testing.T) {
	g := catalogGateway(t, t.TempDir())
	seedProviderLive(t, g, "mock-native", "deepseek-flash")
	g.models.Rebuild()

	if len(g.models.Resolve("deepseek-flash")) == 0 {
		t.Fatal("显式 deepseek-flash 应可路由")
	}
	for _, b := range g.models.Resolve("deepseek-v4-flash") {
		if b.ProviderID == "mock-native" {
			t.Fatalf("V4 Flash 不应通过家族兜底落到无版本泛槽位: %+v", b)
		}
	}
}

func TestResolveDoesNotCrossDeepSeekFlashVersions(t *testing.T) {
	g := catalogGateway(t, t.TempDir())
	seedProviderLive(t, g, "mock-native", "deepseek-v4-flash")
	seedProviderLive(t, g, "mock-weak", "deepseek-v4.1-flash")
	g.catalog.metaMu.Lock()
	g.catalog.meta[normalizeModelKey("deepseek-v4-flash")] = metaEntry{
		Name:      "DeepSeek V4 Flash",
		Canonical: "deepseek/deepseek-v4.1-flash",
	}
	g.catalog.metaMu.Unlock()
	g.models.Rebuild()

	for _, b := range g.models.Resolve("deepseek-v4-flash") {
		if b.ProviderID == "mock-weak" {
			t.Fatalf("V4 Flash 请求不应落到只提供 V4.1 的渠道: %+v", b)
		}
	}
	for _, b := range g.models.Resolve("deepseek-v4.1-flash") {
		if b.ProviderID == "mock-native" {
			t.Fatalf("V4.1 请求不应落到只提供 V4 Flash 的渠道: %+v", b)
		}
	}
}
