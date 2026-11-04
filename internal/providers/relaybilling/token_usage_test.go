package relaybilling

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// relay-panel 的额度接口是 `/api/usage/token/`（带斜杠）。少了斜杠会 404 ——
// 过去只试无斜杠版本，等于对所有 relay-panel 站点都读不到额度。
func TestTokenUsageTrailingSlash(t *testing.T) {
	var hitNoSlash bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/usage/token/":
			_, _ = w.Write([]byte(`{"total_available":1000000,"total_used":500000}`))
		case "/api/usage/token":
			hitNoSlash = true
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	env := panelEnv(t, srv)
	q, err := Query(t.Context(), env, srv.URL)
	if err != nil {
		t.Fatalf("应通过带斜杠的路径读到额度：%v", err)
	}
	// 1000000 / 500000 = $2
	if q.Balance != 2 {
		t.Fatalf("余额 = %v，应为 2", q.Balance)
	}
	_ = hitNoSlash
}

// relay-panel 在 allow_user_quota_query 打开时会顺带返回账号级余额，优先级更高。
func TestTokenUsagePrefersUserQuota(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/usage/token/" {
			_, _ = w.Write([]byte(`{"total_available":0,"total_used":0,"user_quota":2500000}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	env := panelEnv(t, srv)
	q, err := Query(t.Context(), env, srv.URL)
	if err != nil {
		t.Fatalf("应读到账号级额度：%v", err)
	}
	if q.Balance != 5 {
		t.Fatalf("2500000/500000 = 5，得到 %v", q.Balance)
	}
	if !contains(q.Message, "账号级") {
		t.Fatalf("文案应说明来源：%s", q.Message)
	}
}

// `/api/user/self` 未登录时返回 200 + success:false，不能当成"读到 0 余额"。
func TestPanelUnauthorizedBodyIsNotZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/user/self" {
			_, _ = w.Write([]byte(`{"success":false,"message":"无权进行此操作，未登录且未提供 access token"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	env := panelEnv(t, srv)
	if _, err := Query(t.Context(), env, srv.URL); err == nil {
		t.Fatal("未登录应报错，不能返回 0 余额")
	}
}
