package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestPresetsSaveAttachAndMerge(t *testing.T) {
	var gotBody map[string]any
	var gotHeader http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = nil
		_ = json.Unmarshal(raw, &gotBody)
		gotHeader = r.Header.Clone()
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
	})
	env := mustCreateAccount(t, g, "mock-native", "preset@example.com")
	if rec := saveHeaders(t, g, `{"id":"mock-native","headers":{"X-Static":"static"},"extraBody":{"temperature":0.1,"static":true}}`); rec.Code != http.StatusOK {
		t.Fatalf("static headers/extraBody: %d %s", rec.Code, rec.Body.String())
	}

	save := func(body string) {
		t.Helper()
		rec := httptest.NewRecorder()
		g.handlePresetSave(rec, httptest.NewRequest(http.MethodPost, "/api/presets/save", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("save preset %s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	save(`{"name":"channel preset","body":{"temperature":0.2,"top_p":0.9},"headers":{"X-Layer":"channel"}}`)
	save(`{"name":"route preset","body":{"temperature":0.3,"route":true},"headers":{"X-Layer":"route"}}`)

	g.settingsMu.Lock()
	id1 := g.settings.RequestPresets[0].ID
	id2 := g.settings.RequestPresets[1].ID
	g.settingsMu.Unlock()

	g.models.SetVirtualGroup("auto/test", []string{"mock-native"})
	attach := func(scope, key, id string) {
		t.Helper()
		rec := httptest.NewRecorder()
		body := fmt.Sprintf(`{"scope":%q,"key":%q,"ids":[%q]}`, scope, key, id)
		g.handlePresetAttach(rec, httptest.NewRequest(http.MethodPost, "/api/presets/attach", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("attach %s/%s: %d %s", scope, key, rec.Code, rec.Body.String())
		}
	}
	attach("provider", "mock-native", id1)
	attach("route", "auto/test", id2)

	env.SetHTTPClient(srv.Client())
	g.applyProviderTransport(env)
	req, _ := http.NewRequestWithContext(withPresetRoute(context.Background(), "auto/test"),
		http.MethodPost, srv.URL, strings.NewReader(`{"model":"mock-native/echo-pro","temperature":0.01,"messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := env.HTTPClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()

	if gotBody["temperature"] != 0.3 || gotBody["top_p"] != 0.9 || gotBody["static"] != true || gotBody["route"] != true {
		t.Fatalf("body merge = %+v", gotBody)
	}
	if gotHeader.Get("X-Layer") != "route" || gotHeader.Get("X-Static") != "static" {
		t.Fatalf("header merge = %+v", gotHeader)
	}

	// 预设停用后不再生效。
	rec := httptest.NewRecorder()
	g.handlePresetSave(rec, httptest.NewRequest(http.MethodPost, "/api/presets/save",
		strings.NewReader(fmt.Sprintf(`{"id":%q,"name":"route preset","enabled":false}`, id2))))
	if rec.Code != http.StatusOK {
		t.Fatalf("disable preset: %d %s", rec.Code, rec.Body.String())
	}
	req2, _ := http.NewRequestWithContext(withPresetRoute(context.Background(), "auto/test"),
		http.MethodPost, srv.URL, strings.NewReader(`{"model":"mock-native/echo-pro","temperature":0.01,"messages":[]}`))
	req2.Header.Set("Content-Type", "application/json")
	resp, err = env.HTTPClient.Do(req2)
	if err != nil {
		t.Fatalf("request after disable: %v", err)
	}
	resp.Body.Close()
	if gotBody["temperature"] != 0.2 || gotBody["route"] != nil {
		t.Fatalf("disabled route preset still applied: %+v", gotBody)
	}

	// 删除渠道预设后，渠道挂载也应被清掉。
	rec = httptest.NewRecorder()
	g.handlePresetDelete(rec, httptest.NewRequest(http.MethodPost, "/api/presets/delete",
		strings.NewReader(fmt.Sprintf(`{"id":%q}`, id1))))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete preset: %d %s", rec.Code, rec.Body.String())
	}
	g.settingsMu.Lock()
	_, hasProvider := g.settings.ProviderPresets["mock-native"]
	g.settingsMu.Unlock()
	if hasProvider {
		t.Fatal("删除预设后渠道挂载应被清理")
	}
}

func TestRequestPresetValidation(t *testing.T) {
	g := New(Config{Addr: "127.0.0.1:0", SettingsPath: filepath.Join(t.TempDir(), "settings.json")})
	cases := []struct {
		name string
		body string
	}{
		{"空名称", `{"name":" "}`},
		{"保留字段", `{"name":"bad","body":{"model":"x"}}`},
		{"非法头", `{"name":"bad","headers":{"Bad Name":"v"}}`},
		{"换行头", `{"name":"bad","headers":{"X-Test":"a\nb"}}`},
		{"Body 过大", `{"name":"big","body":{"blob":"` + strings.Repeat("a", 70000) + `"}}`},
		{"非法 ID", `{"id":"Bad ID","name":"bad"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			g.handlePresetSave(rec, httptest.NewRequest(http.MethodPost, "/api/presets/save", strings.NewReader(c.body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}

	rec := httptest.NewRecorder()
	g.handlePresetAttach(rec, httptest.NewRequest(http.MethodPost, "/api/presets/attach",
		strings.NewReader(`{"scope":"provider","key":"not-a-provider","ids":[]}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown provider attach = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	g.handlePresetAttach(rec, httptest.NewRequest(http.MethodPost, "/api/presets/attach",
		strings.NewReader(`{"scope":"route","key":"not-a-group","ids":[]}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown route attach = %d", rec.Code)
	}
}

func TestRequestPresetHeadersMasked(t *testing.T) {
	g := New(Config{Addr: "127.0.0.1:0", SettingsPath: filepath.Join(t.TempDir(), "settings.json")})
	rec := httptest.NewRecorder()
	g.handlePresetSave(rec, httptest.NewRequest(http.MethodPost, "/api/presets/save",
		strings.NewReader(`{"name":"auth","headers":{"Authorization":"Bearer secret-token-1234"}}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("save = %d %s", rec.Code, rec.Body.String())
	}
	g.settingsMu.Lock()
	id := g.settings.RequestPresets[0].ID
	g.settingsMu.Unlock()

	rec = httptest.NewRecorder()
	g.handlePresets(rec, httptest.NewRequest(http.MethodGet, "/api/presets", nil))
	var view struct {
		Presets []requestPreset `json:"presets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Presets) != 1 || !strings.HasPrefix(view.Presets[0].Headers["Authorization"], "••••") {
		t.Fatalf("敏感头应打码: %+v", view.Presets)
	}

	// 把打码值回传：应保留旧值。
	rec = httptest.NewRecorder()
	g.handlePresetSave(rec, httptest.NewRequest(http.MethodPost, "/api/presets/save",
		strings.NewReader(fmt.Sprintf(`{"id":%q,"name":"auth","headers":{"Authorization":%q}}`, id, view.Presets[0].Headers["Authorization"]))))
	if rec.Code != http.StatusOK {
		t.Fatalf("save masked = %d %s", rec.Code, rec.Body.String())
	}
	g.settingsMu.Lock()
	got := g.settings.RequestPresets[0].Headers["Authorization"]
	g.settingsMu.Unlock()
	if got != "Bearer secret-token-1234" {
		t.Fatalf("掩码回传应保留旧值, got %q", got)
	}
}

func TestPresetSystemPromptResolution(t *testing.T) {
	g := New(Config{Addr: "127.0.0.1:0", SettingsPath: filepath.Join(t.TempDir(), "settings.json")})
	rec := httptest.NewRecorder()
	g.handlePresetSave(rec, httptest.NewRequest(http.MethodPost, "/api/presets/save",
		strings.NewReader(`{"name":"system","system_prompt":"渠道提示","concat_system":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("save preset: %d %s", rec.Code, rec.Body.String())
	}
	g.settingsMu.Lock()
	id := g.settings.RequestPresets[0].ID
	g.settingsMu.Unlock()
	rec = httptest.NewRecorder()
	g.handlePresetAttach(rec, httptest.NewRequest(http.MethodPost, "/api/presets/attach",
		strings.NewReader(fmt.Sprintf(`{"scope":"provider","key":"mock-native","ids":[%q]}`, id))))
	if rec.Code != http.StatusOK {
		t.Fatalf("attach: %d %s", rec.Code, rec.Body.String())
	}
	ov := g.presetsForRequest(context.Background(), "mock-native")
	if ov.SystemPrompt != "渠道提示" || !ov.ConcatSystem {
		t.Fatalf("preset system prompt = %+v", ov)
	}
}

// 确保 presetsForRequest 在没有挂载时零开销返回 nil。
func TestPresetsForRequestEmpty(t *testing.T) {
	g := New(Config{Addr: "127.0.0.1:0", SettingsPath: filepath.Join(t.TempDir(), "settings.json")})
	ov := g.presetsForRequest(context.Background(), "mock-native")
	if ov.Body != nil || ov.Headers != nil || ov.Names != nil || ov.SystemPrompt != "" {
		t.Fatalf("空配置应返回空 overlay: %+v", ov)
	}
}

// —— 酒馆预设导入（preset_import.go） ——
//
// 样例是**构造**的：仓库里没有真实导出文件，字段名依据酒馆公开结构。
// 解析器是纯函数，这里覆盖两种格式、别名归一、stop 归一、丢弃分组与坏输入。

func findPresetDrop(preview presetImportPreview, key string) (presetImportDrop, bool) {
	for _, item := range preview.Dropped {
		if item.Key == key {
			return item, true
		}
	}
	return presetImportDrop{}, false
}

func findPresetMapped(preview presetImportPreview, key string) (presetImportField, bool) {
	for _, item := range preview.Mapped {
		if item.Key == key {
			return item, true
		}
	}
	return presetImportField{}, false
}

func presetBodyNumber(t *testing.T, preview presetImportPreview, key string) float64 {
	t.Helper()
	value, ok := preview.Body[key]
	if !ok {
		t.Fatalf("body 缺少 %s，实际 %v", key, preview.Body)
	}
	number, ok := value.(float64)
	if !ok {
		t.Fatalf("body[%s] 不是数字：%T", key, value)
	}
	return number
}

func TestParseSillyTavernChatPreset(t *testing.T) {
	raw := `{
		"name": "聊天预设",
		"temperature": 0.7,
		"top_p": 0.9,
		"top_k": 40,
		"frequency_penalty": 0.1,
		"presence_penalty": 0.2,
		"openai_max_tokens": 4096,
		"stream_openai": true,
		"chat_completion_source": "openai",
		"max_context_unused": 0,
		"top_a": 0.5,
		"unknown_flag": 1
	}`
	preview, err := parseSillyTavernPreset(raw, "")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if preview.Format != "chat" {
		t.Fatalf("格式应为 chat，实际 %q", preview.Format)
	}
	if preview.Name != "聊天预设" {
		t.Fatalf("名称 = %q", preview.Name)
	}
	// Gemini 定向预设：top_k 是标准参数，必须进 body（旧口径会丢）
	if got := presetBodyNumber(t, preview, "top_k"); got != 40 {
		t.Fatalf("top_k 应写入 body，实际 %v", got)
	}
	if _, ok := preview.Body["system_prompt"]; ok {
		t.Fatalf("system_prompt 不应出现在 body 里: %v", preview.Body)
	}
	if got := presetBodyNumber(t, preview, "temperature"); got != 0.7 {
		t.Fatalf("temperature = %v", got)
	}
	if got := presetBodyNumber(t, preview, "max_tokens"); got != 4096 {
		t.Fatalf("max_tokens = %v", got)
	}
	if field, ok := findPresetMapped(preview, "max_tokens"); !ok || field.Source != "openai_max_tokens" {
		t.Fatalf("max_tokens 来源应为 openai_max_tokens: %+v", preview.Mapped)
	}
	// 语义冲突：chat_completion_source 归网关自管（preset 不出现 stream_openai 到 gateway 组）
	if drop, ok := findPresetDrop(preview, "chat_completion_source"); !ok || drop.Group != presetDropGroupGateway {
		t.Fatalf("chat_completion_source 应归入网关自管丢弃，实际 %+v", drop)
	}
	// 客户端行为组
	for _, key := range []string{"stream_openai", "max_context_unused"} {
		drop, ok := findPresetDrop(preview, key)
		if !ok || drop.Group != presetDropGroupClient {
			t.Fatalf("%s 应归入客户端行为丢弃，实际 %+v", key, drop)
		}
	}
	// 上游不通用组：原因必须可读（不是只给字段名）
	drop, ok := findPresetDrop(preview, "top_a")
	if !ok || drop.Group != presetDropGroupUpstream {
		t.Fatalf("top_a 应归入上游不支持丢弃，实际 %+v", drop)
	}
	if !strings.Contains(drop.Reason, "400") {
		t.Fatalf("top_a 丢弃原因应说明严格上游会 400，实际 %q", drop.Reason)
	}
	if drop, ok := findPresetDrop(preview, "unknown_flag"); !ok || drop.Group != presetDropGroupUnknown {
		t.Fatalf("未知字段应归入 unknown，实际 %+v", drop)
	}
}

func TestParseSillyTavernTextPreset(t *testing.T) {
	raw := `{"temp": 0.8, "top_p": 0.95, "max_length": 512, "rep_pen": 1.1, "top_k": 40, "sampler_order": [6,0,1]}`
	preview, err := parseSillyTavernPreset(raw, "")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if preview.Format != "text" {
		t.Fatalf("格式应为 text，实际 %q", preview.Format)
	}
	// temp → temperature
	if got := presetBodyNumber(t, preview, "temperature"); got != 0.8 {
		t.Fatalf("temperature = %v", got)
	}
	if field, ok := findPresetMapped(preview, "temperature"); !ok || field.Source != "temp" {
		t.Fatalf("temperature 来源应为 temp: %+v", preview.Mapped)
	}
	// max_length → max_tokens
	if got := presetBodyNumber(t, preview, "max_tokens"); got != 512 {
		t.Fatalf("max_tokens = %v", got)
	}
	if _, ok := preview.Body["top_k"]; !ok {
		t.Fatalf("top_k 是 Gemini 标准参数，应写入 body: %v", preview.Body)
	} else if got := presetBodyNumber(t, preview, "top_k"); got != 40 {
		t.Fatalf("top_k = %v", got)
	}
	if drop, ok := findPresetDrop(preview, "rep_pen"); !ok || drop.Group != presetDropGroupUpstream {
		t.Fatalf("rep_pen 应丢弃并说明原因，实际 %+v", drop)
	}
	if drop, ok := findPresetDrop(preview, "sampler_order"); !ok || drop.Group != presetDropGroupClient {
		t.Fatalf("sampler_order 应归入客户端行为丢弃，实际 %+v", drop)
	}
}

func TestParseSillyTavernAliasPrecedence(t *testing.T) {
	// 三来源取第一个非空：openai_max_tokens > max_tokens > max_length
	preview, err := parseSillyTavernPreset(`{"max_length": 100, "max_tokens": 200, "openai_max_tokens": 300}`, "")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got := presetBodyNumber(t, preview, "max_tokens"); got != 300 {
		t.Fatalf("应取 openai_max_tokens=300，实际 %v", got)
	}
	// 落选的两个不是"被丢弃"，只是没被采用
	for _, key := range []string{"max_length", "max_tokens"} {
		if drop, ok := findPresetDrop(preview, key); ok {
			t.Fatalf("%s 不该出现在丢弃列表（它是别名，不是不支持）: %+v", key, drop)
		}
	}
	// 只保留一条 mapped
	count := 0
	for _, item := range preview.Mapped {
		if item.Key == "max_tokens" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("max_tokens 应只有一条映射，实际 %d", count)
	}

	// 数值写成字符串也要吃
	preview2, err := parseSillyTavernPreset(`{"temp": "0.6", "freq_pen": "0.3"}`, "text")
	if err != nil {
		t.Fatalf("字符串数字解析失败: %v", err)
	}
	if got := presetBodyNumber(t, preview2, "temperature"); got != 0.6 {
		t.Fatalf("字符串 temperature = %v", got)
	}
	if got := presetBodyNumber(t, preview2, "frequency_penalty"); got != 0.3 {
		t.Fatalf("freq_pen 归一 = %v", got)
	}
}

func TestParseSillyTavernStopAndSeed(t *testing.T) {
	// stop 是字符串
	preview, err := parseSillyTavernPreset(`{"temperature": 0.5, "stop": "\nUser:"}`, "")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	stops, ok := preview.Body["stop"].([]string)
	if !ok || len(stops) != 1 || stops[0] != "\nUser:" {
		t.Fatalf("字符串 stop 应归一为单元素数组: %#v", preview.Body["stop"])
	}

	// stop 是数组 + seed/logit_bias 透传
	preview2, err := parseSillyTavernPreset(`{"temperature": 0.5, "stop": ["a","b"], "seed": 42, "logit_bias": {"50256": -100}}`, "")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	stops2, ok := preview2.Body["stop"].([]string)
	if !ok || len(stops2) != 2 {
		t.Fatalf("数组 stop 应保持两项: %#v", preview2.Body["stop"])
	}
	if seed, ok := preview2.Body["seed"].(int64); !ok || seed != 42 {
		t.Fatalf("seed = %#v", preview2.Body["seed"])
	}
	if _, ok := preview2.Body["logit_bias"].(map[string]any); !ok {
		t.Fatalf("logit_bias 应原样透传: %#v", preview2.Body["logit_bias"])
	}
}

func TestParseSillyTavernRejectsBadInput(t *testing.T) {
	if _, err := parseSillyTavernPreset("not json", ""); err == nil {
		t.Fatal("坏 JSON 应报错")
	}
	if _, err := parseSillyTavernPreset("[]", ""); err == nil {
		t.Fatal("顶层数组应报错")
	}
	if _, err := parseSillyTavernPreset("null", ""); err == nil {
		t.Fatal("顶层 null 应报错")
	}
	if _, err := parseSillyTavernPreset("   ", ""); err == nil {
		t.Fatal("空内容应报错")
	}
	// 超过 64KB 拒绝（别把整个酒馆 settings.json 当预设塞进来）
	big := `{"temperature": 0.5, "junk": "` + strings.Repeat("a", presetImportMaxBytes) + `"}`
	if _, err := parseSillyTavernPreset(big, ""); err == nil || !strings.Contains(err.Error(), "过大") {
		t.Fatalf("超大输入应被拒: %v", err)
	}
	// 空对象不崩：没有可映射字段，格式 unknown
	preview, err := parseSillyTavernPreset(`{}`, "")
	if err != nil {
		t.Fatalf("空对象不应报错: %v", err)
	}
	if len(preview.Body) != 0 || preview.Format != "unknown" {
		t.Fatalf("空对象结果 = %+v", preview)
	}
}

func TestPresetImportPreviewHandler(t *testing.T) {
	g := &Gateway{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/presets/import/preview",
		strings.NewReader(`{"format":"chat","json":"{\"temperature\":0.5,\"stream_openai\":true}"}`))
	g.handlePresetImportPreview(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("预览应 200，实际 %d %s", rec.Code, rec.Body.String())
	}
	var preview presetImportPreview
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatalf("响应不是预设预览: %v", err)
	}
	if preview.Body["temperature"] != 0.5 {
		t.Fatalf("预览 body = %v", preview.Body)
	}
	if _, ok := findPresetDrop(preview, "stream_openai"); !ok {
		t.Fatalf("预览应带丢弃项: %+v", preview.Dropped)
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/presets/import/preview",
		strings.NewReader(`{"json":"oops"}`))
	g.handlePresetImportPreview(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("坏输入应 400，实际 %d %s", rec2.Code, rec2.Body.String())
	}
}
