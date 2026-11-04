package zcode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

// testJWT 是形态合法的 ZCode OAuth JWT（三段、首段 eyJ）。
// QueryQuota 只认这种形态，API Key 会被当成"该模式不支持"。
const testJWT = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.c2lnbmF0dXJl"

// quotaEnv 造一个指向 httptest 的账号环境。
func quotaEnv(srv *httptest.Server, creds map[string]string) *provider.AccountEnv {
	env := provider.NewAccountEnv("zcode-quota", "zcode")
	env.Credentials = creds
	env.HTTPClient = srv.Client()
	return env
}

// useQuotaServer 把额度端点指向测试服务器（包级变量是函数级的，测试会还原）。
func useQuotaServer(t *testing.T, srv *httptest.Server) {
	t.Helper()
	old := zcodeBalanceURL
	zcodeBalanceURL = srv.URL + "/api/v1/zcode-plan/billing/balance"
	t.Cleanup(func() { zcodeBalanceURL = old })
}

// ① 正常：两条 balance 求和；remaining_units 优先、available_units 次之；
// 重置时间取最小；请求带 Bearer 与 app_version。
func TestQuotaSumsBalances(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+testJWT {
			t.Errorf("Authorization = %q", got)
		}
		if r.Header.Get("x-zcode-app-version") == "" {
			t.Error("缺少 x-zcode-app-version 来源头")
		}
		if r.URL.Query().Get("app_version") == "" {
			t.Error("缺少 app_version 查询参数")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"plans": []any{
					map[string]any{"name": "旧套餐", "status": "expired"},
					map[string]any{"name": "Coding Plan Pro", "status": "active"},
				},
				"balances": []any{
					// remaining_units 优先：120 剩余
					map[string]any{
						"total_units": "200", "used_units": 80, "remaining_units": 120,
						"period_end": 1790000000,
					},
					// 只有 available_units：60 剩余；周期结束更早
					map[string]any{
						"total_units": 100, "used_units": "40", "available_units": 60,
						"expires_at": 1780000000,
					},
				},
			},
		})
	}))
	defer srv.Close()
	useQuotaServer(t, srv)

	q, err := newProvider("zcode", "intl", "https://api.z.ai/api/coding/paas/v4", "Z.ai").
		QueryQuota(context.Background(), quotaEnv(srv, map[string]string{"zcode_jwt": testJWT}))
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if q.Total != 300 || q.Used != 120 || q.Remaining != 180 {
		t.Fatalf("total/used/remaining = %d/%d/%d", q.Total, q.Used, q.Remaining)
	}
	if q.Unit != "credits" || q.BalanceUnit != "credits" || q.Balance != 180 {
		t.Fatalf("unit/balance = %q/%q/%.1f", q.Unit, q.BalanceUnit, q.Balance)
	}
	if q.Plan != "Coding Plan Pro" {
		t.Fatalf("plan = %q（应跳过 expired 的计划）", q.Plan)
	}
	if q.ExpiresAt != 1780000000 || len(q.Windows) != 1 {
		t.Fatalf("expiresAt = %d windows = %+v", q.ExpiresAt, q.Windows)
	}
	win := q.Windows[0]
	if win.Name != "Credits" || win.Limit != 300 || win.Amount != 120 ||
		win.Used < 39.9 || win.Used > 40.1 || win.Unit != "credits" || win.ResetsAt != 1780000000 {
		t.Fatalf("window = %+v", win)
	}
}

// ② 业务码非 0：用 msg 报错，且不是可重试错误。
func TestQuotaBusinessCodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 40301, "msg": "该 Key 未订阅 Coding Plan"})
	}))
	defer srv.Close()
	useQuotaServer(t, srv)

	_, err := newProvider("zcode", "intl", "https://api.z.ai/api/coding/paas/v4", "Z.ai").
		QueryQuota(context.Background(), quotaEnv(srv, map[string]string{"zcode_jwt": testJWT}))
	if err == nil {
		t.Fatal("业务码非 0 应返回错误")
	}
	if !strings.Contains(err.Error(), "40301") || !strings.Contains(err.Error(), "未订阅") {
		t.Fatalf("错误信息未带业务码/msg：%v", err)
	}
}

// ③ API Key 模式：不查询、不报错（返回 nil, nil），服务端不应收到请求。
func TestQuotaAPIKeyModeSkipsRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	useQuotaServer(t, srv)

	q, err := newProvider("zcode-cn", "cn", "https://open.bigmodel.cn/api/coding/paas/v4", "BigModel").
		QueryQuota(context.Background(), quotaEnv(srv, map[string]string{"api_key": "sk-live-abc"}))
	if err != nil {
		t.Fatalf("API Key 模式不应报错：%v", err)
	}
	if q != nil {
		t.Fatalf("API Key 模式应返回 nil，得到 %+v", q)
	}
	if called {
		t.Fatal("API Key 模式不应发起额度请求")
	}
}

// ④ 空 balances：给"可能未订阅"的友好文案，而不是空指针或 0。
func TestQuotaEmptyBalancesMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{"plans": []any{}, "balances": []any{}},
		})
	}))
	defer srv.Close()
	useQuotaServer(t, srv)

	_, err := newProvider("zcode", "intl", "https://api.z.ai/api/coding/paas/v4", "Z.ai").
		QueryQuota(context.Background(), quotaEnv(srv, map[string]string{"zcode_jwt": testJWT}))
	if err == nil {
		t.Fatal("空 balances 应返回错误")
	}
	msg := err.Error()
	if !strings.Contains(msg, "balances") || !strings.Contains(msg, "Coding Plan") {
		t.Fatalf("空 balances 文案不够友好：%v", msg)
	}
}

// ⑤ 401 与 404 的文案必须不同（一个要重新登录，一个是版本/站点没有接口）。
func TestQuota401And404MessagesDiffer(t *testing.T) {
	query := func(status int) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		defer srv.Close()
		useQuotaServer(t, srv)
		_, err := newProvider("zcode", "intl", "https://api.z.ai/api/coding/paas/v4", "Z.ai").
			QueryQuota(context.Background(), quotaEnv(srv, map[string]string{"zcode_jwt": testJWT}))
		if err == nil {
			t.Fatalf("%d 应返回错误", status)
		}
		return err.Error()
	}

	unauthorized := query(http.StatusUnauthorized)
	notFound := query(http.StatusNotFound)
	if unauthorized == notFound {
		t.Fatalf("401 与 404 文案相同：%q", unauthorized)
	}
	if !strings.Contains(unauthorized, "401") || !strings.Contains(unauthorized, "重新登录") {
		t.Fatalf("401 文案不对：%q", unauthorized)
	}
	if !strings.Contains(notFound, "404") || !strings.Contains(notFound, "额度接口") {
		t.Fatalf("404 文案不对：%q", notFound)
	}
}

// ⑥ 清单必须声明额度能力，否则界面不会去读（reverse 只用 QuotaKind 推这个位）。
func TestQuotaCapabilityDeclared(t *testing.T) {
	p := newProvider("zcode", "intl", "https://api.z.ai/api/coding/paas/v4", "Z.ai")
	if _, ok := any(p).(provider.QuotaCapability); !ok {
		t.Fatal("*Provider 未实现 QuotaCapability")
	}
	if !p.Manifest().Capabilities.Quota {
		t.Fatal("Manifest 未声明 Capabilities.Quota")
	}
}
