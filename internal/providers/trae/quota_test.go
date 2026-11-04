package trae

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

// quotaHostStub 把额度根地址换成本地测试服务器（生产代码按 region 拼域名）。
func quotaHostStub(t *testing.T, srv *httptest.Server) {
	t.Helper()
	oldHosts := traeQuotaHostsForTest
	traeQuotaHostsForTest = []string{srv.URL}
	t.Cleanup(func() { traeQuotaHostsForTest = oldHosts })
}

// 用量卡应读**套餐额度**（basic_usage_amount / basic_usage_limit），
// 而不是签到累计积分。这是本轮修复的核心：两者完全不是一回事。
func TestTraeQuotaReadsPlanUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/pay/ide_user_ent_usage"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"user_entitlement_pack_list":[
				{"entitlement_base_info":{"quota":{"basic_usage_limit":1000},"end_time":"2026-12-01T00:00:00Z"},
				 "usage":{"basic_usage_amount":300}},
				{"entitlement_base_info":{"quota":{"basic_usage_limit":500},"end_time":"2026-12-05T00:00:00Z"},
				 "usage":{"basic_usage_amount":100}}
			]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	quotaHostStub(t, srv)

	env := provider.NewAccountEnv("trae-quota", "trae-cn")
	env.Credentials = map[string]string{"token": "tok"}
	env.HTTPClient = srv.Client()

	q, err := newProvider("trae-cn", "CN").QueryQuota(context.Background(), env)
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	// 两个包合并：总量 1500、已用 400、剩余 1100。
	if q.Total != 1500 || q.Used != 400 || q.Remaining != 1100 {
		t.Fatalf("套餐额度换算错误：%+v", q)
	}
	if q.Balance != 1100 || q.BalanceUnit != "credits" {
		t.Fatalf("可用积分应等于剩余：%+v", q)
	}
	if len(q.Windows) != 1 || q.Windows[0].Used != 400*100.0/1500 {
		t.Fatalf("窗口占比错误：%+v", q.Windows)
	}
	// 重置时间取最早的那个包。
	if q.Until != 1796083200 {
		t.Fatalf("重置时间应取最早的包，得到 %d", q.Until)
	}
}

// 套餐接口读不到时回落到签到积分，且**必须**说明这是签到积分，不能冒充套餐额度。
func TestTraeQuotaFallsBackToCheckinWithNote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/status") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0, "enable": true, "checked_in": true,
			"credits": 42, "extra_credits": 8,
		})
	}))
	defer srv.Close()
	quotaHostStub(t, srv)

	old := traeCheckinURL
	traeCheckinURL = srv.URL + "/trae/api/v2/ug/checkin_credits"
	t.Cleanup(func() { traeCheckinURL = old })

	env := provider.NewAccountEnv("trae-quota", "trae-cn")
	env.Credentials = map[string]string{"token": "tok"}
	env.HTTPClient = srv.Client()

	q, err := newProvider("trae-cn", "CN").QueryQuota(context.Background(), env)
	if err != nil {
		t.Fatalf("应回落到签到积分：%v", err)
	}
	// 只取 credits（参考实现里没有 extra_credits，相加会放大数字）。
	if q.Balance != 42 || q.BalanceUnit != "credits" {
		t.Fatalf("q = %+v", q)
	}
	if !strings.Contains(q.Message, "签到积分") {
		t.Fatalf("必须标明这是签到积分：%q", q.Message)
	}
}

// 国际版现在也能读了（此前直接报"没有公开接口"）。
func TestTraeIntlHasReader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/pay/ide_user_ent_usage") {
			_, _ = w.Write([]byte(`{"code":0,"data":{"user_entitlement_pack_list":[
				{"entitlement_base_info":{"quota":{"basic_usage_limit":100}},"usage":{"basic_usage_amount":10}}]}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	quotaHostStub(t, srv)

	env := provider.NewAccountEnv("trae-intl", "trae-intl")
	env.Credentials = map[string]string{"token": "tok"}
	env.HTTPClient = srv.Client()

	q, err := newProvider("trae-intl", "SG").QueryQuota(context.Background(), env)
	if err != nil {
		t.Fatalf("国际版应能读额度：%v", err)
	}
	if q.Remaining != 90 {
		t.Fatalf("剩余应为 90，得到 %+v", q)
	}
}
