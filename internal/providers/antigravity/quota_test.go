package antigravity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

func quotaTestServer(t *testing.T, summary, models any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "retrieveUserQuotaSummary"):
			_ = json.NewEncoder(w).Encode(summary)
		case strings.HasSuffix(r.URL.Path, "retrieveUserQuota"):
			_ = json.NewEncoder(w).Encode(models)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func quotaEnv(srv *httptest.Server) *provider.AccountEnv {
	env := provider.NewAccountEnv("agy-quota", "antigravity")
	env.Credentials = map[string]string{
		"access_token": "tok",
		"project_id":   "proj-1",
		"base_url":     srv.URL,
	}
	env.HTTPClient = srv.Client()
	return env
}

func TestQueryQuotaPoolsAndModels(t *testing.T) {
	reset := time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
	summary := map[string]any{
		"groups": []any{
			map[string]any{"displayName": "Gemini", "buckets": []any{
				map[string]any{"window": "5h", "remainingFraction": 0.9, "resetTime": reset},
				map[string]any{"window": "7d", "remainingFraction": 0.75},
			}},
			map[string]any{"displayName": "Claude & GPT", "buckets": []any{
				map[string]any{"window": "5h", "remainingFraction": 0.5},
			}},
		},
		"description": "按模型池统计",
	}
	models := map[string]any{"buckets": []any{
		map[string]any{"modelId": "gemini-3-flash", "remainingFraction": 0.95, "resetTime": reset},
		map[string]any{"modelId": "claude-sonnet-4-6", "remainingFraction": 0.4},
		map[string]any{"modelId": "chat_20706", "remainingFraction": 1},
		map[string]any{"modelId": "tab_flash_lite_preview", "remainingFraction": 1},
	}}
	srv := quotaTestServer(t, summary, models)
	env := quotaEnv(srv)

	q, err := (&Provider{}).QueryQuota(context.Background(), env)
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if q.Message != "按模型池统计" {
		t.Fatalf("message = %q", q.Message)
	}
	var pool5, pool7, claude5 *provider.QuotaWindow
	fam := map[string]*provider.QuotaWindow{}
	for i := range q.Windows {
		w := &q.Windows[i]
		switch {
		case w.Pool == "Gemini" && w.Name == "5 hours":
			pool5 = w
		case w.Pool == "Gemini" && w.Name == "7 days":
			pool7 = w
		case w.Pool == "Claude & GPT" && w.Name == "5 hours":
			claude5 = w
		case w.Family != "":
			fam[w.Name] = w
		}
	}
	if pool5 == nil || pool7 == nil || claude5 == nil {
		t.Fatalf("池窗口缺失: %+v", q.Windows)
	}
	if !almost(pool5.Used, 10) || !almost(pool7.Used, 25) || !almost(claude5.Used, 50) {
		t.Fatalf("用量百分比不对: %v %v %v", pool5.Used, pool7.Used, claude5.Used)
	}
	if pool5.ResetsAt == 0 {
		t.Fatal("resetTime 未解析")
	}
	g := fam["gemini-3-flash"]
	if g == nil || g.Family != "Gemini" || g.Pool != "Gemini" || !almost(g.Used, 5) {
		t.Fatalf("gemini 模型窗口 = %+v", g)
	}
	if c := fam["claude-sonnet-4-6"]; c == nil || c.Family != "Claude" || c.Pool != "Claude & GPT" || !almost(c.Used, 60) {
		t.Fatalf("claude 模型窗口 = %+v", c)
	}
	for _, hidden := range []string{"chat_20706", "tab_flash_lite_preview"} {
		if w := fam[hidden]; w != nil {
			t.Fatalf("IDE 内部额度桶不应出现在窗口里: %+v", w)
		}
	}
}

func TestQueryQuotaBothFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := (&Provider{}).QueryQuota(context.Background(), quotaEnv(srv))
	if err == nil || !strings.Contains(err.Error(), "都失败") {
		t.Fatalf("err = %v", err)
	}
}

func almost(a, b float64) bool {
	d := a - b
	return d < 0.001 && d > -0.001
}

func TestWindowName(t *testing.T) {
	cases := map[[2]string]string{
		{"5h", ""}:          "5 hours",
		{"5 hours", ""}:     "5 hours",
		{"", "Weekly"}:      "7 days",
		{"7d", ""}:          "7 days",
		{"daily", ""}:       "1 day",
		{"", "Custom Plan"}: "Custom Plan",
	}
	for in, want := range cases {
		if got := windowName("", in[0], in[1]); got != want {
			t.Errorf("windowName(%q,%q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

// bucketId 白名单优先于文案猜测：上游把 "Weekly" 改成别的措辞时，
// 只要 bucketId 还是 `claude:weekly` 就仍能判定为 7 天窗口。
func TestWindowNameFromBucketID(t *testing.T) {
	cases := map[string]string{
		"3p-5h":                 "5 hours",
		"claude:5h":             "5 hours",
		"gemini-argon-3p-5h":    "5 hours",
		"3p-weekly":             "7 days",
		"claude:weekly":         "7 days",
		"gemini-argon-weekly":   "7 days",
		"argon:weekly":          "7 days",
		"":                      "",
		"unknown-bucket":        "",
	}
	for id, want := range cases {
		if got := windowNameFromBucketID(id); got != want {
			t.Errorf("windowNameFromBucketID(%q) = %q, want %q", id, got, want)
		}
	}
	// 白名单命中时，即便文案写的是别的东西也要以 bucketId 为准。
	if got := windowName("claude:weekly", "5 小时额度", ""); got != "7 days" {
		t.Errorf("bucketId 应优先：得到 %q", got)
	}
}
