package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

// 429 + Retry-After：请求换到健康账号成功，出问题账号进入冷却，
// 冷却期内不再被选中（真实端到端，覆盖 penalize + 账号级故障转移）。
func TestRateLimitedAccountCoolsAndIsSkipped(t *testing.T) {
	var badHits int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&badHits, 1)
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"rate limited, slow down","type":"rate_limit_error"}}`)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-ok","object":"chat.completion","created":1,"model":"x",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	}))
	defer good.Close()

	g := New(Config{
		MaxAttempts:             3,
		FirstByteTimeout:        5 * time.Second,
		MaxConcurrentPerAccount: -1,
		SettingsPath:            filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath:        filepath.Join(t.TempDir(), "accounts.json"),
		MaxRecords:              10,
	})
	a := provider.NewAccountEnv("acc-429", "openai-compat")
	a.Credentials = map[string]string{"base_url": bad.URL + "/v1", "api_key": "sk-a"}
	g.Pool().Add(a)
	b := provider.NewAccountEnv("acc-ok", "openai-compat")
	b.Credentials = map[string]string{"base_url": good.URL + "/v1", "api_key": "sk-b"}
	g.Pool().Add(b)
	g.models.Rebuild()

	srv := httptest.NewServer(g)
	defer srv.Close()

	send := func() int {
		body := `{"model":"openai-compat/gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":false}`
		resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer resp.Body.Close()
		_, _ = io.ReadAll(resp.Body)
		return resp.StatusCode
	}

	if code := send(); code != http.StatusOK {
		t.Fatalf("429 后应换账号成功，got %d", code)
	}
	if got := atomic.LoadInt32(&badHits); got == 0 {
		t.Fatal("限流账号应至少被尝试一次")
	}
	// 429 同时记两笔：账号级（多数上游的限流是账号/Key 维度）+ 模型级
	// （细粒度状态，供排序与"只有这个模型被打满"的场景使用）。
	snap := a.Snapshot()
	if snap.State != provider.StateCoolingDown {
		t.Fatalf("429 账号状态 = %s，期望 cooling_down", snap.State)
	}
	if remaining := time.Until(time.Unix(snap.ResetAt, 0)); remaining <= 0 || remaining > 5*time.Second {
		t.Fatalf("冷却剩余时间不合理: %v（reset_at=%d）", remaining, snap.ResetAt)
	}
	if cooling, _ := a.ModelCoolingDown("gpt-4o"); !cooling {
		t.Fatal("429 也应记录模型级休息")
	}

	// 冷却期内第二次请求：直接走健康账号，不再打限流账号。
	before := atomic.LoadInt32(&badHits)
	if code := send(); code != http.StatusOK {
		t.Fatalf("第二次请求应成功，got %d", code)
	}
	if after := atomic.LoadInt32(&badHits); after != before {
		t.Fatalf("冷却期内不应再请求限流账号（hits %d → %d）", before, after)
	}

	// 该账号的**别的模型**仍然可用：这是模型级冷却的意义所在。
	if cooling, _ := a.ModelCoolingDown("gpt-4o-mini"); cooling {
		t.Fatal("其它模型不该被连坐")
	}
}
