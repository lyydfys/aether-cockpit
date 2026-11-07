package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

// "获取模型"必须优先走渠道自己的模型目录实现（CodeBuddy → /v3/config），
// 而不是硬拼 /models —— CodeBuddy/WorkBuddy 站点上 /models 都是 404。
func TestProviderModelListUsesVendorLister(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"models":[
			{"id":"hy3","name":"HY3","supportsToolCall":true},
			{"id":"deepseek-v4.1-flash","name":"DeepSeek V4.1 Flash","supportsToolCall":true}
		]}}`))
	}))
	defer srv.Close()

	g := headersGateway(t)
	env := provider.NewAccountEnv("wb-list-vendor", "workbuddy-cn")
	env.Credentials = map[string]string{"token": "tk", "user_id": "uid", "base_url": srv.URL}
	env.HTTPClient = srv.Client()
	g.pool.Add(env)

	rec := httptest.NewRecorder()
	g.handleProviderModelList(rec, httptest.NewRequest(http.MethodPost, "/api/provider/models",
		strings.NewReader(`{"id":"workbuddy-cn"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if gotPath != "/v3/config" {
		t.Fatalf("应请求 /v3/config，实际 %q", gotPath)
	}
	var out struct {
		Models []string `json:"models"`
		Source string   `json:"source"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Source != "vendor" || len(out.Models) != 2 || out.Models[0] != "hy3" {
		t.Fatalf("模型清单 = %+v", out)
	}
}

// 上游目录拿不到（没账号/token 过期/站点没有 /models）时退回内置清单，
// 不能让"登录成功但获取模型"只弹一个 404。
func TestProviderModelListRegistryFallback(t *testing.T) {
	g := headersGateway(t)
	rec := httptest.NewRecorder()
	g.handleProviderModelList(rec, httptest.NewRequest(http.MethodPost, "/api/provider/models",
		strings.NewReader(`{"id":"workbuddy-intl"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Models      []string `json:"models"`
		Source      string   `json:"source"`
		VendorError string   `json:"vendor_error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Source != "registry" || len(out.Models) == 0 {
		t.Fatalf("应退回 registry，got %+v", out)
	}
	if out.VendorError == "" {
		t.Fatal("退回内置清单时应带上 vendor_error 说明原因")
	}
	// 内置清单用上游模型名（不带渠道前缀）。
	found := false
	for _, m := range out.Models {
		if m == "deepseek-v4.1-flash" {
			found = true
		}
	}
	if !found {
		t.Fatalf("registry 清单 = %v", out.Models)
	}
}
