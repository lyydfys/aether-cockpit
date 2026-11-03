package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestModelRedirectResolution(t *testing.T) {
	g := catalogGateway(t, t.TempDir())
	if err := g.setModelRedirects(map[string]map[string]string{
		"my-virtual": {
			"mock-weak":   "weak-upstream",
			"mock-native": "native-upstream",
		},
	}); err != nil {
		t.Fatalf("set redirects: %v", err)
	}

	bindings := g.models.Resolve("my-virtual")
	got := map[string]string{}
	for _, b := range bindings {
		got[b.ProviderID] = b.UpstreamModel
	}
	if got["mock-native"] != "native-upstream" || got["mock-weak"] != "weak-upstream" {
		t.Fatalf("redirect bindings = %+v", bindings)
	}

	// 显式 provider/model 仍然直达，不被虚拟名覆盖。
	if exact := g.models.Resolve("mock-native/echo-pro"); len(exact) != 1 || exact[0].UpstreamModel != "echo-pro" {
		t.Fatalf("exact = %+v", exact)
	}
}

// 重定向右边可以写公开显示名，路由层要折成渠道真正的上游 key。
func TestModelRedirectResolvesDisplayNameToUpstream(t *testing.T) {
	g := catalogGateway(t, t.TempDir())
	if err := g.setModelRedirects(map[string]map[string]string{
		"echo-alias": {"mock-native": "Echo Pro"},
	}); err != nil {
		t.Fatal(err)
	}
	bindings := g.models.Resolve("echo-alias")
	if len(bindings) != 1 || bindings[0].UpstreamModel != "echo-pro" {
		t.Fatalf("display name 未折成上游名: %+v", bindings)
	}
}

func TestModelRedirectDoesNotAffectVersionSiblings(t *testing.T) {
	g := catalogGateway(t, t.TempDir())
	if err := g.setModelRedirects(map[string]map[string]string{
		"deepseek-v4.1-flash": {"mock-native": "echo-pro"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, b := range g.models.Resolve("deepseek-v4-flash") {
		if b.ProviderID == "mock-native" {
			t.Fatalf("V4.1 的重定向不应影响 V4 Flash: %+v", b)
		}
	}
}

func TestModelRedirectSaveViewAndExposure(t *testing.T) {
	g := catalogGateway(t, t.TempDir())
	g.Pool().Add(provider.NewAccountEnv("acc-redirect", "mock-native"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/model-redirects",
		strings.NewReader(`{"redirects":{"deepseek-v4.1-flash":{"mock-native":"echo-pro"}}}`))
	g.handleSaveModelRedirects(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !g.models.HasRedirect("DeepSeek-V4.1-Flash") {
		t.Fatal("重定向未进入路由表（应大小写归一）")
	}

	// GET 回显
	rec = httptest.NewRecorder()
	g.handleModelRedirects(rec, httptest.NewRequest(http.MethodGet, "/api/model-redirects", nil))
	var got struct {
		Redirects map[string]map[string]string `json:"redirects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Redirects["deepseek-v4.1-flash"]["mock-native"] != "echo-pro" {
		t.Fatalf("GET redirects = %+v", got.Redirects)
	}

	// /v1/models 暴露虚拟名
	rec = httptest.NewRecorder()
	g.handleModels(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	var list struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range list.Data {
		if m.ID == "deepseek-v4.1-flash" && m.OwnedBy == "aether-cockpit/redirect" {
			found = true
		}
	}
	if !found {
		t.Fatalf("/v1/models 缺少重定向虚拟名: %+v", list.Data)
	}

	// Agent 选择器也包含虚拟名
	found = false
	for _, o := range g.modelOptions() {
		if o.Value == "deepseek-v4.1-flash" && o.Group == "模型重定向" {
			found = true
		}
	}
	if !found {
		t.Fatal("modelOptions 缺少重定向虚拟名")
	}
}

func TestModelRedirectValidation(t *testing.T) {
	g := catalogGateway(t, t.TempDir())
	cases := map[string]map[string]map[string]string{
		"空别名":    {"": {"mock-native": "x"}},
		"别名带斜杠":  {"a/b": {"mock-native": "x"}},
		"未知渠道":   {"ok": {"no-such-provider": "x"}},
		"空上游模型":  {"ok": {"mock-native": " "}},
		"归一化后重复": {"DeepSeek-V4.1-Flash": {"mock-native": "x"}, "deepseek-v4.1-flash": {"mock-native": "y"}},
		"空目标":    {"ok": {}},
	}
	for name, in := range cases {
		if err := g.setModelRedirects(in); err == nil {
			t.Errorf("%s：应拒绝", name)
		}
	}
}
