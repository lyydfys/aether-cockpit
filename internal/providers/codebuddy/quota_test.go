package codebuddy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

var _ provider.QuotaCapability = (*Provider)(nil)

func cbQuotaEnv(srv *httptest.Server) *provider.AccountEnv {
	env := provider.NewAccountEnv("cb-quota", "codebuddy-cn")
	env.Credentials = map[string]string{
		"token": "tok", "user_id": "u1", "base_url": srv.URL,
	}
	env.HTTPClient = srv.Client()
	return env
}

// 本周期额度（Cycle*）优先于购买总量（Capacity*）：后者跨周期，用它算会让
// 用户看到"还剩一大截"，实际本周期已经见底。
func TestCodeBuddyQuotaPrefersCycle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, cbMeterResourcePath) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"Response":{"Data":{"Accounts":[{
			"CapacitySize":10000,"CapacityUsed":9000,
			"CycleCapacitySize":1000,"CycleCapacityUsed":800,
			"ProductName":"CodeBuddy Pro"
		}]}}}}`))
	}))
	defer srv.Close()

	q, err := newProvider(cnProfile).QueryQuota(context.Background(), cbQuotaEnv(srv))
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if q == nil || len(q.Windows) == 0 {
		t.Fatalf("q = %+v", q)
	}
	w := q.Windows[0]
	if w.Limit != 1000 || w.Amount != 800 {
		t.Fatalf("应使用本周期额度 1000/800，得到 %+v", w)
	}
	if q.Balance != 200 {
		t.Fatalf("剩余应为 200，得到 %v", q.Balance)
	}
}

// 企业成员账号的 get-user-resource 会返回空 Accounts，必须回落到企业用量接口，
// 否则企业账号永远显示成"没有额度"。
func TestCodeBuddyQuotaEnterpriseFallback(t *testing.T) {
	var enterpriseHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, cbMeterResourcePath):
			_, _ = w.Write([]byte(`{"data":{"data":{"Accounts":[]}}}`))
		case strings.HasSuffix(r.URL.Path, cbMeterEnterprisePath):
			enterpriseHit = true
			_, _ = w.Write([]byte(`{"data":{"limitNum":1000,"credit":400}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	q, err := newProvider(cnProfile).QueryQuota(context.Background(), cbQuotaEnv(srv))
	if err != nil {
		t.Fatalf("应回落到企业接口：%v", err)
	}
	if !enterpriseHit {
		t.Fatal("企业接口没有被调用")
	}
	if q.Balance != 600 {
		t.Fatalf("limitNum 1000 - credit 400 = 600，得到 %v", q.Balance)
	}
}

// 额度用光是合法读数，不能当成"没读到"。
func TestCodeBuddyQuotaExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, cbMeterResourcePath) {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"Response":{"Data":{"Accounts":[{
			"CycleCapacitySize":500,"CycleCapacityUsed":500
		}]}}}}`))
	}))
	defer srv.Close()

	q, err := newProvider(cnProfile).QueryQuota(context.Background(), cbQuotaEnv(srv))
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if q.Balance != 0 || len(q.Windows) != 1 || q.Windows[0].Used != 100 {
		t.Fatalf("用光时应为 0 且 100%%，得到 %+v", q)
	}
}

// 401 与 404 的错误文案不同：前者是登录态问题，后者是接口不存在。
func TestCodeBuddyQuotaErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, "登录"},
		{http.StatusNotFound, "额度接口"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
		}))
		q, err := newProvider(cnProfile).QueryQuota(context.Background(), cbQuotaEnv(srv))
		srv.Close()
		if err == nil {
			t.Fatalf("HTTP %d 应报错（q=%+v）", tc.status, q)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("HTTP %d 错误应包含 %q，得到 %v", tc.status, tc.want, err)
		}
	}
}
