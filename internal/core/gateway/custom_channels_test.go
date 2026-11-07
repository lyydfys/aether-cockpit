package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/openaicompat"
)

// 自定义渠道表单里的附加请求头必须真的下发给 provider（此前只保存不生效，
// 需要额外头鉴权/选路的中转站会因此 401/403）。
func TestCustomChannelHeadersReachProvider(t *testing.T) {
	g := New(Config{SettingsPath: t.TempDir() + "/settings.json", UsagePath: "-"})
	defer provider.Unregister("custom-hdr-relay")

	body := `{"name":"Hdr Relay","base_url":"https://relay.example.com/v1","protocol":"openai",
		"models":["gpt-4o"],"headers":{"X-Tenant":"t1","X-Key-Echo":"{{api_key}}"}}`
	rec := httptest.NewRecorder()
	g.handleCustomChannelSave(rec, httptest.NewRequest(http.MethodPost, "/api/custom-channels", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("保存失败：%d %s", rec.Code, rec.Body.String())
	}

	p, ok := provider.Get("custom-hdr-relay")
	if !ok {
		t.Fatal("保存后应注册成 provider")
	}
	cp, ok := p.(*openaicompat.Provider)
	if !ok {
		t.Fatalf("provider 类型 = %T", p)
	}
	cfg := cp.Config()
	if cfg.Headers["X-Tenant"] != "t1" || cfg.Headers["X-Key-Echo"] != "{{api_key}}" {
		t.Fatalf("附加头未下发到 provider: %+v", cfg.Headers)
	}
}

// 自定义渠道保存后必须真的注册成 provider，并且模型名带渠道前缀。
func TestCustomChannelSaveRegistersProvider(t *testing.T) {
	g := New(Config{SettingsPath: t.TempDir() + "/settings.json", UsagePath: "-"})
	defer provider.Unregister("custom-my-relay")

	body := `{"name":"My Relay","base_url":"https://relay.example.com/v1","protocol":"openai","models":["gpt-4o","claude-3-5"]}`
	rec := httptest.NewRecorder()
	g.handleCustomChannelSave(rec, httptest.NewRequest(http.MethodPost, "/api/custom-channels", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("保存失败：%d %s", rec.Code, rec.Body.String())
	}

	m, ok := provider.Get("custom-my-relay")
	if !ok {
		t.Fatal("保存后应注册成 provider")
	}
	mf := m.Manifest()
	if mf.BaseURL != "https://relay.example.com/v1" {
		t.Fatalf("BaseURL = %q", mf.BaseURL)
	}
	if len(mf.Models) != 2 || mf.Models[0].ID != "custom-my-relay/gpt-4o" {
		t.Fatalf("模型前缀不对：%+v", mf.Models)
	}
	// 自定义渠道没有额度读取器：不能声明 Quota，否则页面会报"额度读取失败"。
	if mf.Capabilities.Quota {
		t.Fatal("自定义渠道不应声明额度能力")
	}
}

// 删除后要下线 provider，且不影响其它渠道。
func TestCustomChannelDeleteUnregisters(t *testing.T) {
	g := New(Config{SettingsPath: t.TempDir() + "/settings.json", UsagePath: "-"})
	save := func() {
		body := `{"name":"Temp","base_url":"https://tmp.example.com/v1"}`
		rec := httptest.NewRecorder()
		g.handleCustomChannelSave(rec, httptest.NewRequest(http.MethodPost, "/api/custom-channels", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("保存失败：%s", rec.Body.String())
		}
	}
	save()
	if _, ok := provider.Get("custom-temp"); !ok {
		t.Fatal("应已注册")
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/custom-channels/temp", nil)
	req.SetPathValue("id", "temp")
	rec := httptest.NewRecorder()
	g.handleCustomChannelDelete(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("删除失败：%d %s", rec.Code, rec.Body.String())
	}
	if _, ok := provider.Get("custom-temp"); ok {
		t.Fatal("删除后应下线 provider")
	}
}

// 非法输入必须被挡住：非 http(s) 地址、越权请求头、非法协议。
func TestCustomChannelValidation(t *testing.T) {
	g := New(Config{SettingsPath: t.TempDir() + "/settings.json", UsagePath: "-"})
	cases := []struct {
		name string
		body string
		want string
	}{
		{"空地址", `{"name":"x","base_url":""}`, "Base URL 不能为空"},
		{"协议不对", `{"name":"x","base_url":"file:///etc/passwd"}`, "http://"},
		{"覆盖鉴权头", `{"name":"x","base_url":"https://a.example.com/v1","headers":{"Authorization":"x"}}`, "由网关管理"},
		{"非法头名", `{"name":"x","base_url":"https://a.example.com/v1","headers":{"bad header":"x"}}`, "不合法"},
		{"未知协议", `{"name":"x","base_url":"https://a.example.com/v1","protocol":"grpc"}`, "协议只能是"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			g.handleCustomChannelSave(rec, httptest.NewRequest(http.MethodPost, "/api/custom-channels", strings.NewReader(tc.body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("应 400，得到 %d %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("错误文案应包含 %q，得到 %s", tc.want, rec.Body.String())
			}
		})
	}
}

// 同名保存是更新而不是新增；id 从名称生成。
func TestCustomChannelUpdateBySameName(t *testing.T) {
	g := New(Config{SettingsPath: t.TempDir() + "/settings.json", UsagePath: "-"})
	defer provider.Unregister("custom-abc")

	for _, models := range []string{`["a"]`, `["a","b"]`} {
		body := `{"name":"ABC","base_url":"https://abc.example.com/v1","models":` + models + `}`
		rec := httptest.NewRecorder()
		g.handleCustomChannelSave(rec, httptest.NewRequest(http.MethodPost, "/api/custom-channels", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("保存失败：%s", rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	g.handleCustomChannels(rec, httptest.NewRequest(http.MethodGet, "/api/custom-channels", nil))
	var out struct {
		Channels []CustomChannel `json:"channels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Channels) != 1 {
		t.Fatalf("同名保存应更新而不是新增，得到 %d 条", len(out.Channels))
	}
	if len(out.Channels[0].Models) != 2 {
		t.Fatalf("模型应被更新：%+v", out.Channels[0].Models)
	}
}

// probe 只接受 http(s)，并对 4xx 给出可读错误而不是抛 500。
func TestCustomChannelProbeValidation(t *testing.T) {
	g := New(Config{SettingsPath: t.TempDir() + "/settings.json", UsagePath: "-"})
	rec := httptest.NewRecorder()
	g.handleCustomChannelProbe(rec, httptest.NewRequest(http.MethodPost, "/api/custom-channels/probe",
		strings.NewReader(`{"base_url":"javascript:alert(1)"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法地址应 400，得到 %d", rec.Code)
	}
}
