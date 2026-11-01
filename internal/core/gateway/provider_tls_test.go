package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/core/transport"
	"aether-cockpit/internal/provider"
)

// transport.NewClientOpts：insecure=true 时能连自签证书服务器，false 时失败。
func TestNewClientOptsInsecure(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	f := &transport.Factory{DialTimeout: 3 * time.Second}
	strict, err := f.NewClientOpts(provider.NetPolicyDirectForce, "", "", false)
	if err != nil {
		t.Fatalf("NewClientOpts strict: %v", err)
	}
	if _, err := strict.Get(srv.URL); err == nil {
		t.Fatal("不跳过校验时应因自签证书失败")
	}
	lax, err := f.NewClientOpts(provider.NetPolicyDirectForce, "", "", true)
	if err != nil {
		t.Fatalf("NewClientOpts insecure: %v", err)
	}
	resp, err := lax.Get(srv.URL)
	if err != nil {
		t.Fatalf("跳过校验后应能连上: %v", err)
	}
	resp.Body.Close()
}

func clientInsecure(c *http.Client) bool {
	rt := unwrapHeaders(c.Transport)
	tr, ok := rt.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil {
		return false
	}
	return tr.TLSClientConfig.InsecureSkipVerify
}

// 渠道级 insecureTLS：保存 / 回显，并立刻重建账号客户端。
func TestProviderInsecureTLSSaveAndApply(t *testing.T) {
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
	})
	mustCreateAccount(t, g, "mock-native", "tls@example.com")
	env := g.pool.ListByProvider("mock-native")[0]
	if clientInsecure(env.HTTPClient) {
		t.Fatal("默认不应跳过证书校验")
	}

	if rec := saveHeaders(t, g, `{"id":"mock-native","insecureTLS":true}`); rec.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", rec.Code, rec.Body.String())
	}
	if v := providerViewOf(t, g, "mock-native")["insecureTLS"]; v != true {
		t.Fatalf("回显 insecureTLS = %v", v)
	}
	if !clientInsecure(env.HTTPClient) {
		t.Fatal("保存后应重建为跳过校验的客户端")
	}

	if rec := saveHeaders(t, g, `{"id":"mock-native","insecureTLS":false}`); rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d", rec.Code)
	}
	if clientInsecure(env.HTTPClient) {
		t.Fatal("取消后应恢复校验证书")
	}
}
