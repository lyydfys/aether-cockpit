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

func TestMobileCallerKeyRequired(t *testing.T) {
	const (
		adminKey  = "test-admin-session-key"
		callerKey = "test-caller-key-secret"
	)
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		UsagePath:        filepath.Join(t.TempDir(), "usage.json"),
		AdminSessionKey:  adminKey,
		RequireCallerKey: true,
		CallerKey:        callerKey,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = g.Close(ctx)
	})
	srv := httptest.NewServer(g)
	defer srv.Close()

	do := func(path string, mutate func(*http.Request)) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		if mutate != nil {
			mutate(req)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		return resp
	}
	bearer := func(key string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+key) }
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
	requireCallerLocked := func(resp *http.Response) {
		t.Helper()
		body := requireStatus(resp, http.StatusUnauthorized)
		var payload struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(body, &payload); err != nil || payload.Code != "caller_auth_required" {
			t.Fatalf("body=%s err=%v", body, err)
		}
	}

	requireCallerLocked(do("/v1/models", nil))
	requireCallerLocked(do("/v1/models", bearer(adminKey)))
	requireCallerLocked(do("/v1/models?key="+callerKey, nil))

	body := requireStatus(do("/v1/models", bearer(callerKey)), http.StatusOK)
	var list struct {
		Object string `json:"object"`
	}
	if err := json.Unmarshal(body, &list); err != nil || list.Object != "list" {
		t.Fatalf("/v1/models = %s err=%v", body, err)
	}

	// Anthropic/Gemini 风格的 x-api-key 同样接受，但 query 永不接受。
	requireStatus(do("/v1/models", func(r *http.Request) {
		r.Header.Set("x-api-key", callerKey)
	}), http.StatusOK)

	// caller key 不能打开管理面；管理 key 也不能替代 caller key。
	body = requireStatus(do("/api/providers", bearer(callerKey)), http.StatusUnauthorized)
	var guard struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &guard); err != nil || guard.Code != "admin_auth_required" {
		t.Fatalf("management guard = %s err=%v", body, err)
	}
	requireStatus(do("/api/providers", bearer(adminKey)), http.StatusOK)

	// CORS 预检仍然匿名。
	requireStatus(do("/v1/models", func(r *http.Request) {
		r.Method = http.MethodOptions
	}), http.StatusNoContent)
}
