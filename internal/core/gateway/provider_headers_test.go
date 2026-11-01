package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func headersGateway(t *testing.T) *Gateway {
	t.Helper()
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
	})
	mustCreateAccount(t, g, "mock-native", "hdr@example.com")
	return g
}

func saveHeaders(t *testing.T, g *Gateway, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	g.handleProviderSave(rec, httptest.NewRequest(http.MethodPost, "/api/provider/save", strings.NewReader(body)))
	return rec
}

// 渠道自定义头的保存 / 回显 / 掩码 / 掩码回传保留旧值 / 清空。
func TestProviderHeadersSaveEchoMask(t *testing.T) {
	g := headersGateway(t)
	rec := saveHeaders(t, g, `{"id":"mock-native","headers":{"X-Org":"acme","Authorization":"Bearer secret-token-1234"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", rec.Code, rec.Body.String())
	}
	view := providerViewOf(t, g, "mock-native")
	hs, _ := view["headers"].(map[string]any)
	if hs["X-Org"] != "acme" {
		t.Fatalf("普通头应原样回显: %+v", hs)
	}
	if v, _ := hs["Authorization"].(string); !strings.HasPrefix(v, "••••") {
		t.Fatalf("敏感头应打码: %q", v)
	}

	// 把打码值原样提交回去：应保留旧值，而不是把 •••• 存成值。
	masked := hs["Authorization"].(string)
	rec = saveHeaders(t, g, `{"id":"mock-native","headers":{"X-Org":"acme","Authorization":"`+masked+`"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := g.providerHeaders("mock-native")["Authorization"]; got != "Bearer secret-token-1234" {
		t.Fatalf("掩码回传应保留旧值，got %q", got)
	}

	// 清空。
	rec = saveHeaders(t, g, `{"id":"mock-native","headers":{}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d", rec.Code)
	}
	if len(g.providerHeaders("mock-native")) != 0 {
		t.Fatal("清空后不应残留请求头")
	}
}

// 保存后的头会套到账号的 http.Client 上，真实发到上游；请求自身设置的同名头优先。
func TestProviderHeadersReachUpstream(t *testing.T) {
	var seen http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	g := headersGateway(t)
	if rec := saveHeaders(t, g, `{"id":"mock-native","headers":{"X-Org":"acme","X-Build":"42"}}`); rec.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", rec.Code, rec.Body.String())
	}
	env := g.pool.ListByProvider("mock-native")[0]
	env.SetHTTPClient(srv.Client())
	g.applyProviderTransport(env)

	resp, err := env.HTTPClient.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if seen.Get("X-Org") != "acme" || seen.Get("X-Build") != "42" {
		t.Fatalf("上游没收到自定义头: %+v", seen)
	}

	// 请求显式带的同名头不被覆盖。
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("X-Org", "other")
	resp, err = env.HTTPClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if seen.Get("X-Org") != "other" {
		t.Fatalf("请求自己的头应保留，got %q", seen.Get("X-Org"))
	}
}

// 非法头名 / 受 HTTP 客户端管理的头必须明确拒绝。
func TestProviderHeadersRejectsInvalid(t *testing.T) {
	g := headersGateway(t)
	for _, body := range []string{
		`{"id":"mock-native","headers":{"Bad Name":"v"}}`,
		`{"id":"mock-native","headers":{"Host":"evil.example"}}`,
		`{"id":"mock-native","headers":{"X-Test":"a\nb"}}`,
	} {
		rec := saveHeaders(t, g, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s 应 400，got %d：%s", body, rec.Code, rec.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out["error"] == "" {
			t.Fatalf("400 应带原因: %s", rec.Body.String())
		}
	}
}
