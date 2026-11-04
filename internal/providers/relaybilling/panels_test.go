package relaybilling

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"aether-cockpit/internal/provider"
)

// panelEnv 造一个带模型 Key 的账号环境，指向测试服务器。
func panelEnv(t *testing.T, srv *httptest.Server) *provider.AccountEnv {
	t.Helper()
	env := provider.NewAccountEnv("acc-panel", "test-panel")
	env.Credentials["api_key"] = "sk-test"
	env.HTTPClient = srv.Client()
	return env
}

// relay-panel 面板：/api/status 给出自定义汇率，/api/user/self 返回额度单位。
// 关键断言：余额必须按 status 里的 quota_per_unit 折算，而不是写死 500000。
func TestPanelQuotaUsesStatusQuotaPerUnit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			// 这个站把 1 美元设成 1000000 单位（不是默认 500000）。
			_, _ = w.Write([]byte(`{"data":{"quota_per_unit":1000000,"display_in_currency":true}}`))
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota":2000000,"used_quota":1000000,"group":"vip"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("404 page not found"))
		}
	}))
	defer srv.Close()

	env := panelEnv(t, srv)
	q, err := Query(context.Background(), env, srv.URL+"/v1")
	if err != nil {
		t.Fatalf("应读到额度：%v", err)
	}
	if q.Balance != 2 {
		t.Fatalf("剩余应为 2000000/1000000 = $2，得到 %v（说明用了写死的 500000）", q.Balance)
	}
	if q.BalanceUnit != "USD" {
		t.Fatalf("单位应为 USD，得到 %q", q.BalanceUnit)
	}
	if q.Plan != "vip" {
		t.Fatalf("分组应读出 vip，得到 %q", q.Plan)
	}
}

// 没有 status 接口时按 relay-panel 默认汇率 500000 折算，并在文案里说明是默认值。
func TestPanelQuotaFallsBackToDefaultUnit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"data":{"quota":500000,"used_quota":0}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	env := panelEnv(t, srv)
	q, err := Query(context.Background(), env, srv.URL)
	if err != nil {
		t.Fatalf("应读到额度：%v", err)
	}
	if q.Balance != 1 {
		t.Fatalf("500000 单位按默认汇率应为 $1，得到 %v", q.Balance)
	}
}

// 面板接口要用户级令牌：模型 Key 被 401 拒时，错误要给出可执行的说明，
// 而不是把 404 或裸 401 丢给用户。
func TestPanelQuotaExplainsAccessTokenRequirement(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"message":"无权进行此操作，未登录且未提供 access token"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	env := panelEnv(t, srv)
	_, err := Query(context.Background(), env, srv.URL)
	if err == nil {
		t.Fatal("应报错")
	}
	msg := err.Error()
	if !contains(msg, "access token") {
		t.Fatalf("错误应提示需要 access token，得到：%s", msg)
	}
}

// 全站没有任何额度接口时，错误要说明"没有可读接口"，并带上最后一次失败原因。
func TestQueryNoInterfaceMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("404 page not found"))
	}))
	defer srv.Close()

	env := panelEnv(t, srv)
	_, err := Query(context.Background(), env, srv.URL)
	if err == nil {
		t.Fatal("应报错")
	}
	if !contains(err.Error(), "没有账单接口") {
		t.Fatalf("错误文案不对：%s", err.Error())
	}
}

// 页面回落到 HTML（很多站把未知路径交给前端路由）时要当成"不是 JSON"，不能 panic。
func TestQueryHTMLFallbackIsNotJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!DOCTYPE html><html><body>404</body></html>"))
	}))
	defer srv.Close()

	env := panelEnv(t, srv)
	if _, err := Query(context.Background(), env, srv.URL); err == nil {
		t.Fatal("HTML 回落应报错而不是当成额度 0")
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
