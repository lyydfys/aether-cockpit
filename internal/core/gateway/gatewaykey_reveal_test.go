package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// 本机可以查看网关密钥明文，方便"看一眼再复制"。
//
// 以前的模式是"生成时回显一次、之后永不可见"，用户过几天想填到另一台设备，
// 只能重新轮换（顺带把在用的客户端全踢下线）。
func TestLocalCanRevealGatewayKey(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
		APIKey:           "sk-aether-abcdef0123456789abcdef0123456789",
	})
	srv := httptest.NewServer(g)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/settings/gateway-key/reveal")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("本机应能查看明文，得到 %d", resp.StatusCode)
	}
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "sk-aether-abcdef0123456789abcdef0123456789") {
		t.Fatalf("响应应含明文，得到 %q", string(buf[:n]))
	}
}

// 局域网 / 公网访问绝不能拿到明文——那等于把门锁交出去。
func TestRemoteCannotRevealGatewayKey(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
		APIKey:           "sk-aether-abcdef0123456789abcdef0123456789",
	})
	srv := httptest.NewServer(g)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/settings/gateway-key/reveal", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	// 模拟来自局域网：Non-loopback 源地址 + 转发头。
	req.RemoteAddr = "192.168.1.50:12345"
	req.Header.Set("X-Forwarded-For", "192.168.1.50")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("非本机应被拒绝（403），得到 %d", resp.StatusCode)
	}
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	if strings.Contains(string(buf[:n]), "sk-aether-abcdef") {
		t.Fatal("拒绝响应里泄露了明文")
	}
}

// 没设置密钥时如实返回 404，而不是空串成功。
func TestRevealWithoutKeyReturns404(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	srv := httptest.NewServer(g)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/settings/gateway-key/reveal")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("没有密钥时应返回 404，得到 %d", resp.StatusCode)
	}
}

// 生成后用 reveal 拿到的必须与生成时一致（也就是"看得见就能复制"）。
func TestRevealMatchesGeneratedKey(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	srv := httptest.NewServer(g)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/settings/gateway-key", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	_ = resp.Body.Close()
	generated := jsonStringField(t, string(buf[:n]), "api_key")
	if generated == "" {
		t.Fatalf("生成响应里没有 api_key：%s", string(buf[:n]))
	}

	rev, err := http.Get(srv.URL + "/api/settings/gateway-key/reveal")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer rev.Body.Close()
	buf2 := make([]byte, 512)
	n2, _ := rev.Body.Read(buf2)
	revealed := jsonStringField(t, string(buf2[:n2]), "api_key")
	if revealed != generated {
		t.Fatalf("查看得到的密钥与生成的不一致：generated=%q revealed=%q", generated, revealed)
	}
}

// jsonStringField 极简取 JSON 里的字符串字段（测试用，避免引额外依赖）。
func jsonStringField(t *testing.T, body, field string) string {
	t.Helper()
	key := `"` + field + `":"`
	i := strings.Index(body, key)
	if i < 0 {
		return ""
	}
	rest := body[i+len(key):]
	if j := strings.Index(rest, `"`); j >= 0 {
		return rest[:j]
	}
	return ""
}
