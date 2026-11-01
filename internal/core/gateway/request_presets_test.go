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
