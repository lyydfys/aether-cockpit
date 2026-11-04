package minimax

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"aether-cockpit/internal/provider"
)

func minimaxEnvFor(srv *httptest.Server) *provider.AccountEnv {
	env := provider.NewAccountEnv("mm-quota", "minimax")
	env.Credentials = map[string]string{"access_token": "tok", "user_id": "u1"}
	env.HTTPClient = srv.Client()
	return env
}

// 只有计数、没有百分比时，要按「剩余/总量」反算。
//
// 关键陷阱：`current_interval_usage_count` 字面看像"已用"，实际语义是
// **剩余**。写反的话"还剩 90%"会显示成"已用 90%"，额度看起来瞬间见底。
func TestPlanWindowsFromCounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/api/openplatform/coding_plan/remains" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"model_remains":[{
			"model_name":"general",
			"current_interval_status":1,
			"current_interval_total_count":100,
			"current_interval_usage_count":90,
			"start_time":1800000000000,
			"end_time":1800003600000,
			"current_weekly_status":1,
			"current_weekly_total_count":200,
			"current_weekly_usage_count":50,
			"weekly_start_time":1800000000000,
			"weekly_end_time":1800604800000
		}]}`))
	}))
	defer srv.Close()

	p := newProvider(profile{platformBase: srv.URL})
	wins := p.planWindows(t.Context(), minimaxEnvFor(srv), "tok", "")
	if len(wins) != 2 {
		t.Fatalf("应有 5h 与 weekly 两个窗口：%+v", wins)
	}
	// 5h：剩余 90/100 = 90% → 已用 10%
	if wins[0].Used < 9.9 || wins[0].Used > 10.1 {
		t.Fatalf("5h 已用应为 10%%，得到 %.2f", wins[0].Used)
	}
	// weekly：剩余 50/200 = 25% → 已用 75%
	if wins[1].Used < 74.9 || wins[1].Used > 75.1 {
		t.Fatalf("weekly 已用应为 75%%，得到 %.2f", wins[1].Used)
	}
}

// 有百分比时以百分比为准（计数只作为兜底）。
func TestPlanWindowsPrefersPercent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"model_remains":[{
			"model_name":"general",
			"current_interval_status":1,
			"current_interval_remaining_percent":80,
			"current_interval_total_count":100,
			"current_interval_usage_count":10,
			"start_time":1800000000000,"end_time":1800003600000
		}]}`))
	}))
	defer srv.Close()

	p := newProvider(profile{platformBase: srv.URL})
	wins := p.planWindows(t.Context(), minimaxEnvFor(srv), "tok", "")
	if len(wins) == 0 {
		t.Fatal("应至少有一个窗口")
	}
	if wins[0].Used < 19.9 || wins[0].Used > 20.1 {
		t.Fatalf("应使用 80%% 剩余（已用 20%%），得到 %.2f", wins[0].Used)
	}
}
