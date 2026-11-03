package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestProviderExtraModelsAllModes(t *testing.T) {
	g := catalogGateway(t, t.TempDir())
	g.Pool().Add(provider.NewAccountEnv("acc-extra", "mock-native"))
	if err := g.addProviderExtraModel("mock-native", "deepseek-v4.1-flash"); err != nil {
		t.Fatal(err)
	}

	// merge：手动模型与静态目录同时存在。
	models := g.catalogModels("mock-native")
	if !hasModelID(models, "mock-native/deepseek-v4.1-flash") || !hasModelID(models, "mock-native/echo-pro") {
		t.Fatalf("merge 目录 = %+v", models)
	}
	if len(g.models.Resolve("deepseek-v4.1-flash")) == 0 {
		t.Fatal("手动模型应可路由")
	}
	// 家族兜底不能把 V4 Flash 的请求偷换成 V4.1 手动模型。
	for _, b := range g.models.Resolve("deepseek-v4-flash") {
		if b.ProviderID == "mock-native" {
			t.Fatalf("V4 Flash 不应命中 V4.1 手动模型: %+v", b)
		}
	}

	// allowlist：手动模型仍然保留。
	if err := g.setProviderModelMode("mock-native", ModelModeAllowlist); err != nil {
		t.Fatal(err)
	}
	g.models.Rebuild()
	models = g.catalogModels("mock-native")
	if !hasModelID(models, "mock-native/deepseek-v4.1-flash") {
		t.Fatalf("allowlist 目录 = %+v", models)
	}
	if len(g.models.Resolve("deepseek-v4.1-flash")) == 0 {
		t.Fatal("allowlist 下手动模型应可路由")
	}

	// fallback：实时目录存在时，手动模型仍追加。
	seedProviderLive(t, g, "mock-native", "echo-live-only")
	if err := g.setProviderModelMode("mock-native", ModelModeFallback); err != nil {
		t.Fatal(err)
	}
	g.models.Rebuild()
	models = g.catalogModels("mock-native")
	if !hasModelID(models, "mock-native/deepseek-v4.1-flash") || !hasModelID(models, "mock-native/echo-live-only") {
		t.Fatalf("fallback 目录 = %+v", models)
	}
	if hasModelID(models, "mock-native/echo-pro") {
		t.Fatalf("fallback 不应包含未出现在实时目录的静态模型: %+v", models)
	}

	if ids := servedModelIDs(t, g); !ids["mock-native/deepseek-v4.1-flash"] {
		t.Fatalf("/v1/models 缺少手动模型: %v", ids)
	}

	if err := g.removeProviderExtraModel("mock-native", "deepseek-v4.1-flash"); err != nil {
		t.Fatal(err)
	}
	g.models.Rebuild()
	models = g.catalogModels("mock-native")
	if hasModelID(models, "mock-native/deepseek-v4.1-flash") {
		t.Fatalf("移除后目录仍在: %+v", models)
	}
	for _, b := range g.models.Resolve("deepseek-v4.1-flash") {
		if b.ProviderID == "mock-native" {
			t.Fatalf("移除后仍可路由: %+v", b)
		}
	}
}

func TestProviderExtraModelUpdatesSelectionAndView(t *testing.T) {
	g := catalogGateway(t, t.TempDir())
	if err := g.saveProviderModelSelection("mock-native", []string{"echo-pro"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := g.addProviderExtraModel("mock-native", "deepseek-v4.1-flash"); err != nil {
		t.Fatal(err)
	}

	g.settingsMu.Lock()
	sel := append([]string(nil), g.settings.ProviderModels["mock-native"]...)
	g.settingsMu.Unlock()
	if !modelInList(sel, "deepseek-v4.1-flash") {
		t.Fatalf("手动模型未加入渠道选择: %v", sel)
	}

	view := providerViewOf(t, g, "mock-native")
	rows, _ := view["models"].([]any)
	found := false
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["id"] == "deepseek-v4.1-flash" {
			found = true
			if row["manual"] != true || row["on"] != true {
				t.Fatalf("手动模型视图 = %+v", row)
			}
		}
	}
	if !found {
		t.Fatalf("providers 视图缺少手动模型: %+v", rows)
	}

	if err := g.removeProviderExtraModel("mock-native", "deepseek-v4.1-flash"); err != nil {
		t.Fatal(err)
	}
	g.settingsMu.Lock()
	sel = append([]string(nil), g.settings.ProviderModels["mock-native"]...)
	g.settingsMu.Unlock()
	if modelInList(sel, "deepseek-v4.1-flash") {
		t.Fatalf("移除后渠道选择仍有该模型: %v", sel)
	}
}

func TestProviderExtraModelAPI(t *testing.T) {
	g := catalogGateway(t, t.TempDir())
	g.Pool().Add(provider.NewAccountEnv("acc-extra-api", "mock-native"))

	post := func(body string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/providers/mock-native/models/extra", strings.NewReader(body))
		req.SetPathValue("id", "mock-native")
		g.handleProviderExtraModel(rec, req)
		return rec.Code
	}
	if code := post(`{"model":"deepseek-v4.1-flash","action":"add"}`); code != http.StatusOK {
		t.Fatalf("add status = %d", code)
	}
	if !hasModelID(g.catalogModels("mock-native"), "mock-native/deepseek-v4.1-flash") {
		t.Fatal("API 添加后目录未生效")
	}
	if code := post(`{"model":"deepseek-v4.1-flash","action":"remove"}`); code != http.StatusOK {
		t.Fatalf("remove status = %d", code)
	}
	if hasModelID(g.catalogModels("mock-native"), "mock-native/deepseek-v4.1-flash") {
		t.Fatal("API 移除后目录仍有该模型")
	}
	if code := post(`{"model":"","action":"add"}`); code != http.StatusBadRequest {
		t.Fatalf("空模型名应 400，实际 %d", code)
	}
}
