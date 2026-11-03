package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Base URL 少写 /v1 的客户端（Qoder / Cline / Roo 等）会把 /chat/completions
// 直接拼在主机名后面，必须命中而不是 404。
//
// 只查路由表不执行 handler：handler 依赖账号池，这里要断言的是"有没有挂上"。
func TestBareProtocolAliases(t *testing.T) {
	g := &Gateway{}
	g.routes()
	cases := []struct{ method, path string }{
		{http.MethodPost, "/chat/completions"},
		{http.MethodPost, "/responses"},
		{http.MethodPost, "/messages"},
		{http.MethodGet, "/models"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		if _, pattern := g.mux.Handler(req); pattern == "" {
			t.Errorf("%s %s 没有匹配到任何路由（返回 404）", c.method, c.path)
		}
	}
}
