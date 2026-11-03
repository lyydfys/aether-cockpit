package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func seedProviderLive(t *testing.T, g *Gateway, pid string, ids ...string) {
	t.Helper()
	g.catalog.mu.Lock()
	g.catalog.live[pid] = liveModelFile{
		ProviderID: pid,
		Fetched:    time.Now().Unix(),
		Models:     ids,
	}
	g.catalog.mu.Unlock()
	g.models.Rebuild()
}

func hasModelID(models []ir.ModelMeta, id string) bool {
	for _, m := range models {
		if m.ID == id {
			return true
		}
	}
	return false
}

// servedModelIDs 调用真正的 /v1/models，验证下游看到的清单也跟随目录模式。
func servedModelIDs(t *testing.T, g *Gateway) map[string]bool {
	t.Helper()
	rec := httptest.NewRecorder()
	g.handleModels(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/v1/models status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /v1/models: %v", err)
	}
	ids := make(map[string]bool, len(body.Data))
	for _, d := range body.Data {
		ids[d.ID] = true
	}
	return ids
}

// 三种目录模式必须真正改变「有效目录」，而且路由与 KnownModels 同步生效。
func TestModelCatalogThreeModes(t *testing.T) {
	g := catalogGateway(t, t.TempDir())
	g.Pool().Add(provider.NewAccountEnv("acc-mode-models", "mock-native"))
	seedProviderLive(t, g, "mock-native", "echo-live-only")

	// 默认 merge：实时 + 静态补缺。
	models := g.catalogModels("mock-native")
	if !hasModelID(models, "mock-native/echo-pro") || !hasModelID(models, "mock-native/echo-live-only") {
		t.Fatalf("merge 目录 = %+v", models)
	}
	if len(g.models.Resolve("echo-pro")) == 0 || len(g.models.Resolve("echo-live-only")) == 0 {
		t.Fatal("merge 模式下静态与实时模型都应可路由")
	}
	if source, _ := g.catalog.source("mock-native"); source != "live" {
		t.Fatalf("merge source = %q", source)
	}
	if ids := servedModelIDs(t, g); !ids["mock-native/echo-pro"] || !ids["mock-native/echo-live-only"] {
		t.Fatalf("merge /v1/models = %v", ids)
	}

	// fallback：实时目录存在时以它为准，静态不再参与路由。
	if err := g.setProviderModelMode("mock-native", ModelModeFallback); err != nil {
		t.Fatal(err)
	}
	g.models.Rebuild()
	models = g.catalogModels("mock-native")
	if len(models) != 1 || models[0].ID != "mock-native/echo-live-only" {
		t.Fatalf("fallback 目录 = %+v", models)
	}
	if len(g.models.Resolve("echo-pro")) != 0 {
		t.Fatal("fallback 模式下实时目录未包含的静态模型不应可路由")
	}
	if len(g.models.Resolve("echo-live-only")) == 0 {
		t.Fatal("fallback 模式下实时模型应可路由")
	}
	if ids := servedModelIDs(t, g); ids["mock-native/echo-pro"] || !ids["mock-native/echo-live-only"] {
		t.Fatalf("fallback /v1/models = %v", ids)
	}

	// allowlist：只用静态目录，实时缓存被忽略。
	if err := g.setProviderModelMode("mock-native", ModelModeAllowlist); err != nil {
		t.Fatal(err)
	}
	g.models.Rebuild()
	models = g.catalogModels("mock-native")
	if len(models) != 1 || models[0].ID != "mock-native/echo-pro" {
		t.Fatalf("allowlist 目录 = %+v", models)
	}
	if source, fetched := g.catalog.source("mock-native"); source != "static" || fetched != 0 {
		t.Fatalf("allowlist source = %q fetched = %d", source, fetched)
	}
	if len(g.models.Resolve("echo-live-only")) != 0 {
		t.Fatal("allowlist 模式下实时目录的模型不应可路由")
	}
	if len(g.models.Resolve("echo-pro")) == 0 {
		t.Fatal("allowlist 模式下静态模型应可路由")
	}
	if ids := servedModelIDs(t, g); !ids["mock-native/echo-pro"] || ids["mock-native/echo-live-only"] {
		t.Fatalf("allowlist /v1/models = %v", ids)
	}

	// KnownModels（/v1/models 的数据源）也必须跟随模式。
	known := map[string]bool{}
	for _, m := range g.models.KnownModels() {
		known[m.ID] = true
	}
	if !known["mock-native/echo-pro"] || known["mock-native/echo-live-only"] {
		t.Fatalf("allowlist KnownModels = %v", known)
	}
}

// allowlist 明确跳过上游 /models：不能刷新，也不显示实时来源。
func TestModelModeAllowlistSkipsUpstream(t *testing.T) {
	g := catalogGateway(t, t.TempDir())
	env := provider.NewAccountEnv("acc-mode", "opencode")
	env.Credentials = map[string]string{"api_key": "k", "base_url": "http://127.0.0.1:1"}
	env.HTTPClient = &http.Client{Timeout: time.Second}
	g.Pool().Add(env)

	if !g.catalog.supports("opencode") {
		t.Fatal("默认模式应支持从上游刷新")
	}
	if err := g.setProviderModelMode("opencode", ModelModeAllowlist); err != nil {
		t.Fatal(err)
	}
	if g.catalog.supports("opencode") {
		t.Fatal("allowlist 模式不应支持上游刷新")
	}
	if _, err := g.catalog.refresh(context.Background(), "opencode"); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("allowlist refresh err = %v", err)
	}
}

// 刷新全部目录会跳过 allowlist 渠道。
func TestRefreshAllProviderModelsSkipsAllowlist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"live-model-a"}]}`))
	}))
	defer srv.Close()

	g := catalogGateway(t, t.TempDir())
	env := provider.NewAccountEnv("acc-refresh-all", "opencode")
	env.Credentials = map[string]string{"api_key": "k", "base_url": srv.URL}
	env.HTTPClient = srv.Client()
	g.Pool().Add(env)

	call := func() map[string]any {
		rec := httptest.NewRecorder()
		g.handleRefreshAllProviderModels(rec, httptest.NewRequest(http.MethodPost, "/api/providers/models/refresh-all", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	if got := call(); got["refreshed"] != float64(1) {
		t.Fatalf("首次刷新 = %+v", got)
	}
	if err := g.setProviderModelMode("opencode", ModelModeAllowlist); err != nil {
		t.Fatal(err)
	}
	if got := call(); got["refreshed"] != float64(0) {
		t.Fatalf("allowlist 不应刷新: %+v", got)
	}
}

// 渠道保存接口能落盘模式，并在 providers 视图里回显。
func TestProviderModelModeSaveAndView(t *testing.T) {
	g := catalogGateway(t, t.TempDir())
	g.Pool().Add(provider.NewAccountEnv("acc-mode-save", "mock-native"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/provider/save",
		strings.NewReader(`{"id":"mock-native","modelMode":"fallback"}`))
	g.handleProviderSave(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := g.providerModelMode("mock-native"); got != ModelModeFallback {
		t.Fatalf("mode = %q", got)
	}

	var body struct {
		Providers []struct {
			ID         string `json:"id"`
			ModelsMode string `json:"models_mode"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode providers: %v", err)
	}
	found := false
	for _, p := range body.Providers {
		if p.ID == "mock-native" {
			found = true
			if p.ModelsMode != ModelModeFallback {
				t.Fatalf("providers 视图 modes_mode = %q", p.ModelsMode)
			}
		}
	}
	if !found {
		t.Fatal("providers 视图缺少 mock-native")
	}

	raw, err := os.ReadFile(g.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"provider_model_mode"`) {
		t.Fatalf("settings 未落盘: %s", raw)
	}

	// 非法模式必须拒绝，不能静默回落。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/provider/save",
		strings.NewReader(`{"id":"mock-native","modelMode":"nope"}`))
	g.handleProviderSave(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid mode status = %d body=%s", rec.Code, rec.Body.String())
	}
}

// Agent 模型选择器与渠道页、/v1/models 用同一份有效目录。
func TestModelOptionsUsesEffectiveCatalog(t *testing.T) {
	g := catalogGateway(t, t.TempDir())
	seedProviderLive(t, g, "mock-native", "echo-live-only")

	values := map[string]bool{}
	for _, o := range g.modelOptions() {
		values[o.Value] = true
	}
	if !values["mock-native/echo-pro"] || !values["mock-native/echo-live-only"] {
		t.Fatalf("merge modelOptions = %v", values)
	}

	if err := g.setProviderModelMode("mock-native", ModelModeAllowlist); err != nil {
		t.Fatal(err)
	}
	values = map[string]bool{}
	for _, o := range g.modelOptions() {
		values[o.Value] = true
	}
	if values["mock-native/echo-live-only"] {
		t.Fatalf("allowlist modelOptions 仍包含实时模型: %v", values)
	}
	if !values["mock-native/echo-pro"] {
		t.Fatalf("allowlist modelOptions 缺静态模型: %v", values)
	}
}

// 合并去重按归一化 ID：上游大小写不同不能产生两条记录。
func TestMergeCatalogModelsNormalizedID(t *testing.T) {
	live := []ir.ModelMeta{{ID: "p/DeepSeek-V4-Pro", DisplayName: "live"}}
	static := []ir.ModelMeta{{ID: "p/deepseek-v4-pro", DisplayName: "static"}}
	got := mergeCatalogModels(live, static)
	if len(got) != 1 || got[0].DisplayName != "live" {
		t.Fatalf("normalized merge = %+v", got)
	}
}
