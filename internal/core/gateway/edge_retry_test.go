package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

// 回归：单账号渠道撞上 Cloudflare 520 时，网关必须**自己重试到成功**，
// 而不是把错误透传给客户端让任务断掉。
//
// 旧行为（用户实际遇到）：upstream 返回 520 → 账号被 exclude → 只有一个
// 账号，第二轮选号失败 → 直接把 502 抛给客户端 → 任务中断。
func TestSingleAccountRetriesThroughUpstream520(t *testing.T) {
	var hits int32
	const failFirst = 2
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n <= failFirst {
			w.WriteHeader(520)
			_, _ = w.Write([]byte(`{"error":{"message":"origin unreachable"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer upstream.Close()

	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
		MaxAttempts:      failFirst + 2,
		FirstByteTimeout: 5 * time.Second,
		RequestBudget:    30 * time.Second,
	})
	env := provider.NewAccountEnv("acc-only", "openai-compat")
	env.SetCredential("base_url", upstream.URL+"/v1")
	env.SetCredential("api_key", "sk-test")
	g.Pool().Add(env)
	g.models.Rebuild()

	srv := httptest.NewServer(g)
	defer srv.Close()

	body := `{"model":"openai-compat/gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":false}`
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("单账号撞 520 应自动重试成功，得到 %d（说明没有重试）", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&hits); got < failFirst+1 {
		t.Fatalf("上游只被打了 %d 次，未达预期重试次数", got)
	}
	// 账号不应因为线路故障被熔断。
	if env.State() != provider.StateReady {
		t.Fatalf("线路故障把账号打成 %q（应保持 ready）", env.State())
	}
}

// 反例：真的持续 520（线路一直坏）时，重试耗尽后应如实报错，
// 而不是无限重试把客户端挂死。
func TestPersistent520EventuallyFails(t *testing.T) {
	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(520)
		_, _ = w.Write([]byte(`{"error":{"message":"origin unreachable"}}`))
	}))
	defer upstream.Close()

	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
		MaxAttempts:      3,
		FirstByteTimeout: 5 * time.Second,
		RequestBudget:    30 * time.Second,
	})
	env := provider.NewAccountEnv("acc-down", "openai-compat")
	env.SetCredential("base_url", upstream.URL+"/v1")
	env.SetCredential("api_key", "sk-test")
	g.Pool().Add(env)
	g.models.Rebuild()

	srv := httptest.NewServer(g)
	defer srv.Close()

	body := `{"model":"openai-compat/gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":false}`
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		t.Fatal("持续 520 不该返回成功")
	}
	// 有重试但有限度：至少打了 2 次，且不会打爆。
	n := atomic.LoadInt32(&hits)
	if n < 2 {
		t.Fatalf("应至少重试一次，实际只打了 %d 次", n)
	}
	if n > 6 {
		t.Fatalf("重试次数失控：%d 次", n)
	}
}
