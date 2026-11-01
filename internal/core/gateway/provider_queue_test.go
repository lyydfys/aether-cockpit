package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

func queueLimitGateway(t *testing.T, cfg Config) (*Gateway, *provider.AccountEnv, *httptest.Server) {
	t.Helper()
	cfg.Addr = ""
	cfg.UsagePath = "-"
	cfg.AccountStorePath = filepath.Join(t.TempDir(), "accounts.json")
	cfg.SettingsPath = filepath.Join(t.TempDir(), "settings.json")
	cfg.MaxAttempts = 2
	cfg.FirstByteTimeout = 3 * time.Second
	g := New(cfg)
	env := provider.NewAccountEnv("q-acc", "mock-native")
	g.Pool().Add(env)
	g.models.Rebuild()
	return g, env, httptest.NewServer(g)
}

// 渠道级队列长度/等待：保存、回显、清空；队列长度覆盖按渠道生效。
func TestProviderQueueOverridesSaveEchoAndApply(t *testing.T) {
	g, _, _ := queueLimitGateway(t, Config{QueueWait: 5 * time.Second, QueueMaxWaiters: 1})
	rec := saveHeaders(t, g, `{"id":"mock-native","queueLimit":2,"queueWait":20}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", rec.Code, rec.Body.String())
	}
	view := providerViewOf(t, g, "mock-native")
	if view["queueLimit"] != float64(2) || view["queueWait"] != float64(20) {
		t.Fatalf("回显不对: limit=%v wait=%v", view["queueLimit"], view["queueWait"])
	}
	if got := g.queueMaxWaitersFor("mock-native"); got != 2 {
		t.Fatalf("queueMaxWaitersFor = %d，期望 2（覆盖全局 1）", got)
	}
	if got := g.queueWaitFor("mock-native"); got != 20*time.Second {
		t.Fatalf("queueWaitFor = %v，期望 20s", got)
	}
	// 覆盖生效：全局上限 1 时，渠道允许 2 个等待者。
	if !g.tryAddLaneWaiter("mock-native") || !g.tryAddLaneWaiter("mock-native") {
		t.Fatal("覆盖 2 时应允许两个等待者")
	}
	if g.tryAddLaneWaiter("mock-native") {
		t.Fatal("第三个等待者应被拒绝")
	}
	g.addLaneWaiter("mock-native", -2)

	// 清空（0 = 跟随全局）。
	if rec := saveHeaders(t, g, `{"id":"mock-native","queueLimit":0,"queueWait":0}`); rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d", rec.Code)
	}
	if _, ok := g.providerQueueLimit("mock-native"); ok {
		t.Fatal("清空后不应再有队列长度覆盖")
	}
	if got := g.queueMaxWaitersFor("mock-native"); got != 1 {
		t.Fatalf("应回到全局 1，got %d", got)
	}
	if got := g.queueWaitFor("mock-native"); got != 5*time.Second {
		t.Fatalf("应回到全局 5s，got %v", got)
	}
}

// 渠道级 Queue wait=1s：全局 5s 时，占用 2.5s 的槽位应让请求约 1s 就 429。
func TestProviderQueueWaitOverrideTimesOut(t *testing.T) {
	g, env, srv := queueLimitGateway(t, Config{QueueWait: 5 * time.Second, MaxConcurrentPerAccount: 1})
	defer srv.Close()
	if rec := saveHeaders(t, g, `{"id":"mock-native","queueWait":1}`); rec.Code != http.StatusOK {
		t.Fatalf("save status = %d", rec.Code)
	}
	release, ok := env.TryAcquire(1)
	if !ok {
		t.Fatal("占位失败")
	}
	defer release()

	start := time.Now()
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"mock-native/echo-pro","messages":[{"role":"user","content":"q"}]}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	elapsed := time.Since(start)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("槽位一直占用应 429，got %d", resp.StatusCode)
	}
	if elapsed < 700*time.Millisecond || elapsed > 2500*time.Millisecond {
		t.Fatalf("约 1s 后应放弃（渠道覆盖），实际 %v", elapsed)
	}
}

// 渠道级 Queue wait=3s：1.2s 后释放槽位，请求应等到并可成功。
func TestProviderQueueWaitOverrideWaits(t *testing.T) {
	g, env, srv := queueLimitGateway(t, Config{QueueWait: 5 * time.Second, MaxConcurrentPerAccount: 1})
	defer srv.Close()
	if rec := saveHeaders(t, g, `{"id":"mock-native","queueWait":3}`); rec.Code != http.StatusOK {
		t.Fatalf("save status = %d", rec.Code)
	}
	release, ok := env.TryAcquire(1)
	if !ok {
		t.Fatal("占位失败")
	}
	go func() {
		time.Sleep(1200 * time.Millisecond)
		release()
	}()
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"mock-native/echo-pro","messages":[{"role":"user","content":"q"}]}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("槽位释放后应成功，got %d: %v", resp.StatusCode, body)
	}
}
