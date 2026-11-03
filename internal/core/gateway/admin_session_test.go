package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestMobileAdminSessionRequired(t *testing.T) {
	const key = "test-admin-session-key"
	g := New(Config{
		SettingsPath:    filepath.Join(t.TempDir(), "settings.json"),
		UsagePath:       filepath.Join(t.TempDir(), "usage.json"),
		AdminSessionKey: key,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = g.Close(ctx)
	})
	srv := httptest.NewServer(g)
	defer srv.Close()

	do := func(method, path, authorization string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+path, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}
	requireStatus := func(resp *http.Response, want int) []byte {
		t.Helper()
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("status = %d want %d; body=%s", resp.StatusCode, want, body)
		}
		return body
	}

	for _, tc := range []struct {
		name   string
		path   string
		header string
	}{
		{"missing", "/api/providers", ""},
		{"wrong", "/api/providers", "Bearer wrong-key"},
		{"query-is-not-auth", "/api/providers?key=" + key, ""},
		{"basic-is-not-bearer", "/api/providers", "Basic " + key},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := requireStatus(do(http.MethodGet, tc.path, tc.header), http.StatusUnauthorized)
			var payload struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(body, &payload); err != nil || payload.Code != "admin_auth_required" {
				t.Fatalf("body=%s err=%v", body, err)
			}
		})
	}

	body := requireStatus(do(http.MethodGet, "/api/providers", "Bearer "+key), http.StatusOK)
	if len(body) == 0 || body[0] != '{' {
		t.Fatalf("providers body = %s", body)
	}

	requireStatus(do(http.MethodGet, "/api/health", ""), http.StatusOK)
	requireStatus(do(http.MethodOptions, "/api/providers", ""), http.StatusNoContent)

	// 模型协议使用独立的 caller key（下一阶段），不能被管理会话凭据误伤。
	requireStatus(do(http.MethodGet, "/v1/models", ""), http.StatusOK)

	// 管理面不只在 /api 下：MCP 中继、指标与版本同样携带账号/凭据语义。
	for _, path := range []string{"/mcp", "/metrics", "/readyz", "/version"} {
		requireStatus(do(http.MethodGet, path, ""), http.StatusUnauthorized)
	}
	requireStatus(do(http.MethodGet, "/metrics", "Bearer "+key), http.StatusOK)
	requireStatus(do(http.MethodGet, "/healthz", ""), http.StatusOK)
}
