package claude

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func TestParseExpiry(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want time.Time
	}{
		{"RFC3339", "2030-01-02T03:04:05Z", time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)},
		{"Unix 秒", "1893456000", time.Unix(1893456000, 0)},
		{"Unix 毫秒（Claude Code 原生格式）", "1893456000000", time.Unix(1893456000, 0)},
		{"空", "", time.Time{}},
		{"垃圾", "not-a-time", time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseExpiry(tc.raw)
			if !got.Equal(tc.want) {
				t.Fatalf("parseExpiry(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestRefreshCredential 验证刷新请求的 JSON 契约与凭证写回。
func TestRefreshCredential(t *testing.T) {
	var gotBody map[string]any
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"sk-ant-oat01-new","refresh_token":"sk-ant-ort01-rotated","expires_in":3600}`)
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("acc-1", channelID)
	env.Credentials = map[string]string{
		"access_token":    "sk-ant-oat01-old",
		"refresh_token":   "sk-ant-ort01-old",
		"oauth_client_id": "cid-custom",
		"oauth_token_url": srv.URL,
	}

	p := &Provider{}
	draft, err := p.RefreshCredential(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}

	// 请求体契约：grant_type / refresh_token / client_id。
	if gotBody["grant_type"] != "refresh_token" || gotBody["refresh_token"] != "sk-ant-ort01-old" {
		t.Fatalf("body = %+v", gotBody)
	}
	if gotBody["client_id"] != "cid-custom" {
		t.Fatalf("client_id = %v，应优先使用凭证里的 oauth_client_id", gotBody["client_id"])
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Fatalf("content-type = %q", gotContentType)
	}

	// draft 携带刷新后的凭证与过期时间。
	if draft.Credentials["access_token"] != "sk-ant-oat01-new" {
		t.Fatalf("draft.access_token = %q", draft.Credentials["access_token"])
	}
	if draft.Credentials["refresh_token"] != "sk-ant-ort01-rotated" {
		t.Fatalf("draft.refresh_token = %q（轮换后的新值必须写回）", draft.Credentials["refresh_token"])
	}
	if draft.ExpiresAt <= time.Now().Unix() {
		t.Fatalf("draft.ExpiresAt = %d", draft.ExpiresAt)
	}

	// env 也同步更新（刷新中途失败恢复、并发请求取新值）。
	if env.GetCredential("access_token") != "sk-ant-oat01-new" ||
		env.GetCredential("refresh_token") != "sk-ant-ort01-rotated" {
		t.Fatalf("env credentials = %+v", env.Credentials)
	}
	if env.GetCredential("expires_at") == "" {
		t.Fatal("expires_at 未写回")
	}
}

// TestRefreshCredentialDefaultClientID 验证凭证没带 client_id 时用官方公开 ID 兜底。
func TestRefreshCredentialDefaultClientID(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = io.WriteString(w, `{"access_token":"tk2","expires_in":60}`)
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("acc-2", channelID)
	env.Credentials = map[string]string{
		"refresh_token":   "rt",
		"oauth_token_url": srv.URL,
	}

	p := &Provider{}
	draft, err := p.RefreshCredential(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["client_id"] != defaultClientID {
		t.Fatalf("client_id = %v, want %q", gotBody["client_id"], defaultClientID)
	}
	// 响应没有 refresh_token 时保留旧值（Anthropic 的轮换是"给了新值才作废旧值"）。
	if draft.Credentials["refresh_token"] != "rt" {
		t.Fatalf("refresh_token = %q, want 保留旧值", draft.Credentials["refresh_token"])
	}
}

// TestRefreshCredentialFailure 验证刷新失败时返回错误且不破坏原凭证。
func TestRefreshCredentialFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"refresh token is invalid"}`)
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("acc-3", channelID)
	env.Credentials = map[string]string{
		"access_token":    "sk-ant-oat01-keep",
		"refresh_token":   "sk-ant-ort01-broken",
		"oauth_token_url": srv.URL,
	}

	p := &Provider{}
	draft, err := p.RefreshCredential(context.Background(), env)
	if err == nil || draft != nil {
		t.Fatalf("应返回错误, got draft=%+v err=%v", draft, err)
	}
	ae := ir.AsAPIError(err)
	if ae == nil || ae.Code != ir.ErrCodeUpstreamAuth {
		t.Fatalf("err = %v", err)
	}
	// 失败时原凭证必须原样保留，由上层决定是否恢复状态。
	if env.GetCredential("access_token") != "sk-ant-oat01-keep" {
		t.Fatalf("access_token 被破坏: %q", env.GetCredential("access_token"))
	}
}

// TestRefreshCredentialManualToken 手动粘贴的长期 token：无 refresh_token 也能返回草稿。
func TestRefreshCredentialManualToken(t *testing.T) {
	env := provider.NewAccountEnv("acc-4", channelID)
	env.Credentials = map[string]string{"access_token": "manual-token"}

	p := &Provider{}
	draft, err := p.RefreshCredential(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Credentials["access_token"] != "manual-token" {
		t.Fatalf("draft = %+v", draft)
	}

	// 连 access_token 都没有：明确报错。
	empty := provider.NewAccountEnv("acc-5", channelID)
	if _, err := p.RefreshCredential(context.Background(), empty); err == nil {
		t.Fatal("空凭证应报错")
	}
}

// TestEnsureAccessTokenExpired 触发过期前刷新。
func TestEnsureAccessTokenExpired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"sk-ant-oat01-fresh","expires_in":7200}`)
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("acc-6", channelID)
	env.Credentials = map[string]string{
		"access_token":    "sk-ant-oat01-stale",
		"refresh_token":   "sk-ant-ort01-ok",
		"expires_at":      time.Now().Add(-time.Minute).Format(time.RFC3339), // 已过期
		"oauth_token_url": srv.URL,
	}

	p := &Provider{}
	token, err := p.ensureAccessToken(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if token != "sk-ant-oat01-fresh" {
		t.Fatalf("token = %q", token)
	}
}

// TestEnsureAccessTokenMissing 空账号的报错必须是 UpstreamAuth。
func TestEnsureAccessTokenMissing(t *testing.T) {
	p := &Provider{}
	_, err := p.ensureAccessToken(context.Background(), provider.NewAccountEnv("acc-7", channelID))
	ae := ir.AsAPIError(err)
	if ae == nil || ae.Code != ir.ErrCodeUpstreamAuth {
		t.Fatalf("err = %v", err)
	}
}
