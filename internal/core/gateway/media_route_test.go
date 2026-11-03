package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/core/tools"
	"aether-cockpit/internal/provider"
)

// 媒体端点接入账号池/渠道路由的回归测试（P1-5）。
//
// 覆盖三件事：
//  1. 模型能解析到渠道时，凭证与地址都来自渠道账号，而不是设置页；
//  2. 模型解析不到时，行为与改动前逐字节一致（设置页单一配置）；
//  3. 渠道命中但账号全不可用时，明确报错且一个字节都不发出去。
//
// 另外补两条护栏：路由器"家族兜底"不能改道媒体请求；视频轮询要跟着创建
// 时的渠道走。

// newMediaGateway 造一个干净的网关（不加载任何 spec、不带账号）。
func newMediaGateway(t *testing.T) *Gateway {
	t.Helper()
	return New(Config{
		MaxAttempts:             1,
		MaxConcurrentPerAccount: -1,
		SettingsPath:            filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath:        filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:               "-",
		MaxRecords:              20,
	})
}

// withChannelBaseURL 给某渠道装一份 spec，声明它的上游 base_url。
//
// spec 是进程级全局状态，测试结束必须还原：否则后面跑的用例会看到这份
// 幽灵配置（尤其是模型清单与 base_url）。
func withChannelBaseURL(t *testing.T, providerID, baseURL string) {
	t.Helper()
	prev := spec.Default()
	spec.SetDefault(spec.NewStore())
	t.Cleanup(func() { spec.SetDefault(prev) })

	dir := t.TempDir()
	payload := `{"provider_id":"` + providerID + `","base_url":"` + baseURL + `"}`
	if err := os.WriteFile(filepath.Join(dir, "spec.json"), []byte(payload), 0o600); err != nil {
		t.Fatalf("写 spec: %v", err)
	}
	if err := spec.Default().LoadDir(dir); err != nil {
		t.Fatalf("加载 spec: %v", err)
	}
}

// TestMediaTargetRoutesToChannel 是主路径：模型带渠道前缀 → 用渠道地址与账号凭证。
func TestMediaTargetRoutesToChannel(t *testing.T) {
	var gotAuth, gotModel, gotHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotAuth = r.Header.Get("Authorization")
		var payload map[string]any
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		gotModel, _ = payload["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"url":"https://img.example/a.png"}]}`))
	}))
	defer upstream.Close()
	withChannelBaseURL(t, "mock-native", upstream.URL)

	g := newMediaGateway(t)
	// 设置页故意指向连不上的地址：只要请求成功，就证明走的是渠道。
	g.settings.Tools = &ToolsConfig{Image: &tools.ImageConfig{
		BaseURL: "http://127.0.0.1:1", APIKey: "settings-key", Model: "gpt-image-1",
	}}
	env := provider.NewAccountEnv("media-route-acc", "mock-native")
	env.SetCredential("api_key", "sk-channel")
	g.pool.Add(env)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations",
		strings.NewReader(`{"model":"mock-native/gpt-image-1","prompt":"a red dot"}`))
	g.handleImageGenerate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if gotAuth != "Bearer sk-channel" {
		t.Fatalf("Authorization = %q（应使用渠道账号凭证）", gotAuth)
	}
	if gotModel != "gpt-image-1" {
		t.Fatalf("上游 model = %q（应剥掉渠道前缀）", gotModel)
	}
	if want := strings.TrimPrefix(upstream.URL, "http://"); gotHost != want {
		t.Fatalf("Host = %q，应为渠道上游 %q", gotHost, want)
	}
}

// TestMediaTargetAccountBaseURLWinsOverSpec 账号自带的 base_url 优先于渠道 spec：
// 同一渠道下不同账号可能落在不同域名。
func TestMediaTargetAccountBaseURLWinsOverSpec(t *testing.T) {
	var specHits, accountHits atomic.Int32
	specSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		specHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer specSrv.Close()
	accountSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accountHits.Add(1)
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3media"))
	}))
	defer accountSrv.Close()
	withChannelBaseURL(t, "mock-native", specSrv.URL)

	g := newMediaGateway(t)
	env := provider.NewAccountEnv("media-base-acc", "mock-native")
	env.SetCredential("api_key", "sk-account")
	env.SetCredential("base_url", accountSrv.URL)
	g.pool.Add(env)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech",
		strings.NewReader(`{"model":"mock-native/tts-1","input":"hi"}`))
	g.handleAudioSpeech(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "ID3media" {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
	}
	if accountHits.Load() == 0 {
		t.Fatal("账号 base_url 未被使用")
	}
	if specHits.Load() != 0 {
		t.Fatalf("渠道 spec 地址被误用：%d 次", specHits.Load())
	}
}

// TestMediaTargetFallsBackToSettings 是向后兼容的关键：没有渠道可解析时
// 与改动前完全一致——设置页地址、设置页 Key、设置页模型只作缺省值。
func TestMediaTargetFallsBackToSettings(t *testing.T) {
	var gotAuth, gotModel, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		var payload map[string]any
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		gotModel, _ = payload["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"request_id":"req_settings"}`))
	}))
	defer upstream.Close()

	g := newMediaGateway(t) // 没有 spec、没有账号
	g.settings.Tools = &ToolsConfig{Video: &tools.VideoConfig{
		BaseURL: upstream.URL, APIKey: "settings-key", Model: "grok-imagine-video",
	}}

	// 1) 请求没带模型：设置页模型作为缺省值注入（改动前就是这个行为）。
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/videos/generations", strings.NewReader(`{"prompt":"a cat"}`))
	g.handleVideoGenerate(rec, req)
	if rec.Code != http.StatusOK || gotPath != "/videos/generations" {
		t.Fatalf("status = %d path = %q body = %s", rec.Code, gotPath, rec.Body.String())
	}
	if gotAuth != "Bearer settings-key" || gotModel != "grok-imagine-video" {
		t.Fatalf("auth = %q model = %q（应沿用设置页配置）", gotAuth, gotModel)
	}

	// 2) 请求带了未登记的模型名：设置页路径不改写客户端的模型（与改动前一致）。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/videos/generations",
		strings.NewReader(`{"model":"zzz-unknown-media-model","prompt":"a cat"}`))
	g.handleVideoGenerate(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if gotModel != "zzz-unknown-media-model" {
		t.Fatalf("model = %q（设置页路径不应覆盖客户端显式模型）", gotModel)
	}
}

// TestMediaTargetNoAvailableAccount 渠道命中但没有可用账号：明确报错，且不发请求。
func TestMediaTargetNoAvailableAccount(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer upstream.Close()
	withChannelBaseURL(t, "mock-native", upstream.URL)

	g := newMediaGateway(t)
	env := provider.NewAccountEnv("media-cooling-acc", "mock-native")
	env.SetCredential("api_key", "sk-cool")
	env.MarkCoolingDown(time.Hour, "测试：全冷却")
	g.pool.Add(env)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations",
		strings.NewReader(`{"model":"mock-native/gpt-image-1","prompt":"x"}`))
	g.handleImageGenerate(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s（账号全冷却时应明确报错）", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "冷却") {
		t.Fatalf("错误文案应说明账号状态: %s", rec.Body.String())
	}
	if hits.Load() != 0 {
		t.Fatalf("账号不可用时不应向上游发请求，实际 %d 次", hits.Load())
	}
}

// TestMediaBindingsRejectsFamilyFallback 护栏：路由器的"模型家族兜底"不能把
// 生图请求改道到同族对话模型上（否则存量用户会被静默改道）。
func TestMediaBindingsRejectsFamilyFallback(t *testing.T) {
	r := NewModelRouter()
	r.Rebuild()
	// 直接塞一条 gpt 族绑定，模拟真实渠道声明了对话模型 gpt-5 的情形。
	// （测试里改内部字段是刻意的：生产代码路径不会这样做。）
	r.bare["gpt-5-test"] = []modelBinding{{
		ProviderID: "mock-native", UpstreamModel: "gpt-5-test", DisplayName: "GPT-5 Test",
	}}
	g := &Gateway{models: r}

	if got := r.Resolve("gpt-image-1"); len(got) == 0 {
		t.Fatalf("前置条件不成立：路由器家族兜底应命中，实际 %+v", got)
	}
	if got := g.mediaBindings("gpt-image-1"); len(got) != 0 {
		t.Fatalf("媒体路由不应接受家族兜底绑定: %+v", got)
	}
	// 精确名不受影响：上游名与请求名一致时正常参与。
	if got := g.mediaBindings("echo-pro"); len(got) != 1 || got[0].UpstreamModel != "echo-pro" {
		t.Fatalf("精确名解析失败: %+v", got)
	}
}

// TestVideoRouteCache 记住创建渠道、忽略设置页目标、过期视为未命中。
func TestVideoRouteCache(t *testing.T) {
	target := mediaTarget{ProviderID: "mock-native", BaseURL: "http://upstream.example"}
	rememberVideoRoute("media-test-task-1", target)
	if got, ok := videoRouteFor("media-test-task-1"); !ok || got.ProviderID != "mock-native" {
		t.Fatalf("任务路由未记住: %+v ok=%v", got, ok)
	}

	// 设置页路径不需要记（它本来就是轮询时的回退）。
	rememberVideoRoute("media-test-task-2", mediaTarget{BaseURL: "http://settings.example"})
	if _, ok := videoRouteFor("media-test-task-2"); ok {
		t.Fatal("设置页目标不该进任务路由表")
	}

	videoRouteCache.mu.Lock()
	videoRouteCache.items["media-test-task-3"] = videoRouteEntry{
		target: target, expires: time.Now().Add(-time.Second),
	}
	videoRouteCache.mu.Unlock()
	if _, ok := videoRouteFor("media-test-task-3"); ok {
		t.Fatal("过期条目应视为未命中")
	}
}

// TestVideoPollingFollowsChannelRoute 端到端：创建走渠道，轮询不带模型也必须
// 回到同一个渠道（否则永远拿不到那条上游的任务）。
func TestVideoPollingFollowsChannelRoute(t *testing.T) {
	var channelPolls, settingsHits atomic.Int32
	channelUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/videos/generations" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"request_id":"req_media_track_1","status":"pending"}`))
		case r.URL.Path == "/videos/req_media_track_1":
			channelPolls.Add(1)
			_, _ = w.Write([]byte(`{"request_id":"req_media_track_1","status":"done"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer channelUpstream.Close()
	settingsUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		settingsHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer settingsUpstream.Close()
	withChannelBaseURL(t, "mock-native", channelUpstream.URL)

	g := newMediaGateway(t)
	g.settings.Tools = &ToolsConfig{Video: &tools.VideoConfig{
		BaseURL: settingsUpstream.URL, APIKey: "settings-key", Model: "grok-imagine-video",
	}}
	env := provider.NewAccountEnv("media-video-acc", "mock-native")
	env.SetCredential("api_key", "sk-video")
	g.pool.Add(env)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/videos/generations",
		strings.NewReader(`{"model":"mock-native/grok-imagine-video","prompt":"a cat surfing"}`))
	g.handleVideoGenerate(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "req_media_track_1") {
		t.Fatalf("创建失败: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/videos/req_media_track_1", nil)
	req.SetPathValue("id", "req_media_track_1")
	g.handleVideoGet(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"done"`) {
		t.Fatalf("轮询失败: %d %s", rec.Code, rec.Body.String())
	}
	if channelPolls.Load() == 0 {
		t.Fatal("轮询没有回到创建时用的渠道")
	}
	if settingsHits.Load() != 0 {
		t.Fatalf("轮询误打到设置页地址：%d 次", settingsHits.Load())
	}
}
