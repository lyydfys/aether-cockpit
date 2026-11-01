package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// fakeTrace 返回 Cloudflare trace 页面形状的响应。
func fakeTrace(t *testing.T, ip, loc string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "fl=123abc\nh=www.cloudflare.com\nip="+ip+"\nts=1700000000.000\nvisit_scheme=https\nuag=AetherCockpit\ncolo=SJC\nsliver=none\nhttp=http/2\nloc="+loc+"\ntls=TLSv1.3\nsni=plaintext\nwarp=off\ngateway=off\nrbi=off\nkex=X25519\n")
	}))
	t.Cleanup(srv.Close)
	old := proxyTraceURL
	proxyTraceURL = srv.URL
	t.Cleanup(func() { proxyTraceURL = old })
	return srv
}

type proxyCheckOut struct {
	Direct  *proxyLeg `json:"direct"`
	Through *proxyLeg `json:"through"`
}

func runProxyCheck(t *testing.T, g *Gateway) proxyCheckOut {
	t.Helper()
	rec := httptest.NewRecorder()
	g.handleProxyCheck(rec, httptest.NewRequest(http.MethodPost, "/api/proxy/check", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var out proxyCheckOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是 JSON: %v (%s)", err, rec.Body.String())
	}
	return out
}

func newProxyCheckGateway(t *testing.T) *Gateway {
	t.Helper()
	return New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
}

func TestProxyCheckDirectOnly(t *testing.T) {
	fakeTrace(t, "203.0.113.7", "SG")
	g := newProxyCheckGateway(t)

	out := runProxyCheck(t, g)
	if out.Direct == nil || out.Direct.IP != "203.0.113.7" || out.Direct.Loc != "SG" {
		t.Fatalf("direct = %#v", out.Direct)
	}
	if out.Direct.Error != "" {
		t.Fatalf("direct 不应有错误: %q", out.Direct.Error)
	}
	if out.Direct.Proxied {
		t.Fatal("direct 不应标记 proxied")
	}
	if out.Through != nil {
		t.Fatalf("没有代理时不应有 through: %#v", out.Through)
	}
}

func TestProxyCheckThroughFails(t *testing.T) {
	fakeTrace(t, "203.0.113.7", "SG")
	g := newProxyCheckGateway(t)
	// 关闭的端口：代理不可达，through 应如实报错而不是退回直连结果。
	if _, err := g.applyGlobalProxy("http://127.0.0.1:1"); err != nil {
		t.Fatalf("设置代理失败: %v", err)
	}

	out := runProxyCheck(t, g)
	if out.Through == nil {
		t.Fatal("有代理时应有 through")
	}
	if out.Through.IP != "" || out.Through.Error == "" {
		t.Fatalf("through 应报错: %#v", out.Through)
	}
	if !out.Through.Proxied {
		t.Fatal("through 应标记 proxied")
	}
	if out.Direct == nil || out.Direct.IP != "203.0.113.7" {
		t.Fatalf("direct 不应受代理影响: %#v", out.Direct)
	}
}

func TestProxyCheckDirectFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	dead := srv.URL
	srv.Close()
	old := proxyTraceURL
	proxyTraceURL = dead
	t.Cleanup(func() { proxyTraceURL = old })

	g := newProxyCheckGateway(t)
	out := runProxyCheck(t, g)
	if out.Direct == nil || out.Direct.Error == "" {
		t.Fatalf("直连失败应报错: %#v", out.Direct)
	}
}
