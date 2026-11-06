package workbuddy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

// TestLoginAPIBase 钉住两个渠道各自的登录站点根。
//
// 这是本 bug 的回归测试：登录链路曾经写死 copilot.tencent.com，
// 国际版点「接入」拿到的却是国内登录页（authUrl 由服务端按站点签发）。
// 纯函数，不碰网络。
func TestLoginAPIBase(t *testing.T) {
	// 确保测试钩子不干扰域名断言。
	oldOverride := wbAPIBaseOverride
	wbAPIBaseOverride = ""
	defer func() { wbAPIBaseOverride = oldOverride }()

	cases := []struct {
		id      string
		want    string
		notWant string
	}{
		{"workbuddy-cn", "https://copilot.tencent.com", "https://www.workbuddy.ai"},
		{"workbuddy-intl", "https://www.workbuddy.ai", "https://copilot.tencent.com"},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			prov, ok := provider.Get(tc.id)
			if !ok {
				t.Fatalf("%s 未注册", tc.id)
			}
			p, ok := prov.(*Provider)
			if !ok {
				t.Fatalf("%s 不是 *workbuddy.Provider", tc.id)
			}

			got := loginAPIBase(p)
			if got != tc.want {
				t.Fatalf("%s 登录站点 = %q, want %q", tc.id, got, tc.want)
			}
			if got == tc.notWant {
				t.Fatalf("%s 登录站点串到了另一区: %q", tc.id, got)
			}

			// 卡片文案（Manifest.Description）必须与真实登录站点一致，
			// 否则用户看到「直连 xxx」却跳到另一个域名。
			host := strings.TrimPrefix(tc.want, "https://")
			if desc := p.Manifest().Description; !strings.Contains(desc, host) {
				t.Fatalf("%s 卡片文案未提到 %s: %q", tc.id, host, desc)
			}
		})
	}
}

// TestProviderOrigin 覆盖推导站点根的边界：版本段可有可无、末尾斜杠。
func TestProviderOrigin(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://copilot.tencent.com/v2", "https://copilot.tencent.com"},
		{"https://www.workbuddy.ai/v2", "https://www.workbuddy.ai"},
		{"https://www.workbuddy.ai/v2/", "https://www.workbuddy.ai"},
		{"https://www.workbuddy.ai", "https://www.workbuddy.ai"},
		{"https://www.workbuddy.ai/", "https://www.workbuddy.ai"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := providerOrigin(tc.in); got != tc.want {
			t.Fatalf("providerOrigin(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestLoginFlow 走一遍完整的 auth/state → auth/token → login/account。
// 两个渠道各跑一次：国际版必须带着自己的 provider_id 建会话（站点根用测试钩子替换，
// 真实域名由 TestLoginAPIBase 覆盖）。
func TestLoginFlow(t *testing.T) {
	for _, providerID := range []string{"workbuddy-cn", "workbuddy-intl"} {
		t.Run(providerID, func(t *testing.T) {
			tokenCalls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/v2/plugin/auth/state"):
					if r.Header.Get("X-No-Authorization") != "true" {
						t.Errorf("缺少 X-No-Authorization")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"state": "st-1"}})
				case strings.HasPrefix(r.URL.Path, "/v2/plugin/auth/token"):
					if r.URL.Query().Get("state") != "st-1" {
						t.Errorf("state = %q", r.URL.Query().Get("state"))
					}
					tokenCalls++
					if tokenCalls < 2 {
						_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "message": "pending"})
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
						"accessToken": "wb-token", "refreshToken": "wb-rt", "domain": "d.example",
					}})
				case strings.HasPrefix(r.URL.Path, "/v2/plugin/login/account"):
					if r.Header.Get("Authorization") != "Bearer wb-token" {
						t.Errorf("account Authorization = %q", r.Header.Get("Authorization"))
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
						"uid": "u1", "nickname": "Nick", "email": "dev@example.com", "enterpriseId": "ent-1",
					}})
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			oldOverride := wbAPIBaseOverride
			wbAPIBaseOverride = srv.URL
			defer func() { wbAPIBaseOverride = oldOverride }()

			s, err := StartLogin(providerID)
			if err != nil {
				t.Fatalf("StartLogin: %v", err)
			}
			if s.ProviderID != providerID {
				t.Fatalf("会话 provider_id = %q, want %q", s.ProviderID, providerID)
			}
			u, err := url.Parse(s.LoginURL)
			if err != nil {
				t.Fatalf("login url: %v", err)
			}
			if u.Query().Get("state") != "st-1" || u.Query().Get("loginSessionId") == "" {
				t.Fatalf("login url 参数不对: %s", s.LoginURL)
			}
			// 轮询与取账号必须复用会话自己的站点根，而不是包级默认值。
			if got := GetLogin(s.ID); got.base != srv.URL {
				t.Fatalf("会话站点根 = %q, want %q", got.base, srv.URL)
			}

			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				if GetLogin(s.ID).State == "done" {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			got := GetLogin(s.ID)
			if got.State != "done" {
				t.Fatalf("state = %s err=%s", got.State, got.Err)
			}
			creds := Credentials(s.ID)
			for k, want := range map[string]string{
				"token": "wb-token", "refresh_token": "wb-rt", "user_id": "u1",
				"user_name": "Nick", "user_email": "dev@example.com", "domain": "d.example", "enterprise_id": "ent-1",
			} {
				if creds[k] != want {
					t.Fatalf("%s = %q, want %q (%+v)", k, creds[k], want, creds)
				}
			}
			if !MarkCommitted(s.ID, "acct-1") || MarkCommitted(s.ID, "acct-2") {
				t.Fatal("提交标记应只成功一次")
			}
		})
	}
}

func TestLoginRejectsOtherProvider(t *testing.T) {
	if _, err := StartLogin("qoder-cn"); err == nil {
		t.Fatal("非 WorkBuddy 渠道应报错")
	}
}
