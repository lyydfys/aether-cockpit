package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/mock"
)

func catalogGateway(t *testing.T, modelsDir string) *Gateway {
	t.Helper()
	return New(Config{
		SettingsPath: filepath.Join(t.TempDir(), "settings.json"),
		UsagePath:    "-", MaxRecords: 10, StateTTL: time.Minute,
		FirstByteTimeout: time.Second, MaxAttempts: 1, ModelsDir: modelsDir,
	})
}

// 有实时目录时以它为准，元数据按归一化 ID 从静态清单回填；未知模型保留。
func TestCatalogModelsMergeLiveAndStatic(t *testing.T) {
	dir := t.TempDir()
	g := catalogGateway(t, dir)
	g.catalog.mu.Lock()
	g.catalog.live["qoder-cn"] = liveModelFile{
		ProviderID: "qoder-cn", Fetched: time.Now().Unix(),
		Models: []string{"DeepSeek-V4.1-Flash", "deepseek-v4-pro", "brand-new-model"},
	}
	g.catalog.mu.Unlock()

	models := g.catalogModels("qoder-cn")
	if len(models) < 3 {
		t.Fatalf("models = %+v", models)
	}
	if models[0].ID != "qoder-cn/DeepSeek-V4.1-Flash" || models[0].DisplayName == "" {
		t.Fatalf("live 第一项 = %+v", models[0])
	}
	if !models[0].Capabilities.ToolCall || !models[0].Capabilities.Streaming {
		t.Fatalf("静态元数据未回填: %+v", models[0])
	}
	unknown := models[2]
	if unknown.ID != "qoder-cn/brand-new-model" || unknown.DisplayName != "brand-new-model" {
		t.Fatalf("未知模型 = %+v", unknown)
	}
	if !unknown.Capabilities.Streaming || !unknown.Capabilities.ToolCall {
		t.Fatalf("未知模型默认能力 = %+v", unknown)
	}
	if !hasModelID(models, "qoder-cn/Kimi-K2") {
		t.Fatalf("merge 应保留静态补缺的模型: %+v", models)
	}
	if source, _ := g.catalog.source("qoder-cn"); source != "live" {
		t.Fatalf("source = %q", source)
	}

	// 没有实时目录的渠道退回静态清单。
	fallback := g.catalogModels("antigravity")
	if len(fallback) == 0 {
		t.Fatal("静态兜底为空")
	}
	if source, _ := g.catalog.source("antigravity"); source != "static" {
		t.Fatalf("fallback source = %q", source)
	}
}

// 刷新会调用上游 /models、落盘，并让路由表立即可解析新模型。
func TestCatalogRefreshFetchesAndPersists(t *testing.T) {
	dir := t.TempDir()
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"live-model-a"},{"id":"live-model-b"}]}`))
	}))
	defer srv.Close()

	g := catalogGateway(t, dir)
	env := provider.NewAccountEnv("acc-live", "opencode")
	env.Credentials = map[string]string{"api_key": "oc_sk_live", "base_url": srv.URL}
	env.HTTPClient = srv.Client()
	g.Pool().Add(env)

	n, err := g.catalog.refresh(context.Background(), "opencode")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if n != 2 {
		t.Fatalf("n = %d", n)
	}
	if gotAuth != "Bearer oc_sk_live" {
		t.Fatalf("鉴权头 = %q", gotAuth)
	}
	if source, fetched := g.catalog.source("opencode"); source != "live" || fetched == 0 {
		t.Fatalf("source = %q fetched = %d", source, fetched)
	}
	if bindings := g.models.Resolve("opencode/live-model-a"); len(bindings) == 0 {
		t.Fatal("实时模型未进入路由表")
	}

	// 新进程用同一目录应能恢复实时目录。
	g2 := catalogGateway(t, dir)
	models := g2.catalogModels("opencode")
	found := false
	for _, m := range models {
		if m.UpstreamModel == "live-model-a" {
			found = true
		}
	}
	if !found {
		t.Fatalf("缓存未恢复: %+v", models)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f liveModelFile
	if err := json.Unmarshal(raw, &f); err != nil || len(f.Models) != 2 {
		t.Fatalf("缓存文件 = %s (%v)", raw, err)
	}
}

// models.dev 元数据：归一化/别名匹配、静态补全、未知实时模型的能力位。
func TestCatalogMetadataEnrichment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "deepseek": {"models": {
		    "deepseek-flash": {
		      "id": "deepseek-flash", "name": "DeepSeek V4.1 Flash",
		      "tool_call": true, "reasoning": true,
		      "modalities": {"input": ["text","image"]},
		      "limit": {"context": 1000000},
		      "cost": {"input": 0.15, "output": 0.6, "cache_read": 0.003},
		      "reasoning_options": [{"type": "effort", "values": ["low","high","max"]}],
		      "canonical_model_id": "deepseek/deepseek-v4.1-flash"
		    }
		  }},
		  "meta": {"models": {
		    "llama-4-meta": {"id": "llama-4-meta", "name": "Llama 4", "status": "deprecated", "limit": {"context": 128000}}
		  }},
		  "router": {"models": {
		    "auto": {"id": "auto", "name": "Auto Router", "limit": {"context": 2000000}}
		  }}
		}`))
	}))
	defer srv.Close()
	t.Setenv("AETHER_MODELS_DEV_URL", srv.URL)

	dir := t.TempDir()
	g := catalogGateway(t, dir)
	if err := g.catalog.refreshMetadata(context.Background()); err != nil {
		t.Fatalf("refreshMetadata: %v", err)
	}
	if e, ok := g.catalog.lookupMeta("DeepSeek-V4.1-Flash"); !ok || e.Context != 1000000 || !e.Vision {
		t.Fatalf("元数据查找 = %+v %v", e, ok)
	}
	if _, ok := g.catalog.lookupMeta("llama-4-meta"); !ok {
		t.Fatal("deprecated 条目也应回填")
	}
	if _, ok := g.catalog.lookupMeta("auto"); ok {
		t.Fatal("通用名 auto 不应匹配具体模型元数据")
	}
	info := g.catalog.info("deepseek-flash")
	if !info.CostKnown || info.CostIn != 0.15 || info.CostOut != 0.6 || info.CostCacheRead != 0.003 {
		t.Fatalf("价格元数据 = %+v", info)
	}
	if len(info.Efforts) != 3 || info.Efforts[2] != "max" {
		t.Fatalf("推理档位 = %+v", info.Efforts)
	}

	// 静态目录补全：Qoder 的 V4.1 没有窗口/能力信息，从 models.dev 补。
	static := g.catalogModels("qoder-cn")
	var found bool
	for _, m := range static {
		if m.UpstreamModel == "DeepSeek-V4.1-Flash" {
			found = true
			if m.ContextWindow != 1000000 {
				t.Fatalf("静态模型上下文未补全: %+v", m)
			}
		}
		if m.UpstreamModel == "auto" && m.ContextWindow != 0 {
			t.Fatalf("auto 不应被补成具体模型的窗口: %+v", m)
		}
	}
	if !found {
		t.Fatal("静态模型缺失")
	}

	// 未知实时模型：能力位以 models.dev 为准；完全未知的保持默认。
	g.catalog.mu.Lock()
	g.catalog.live["qoder-cn"] = liveModelFile{
		ProviderID: "qoder-cn", Fetched: time.Now().Unix(),
		Models: []string{"deepseek-flash", "mystery-model"},
	}
	g.catalog.mu.Unlock()
	// 这一段只关心实时目录的元数据补全，切到 fallback 避免静态补缺参与断言。
	if err := g.setProviderModelMode("qoder-cn", ModelModeFallback); err != nil {
		t.Fatalf("set mode: %v", err)
	}
	live := g.catalogModels("qoder-cn")
	if len(live) != 2 {
		t.Fatalf("live = %+v", live)
	}
	if live[0].ContextWindow != 1000000 || !live[0].Capabilities.Vision || live[0].DisplayName != "DeepSeek V4.1 Flash" {
		t.Fatalf("实时模型元数据未合并: %+v", live[0])
	}
	if !live[1].Capabilities.ToolCall || !live[1].Capabilities.Streaming {
		t.Fatalf("未知模型默认能力 = %+v", live[1])
	}

	// 缓存文件在新进程里应能恢复。
	g2 := catalogGateway(t, dir)
	if e, ok := g2.catalog.lookupMeta("deepseek-flash"); !ok || e.Name != "DeepSeek V4.1 Flash" {
		t.Fatalf("元数据缓存未恢复: %+v %v", e, ok)
	}
}

func TestCatalogMetadataRefreshRebuildsCanonicalAliases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "qoder": {"models": {"qoder-special-model-2026": {"id":"qoder-special-model-2026","name":"Qoder Special","canonical_model_id":"test-vendor/canonical-model-2026"}}},
		  "workbuddy": {"models": {"workbuddy-special-model-2026": {"id":"workbuddy-special-model-2026","name":"WorkBuddy Special","canonical_model_id":"test-vendor/canonical-model-2026"}}}
		}`))
	}))
	defer srv.Close()
	t.Setenv("AETHER_MODELS_DEV_URL", srv.URL)

	g := catalogGateway(t, t.TempDir())
	g.catalog.mu.Lock()
	g.catalog.live["qoder-cn"] = liveModelFile{ProviderID: "qoder-cn", Entries: []provider.ModelInfo{{ID: "qoder-special-model-2026", UpstreamModel: "qoder-upstream"}}}
	g.catalog.live["workbuddy-intl"] = liveModelFile{ProviderID: "workbuddy-intl", Entries: []provider.ModelInfo{{ID: "workbuddy-special-model-2026", UpstreamModel: "workbuddy-upstream"}}}
	g.catalog.rebuildLiveInfo()
	g.catalog.mu.Unlock()
	g.models.Rebuild()

	canonical := "test-vendor/canonical-model-2026"
	if got := g.models.Resolve(canonical); len(got) != 0 {
		t.Fatalf("刷新前不应有 canonical 绑定: %+v", got)
	}
	if err := g.catalog.refreshMetadata(context.Background()); err != nil {
		t.Fatalf("refreshMetadata: %v", err)
	}
	bindings := g.models.Resolve(canonical)
	got := map[string]string{}
	for _, b := range bindings {
		got[b.ProviderID] = b.UpstreamModel
	}
	if got["qoder-cn"] != "qoder-upstream" || got["workbuddy-intl"] != "workbuddy-upstream" {
		t.Fatalf("元数据刷新后 canonical 别名未重建: %+v", bindings)
	}
}

func TestCatalogAliasesForConcurrentLiveRefresh(t *testing.T) {
	g := catalogGateway(t, t.TempDir())
	const iterations = 1000
	start := make(chan struct{})
	var wg sync.WaitGroup
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < iterations; i++ {
				g.catalog.aliasesFor("qoder-cn", "qoder-fast")
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < iterations; i++ {
			g.catalog.mu.Lock()
			g.catalog.liveInfo = map[string]provider.ModelInfo{
				liveKey("qoder-cn", "qoder-fast"): {ID: "qoder-fast", Name: "Qoder Fast"},
			}
			g.catalog.mu.Unlock()
		}
	}()
	close(start)
	wg.Wait()
}

func TestClampReasoningEffort(t *testing.T) {
	g := catalogGateway(t, t.TempDir())
	g.catalog.meta["claudeopus"] = metaEntry{Efforts: []string{"low", "high", "max"}}
	cases := []struct{ model, in, want string }{
		{"qoder-cn/Claude-Opus", "medium", "low"}, // 等距取更低档
		{"qoder-cn/Claude-Opus", "xhigh", "high"},
		{"qoder-cn/Claude-Opus", "max", "max"},
		{"qoder-cn/Claude-Opus", "minimal", "low"},
		{"qoder-cn/Claude-Opus", "none", ""},
		{"unknown-model", "minimal", "low"}, // 无目录：只做别名归一
		{"unknown-model", "ultra", "ultra"},
	}
	for _, tc := range cases {
		if got := g.clampReasoningEffort(tc.model, tc.in); got != tc.want {
			t.Errorf("clamp(%q,%q) = %q, want %q", tc.model, tc.in, got, tc.want)
		}
	}
}

// 端到端：模型支持 low/high，客户端要 medium → 上游实际收到 low。
func TestReasoningEffortClampedEndToEnd(t *testing.T) {
	mock.Register()
	g := catalogGateway(t, t.TempDir())
	g.catalog.metaMu.Lock()
	g.catalog.meta["echopro"] = metaEntry{Efforts: []string{"low", "high"}}
	g.catalog.metaMu.Unlock()
	env := provider.NewAccountEnv("acc-mock", "mock-native")
	env.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	g.Pool().Add(env)

	body := `{"model":"mock-native/echo-pro","stream":false,"reasoning_effort":"medium","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "reasoning.effort=low") {
		t.Fatalf("上游收到的档位不是 low: %s", rec.Body.String())
	}
}

// /v1/models 必须包含实时目录的模型，并带上窗口/能力/efforts/价格。
func TestModelsEndpointIncludesLiveAndMetadata(t *testing.T) {
	mock.Register()
	g := catalogGateway(t, t.TempDir())
	env := provider.NewAccountEnv("acc-mock", "mock-native")
	env.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	g.Pool().Add(env)

	g.catalog.mu.Lock()
	g.catalog.live["mock-native"] = liveModelFile{
		ProviderID: "mock-native", Fetched: time.Now().Unix(), Models: []string{"live-echo"},
	}
	g.catalog.mu.Unlock()
	g.catalog.metaMu.Lock()
	g.catalog.meta["liveecho"] = metaEntry{
		Context: 555000, ToolCall: true, Reasoning: true,
		CostKnown: true, CostIn: 1.25, CostOut: 5, CostCacheRead: 0.1,
		Efforts: []string{"low", "high"},
	}
	g.catalog.metaMu.Unlock()
	g.models.Rebuild()

	rec := httptest.NewRecorder()
	g.handleModels(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	var out struct {
		Data []struct {
			ID            string             `json:"id"`
			ContextWindow int                `json:"context_window"`
			Capabilities  map[string]bool    `json:"capabilities"`
			Efforts       []string           `json:"efforts"`
			Cost          map[string]float64 `json:"cost"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var hit bool
	for _, m := range out.Data {
		if m.ID != "mock-native/live-echo" {
			continue
		}
		hit = true
		if m.ContextWindow != 555000 || !m.Capabilities["tool_call"] || !m.Capabilities["streaming"] {
			t.Fatalf("模型元数据 = %+v", m)
		}
		if len(m.Efforts) != 2 || m.Cost["input_per_m"] != 1.25 {
			t.Fatalf("efforts/cost = %+v", m)
		}
	}
	if !hit {
		t.Fatalf("实时模型未进入 /v1/models: %s", rec.Body.String())
	}
}

// 同名模型在不同渠道的倍率必须隔离（Qoder 的 Auto 不能套到 WorkBuddy 的 auto）。
func TestCatalogInfoScopedPerProvider(t *testing.T) {
	g := catalogGateway(t, t.TempDir())
	g.catalog.mu.Lock()
	g.catalog.liveInfo[liveKey("qoder-cn", "auto")] = provider.ModelInfo{ID: "auto", Rate: 0.5}
	g.catalog.liveInfo[liveKey("workbuddy-cn", "auto")] = provider.ModelInfo{ID: "auto", Rate: 0}
	g.catalog.mu.Unlock()
	if info := g.catalogInfoFor("qoder-cn", "auto"); info.Rate != 0.5 {
		t.Fatalf("qoder auto rate = %v", info.Rate)
	}
	if info := g.catalogInfoFor("workbuddy-cn", "auto"); info.Rate != 0 {
		t.Fatalf("workbuddy auto rate = %v", info.Rate)
	}
	if info := g.catalogInfo("qoder-cn/auto"); info.Rate != 0.5 {
		t.Fatalf("full-ID lookup = %v", info.Rate)
	}
}

// 同一模型的不同写法/不同渠道（canonical 别名）应归到一条跨渠道候选链。
func TestCanonicalAliasRoutingAcrossProviders(t *testing.T) {
	g := catalogGateway(t, t.TempDir())
	g.catalog.mu.Lock()
	g.catalog.live["qoder-cn"] = liveModelFile{
		ProviderID: "qoder-cn", Fetched: time.Now().Unix(),
		Entries: []provider.ModelInfo{{ID: "DeepSeek-Flash", UpstreamModel: "dfmodel", Name: "DeepSeek V4.1 Flash"}},
	}
	g.catalog.live["workbuddy-intl"] = liveModelFile{
		ProviderID: "workbuddy-intl", Fetched: time.Now().Unix(),
		Entries: []provider.ModelInfo{{ID: "deepseek-v4.1-flash", UpstreamModel: "deepseek-v4.1-flash", Name: "Deepseek-V4.1-Flash"}},
	}
	g.catalog.rebuildLiveInfo()
	g.catalog.mu.Unlock()
	g.catalog.metaMu.Lock()
	g.catalog.meta["deepseekflash"] = metaEntry{Name: "DeepSeek V4.1 Flash", Canonical: "deepseek/deepseek-v4.1-flash"}
	g.catalog.metaMu.Unlock()
	g.models.Rebuild()

	bindings := g.models.Resolve("DeepSeek-V4.1-Flash")
	got := map[string]string{}
	for _, b := range bindings {
		got[b.ProviderID] = b.UpstreamModel
	}
	if got["qoder-cn"] != "dfmodel" {
		t.Fatalf("Qoder 的显示名未按 canonical 归链: %+v", bindings)
	}
	if got["workbuddy-intl"] != "deepseek-v4.1-flash" {
		t.Fatalf("精确 ID 未归链: %+v", bindings)
	}
	if len(bindings) < 3 {
		t.Fatalf("应包含多个可用渠道: %+v", bindings)
	}

	// 有可用账号的渠道排到链首（其余渠道保留在链尾，仍可失败转移）。
	env := provider.NewAccountEnv("wb-live", "workbuddy-intl")
	env.HTTPClient = &http.Client{Timeout: time.Second}
	g.Pool().Add(env)
	bindings = g.models.Resolve("DeepSeek-V4.1-Flash")
	if bindings[0].ProviderID != "workbuddy-intl" {
		t.Fatalf("有额度的渠道应优先: %+v", bindings)
	}
}
