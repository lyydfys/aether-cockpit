package gateway

import (
	"aether-cockpit/internal/core/state"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func queueGateway(t *testing.T, wait time.Duration) (*Gateway, *provider.AccountEnv, *httptest.Server) {
	t.Helper()
	g := New(Config{
		MaxAttempts:      1,
		FirstByteTimeout: 5 * time.Second,
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		UsagePath:        "-",
		QueueWait:        wait,
	})
	env := provider.NewAccountEnv("q-acc", "mock-native")
	env.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	one := 1
	env.SetConcurrencyOverride(&one)
	g.pool.Add(env)
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	return g, env, srv
}

// 槽位被占满时，第二个请求排队等第一个跑完，而不是立刻 429。
func TestQueueWaitsForSlot(t *testing.T) {
	_, env, srv := queueGateway(t, 2*time.Second)
	release, ok := env.TryAcquire(1)
	if !ok {
		t.Fatal("占位失败")
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		release()
	}()
	start := time.Now()
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"mock-native/echo-pro","messages":[{"role":"user","content":"queued"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("排队后应 200，得到 %d", resp.StatusCode)
	}
	if d := time.Since(start); d < 100*time.Millisecond {
		t.Fatalf("应在等槽位（150ms），实际 %v", d)
	}
}

// 排队预算用完仍然没有槽位：立刻 429，而不是无限等。
func TestQueueBudgetExhausted(t *testing.T) {
	_, env, srv := queueGateway(t, 80*time.Millisecond)
	release, _ := env.TryAcquire(1)
	defer release()
	start := time.Now()
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"mock-native/echo-pro","messages":[{"role":"user","content":"full"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("预算用完应 429，得到 %d", resp.StatusCode)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("不应等太久: %v", d)
	}
}

// QueueWait<0：不排队，立刻 429。
func TestQueueDisabled(t *testing.T) {
	_, env, srv := queueGateway(t, -1)
	release, _ := env.TryAcquire(1)
	defer release()
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"mock-native/echo-pro","messages":[{"role":"user","content":"nowait"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("禁用排队应 429，得到 %d", resp.StatusCode)
	}
}

func TestLaneWaitersCountedWhileQueued(t *testing.T) {
	g, env, srv := queueGateway(t, 2*time.Second)
	release, ok := env.TryAcquire(1)
	if !ok {
		t.Fatal("占位失败")
	}

	done := make(chan int, 1)
	go func() {
		resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json",
			strings.NewReader(`{"model":"mock-native/echo-pro","messages":[{"role":"user","content":"queued"}]}`))
		if err != nil {
			done <- 0
			return
		}
		resp.Body.Close()
		done <- resp.StatusCode
	}()

	// 请求应在排队：轮询 lanes 直到看到 waiting>=1。
	saw := 0
	for i := 0; i < 40; i++ {
		time.Sleep(20 * time.Millisecond)
		rec := httptest.NewRecorder()
		g.handleLanes(rec, httptest.NewRequest(http.MethodGet, "/api/lanes", nil))
		var lanes map[string]struct {
			Busy    int `json:"busy"`
			Waiting int `json:"waiting"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &lanes)
		if l := lanes["mock-native#q-acc"]; l.Waiting > saw {
			saw = l.Waiting
		}
		if saw > 0 {
			break
		}
	}
	if saw == 0 {
		t.Fatal("排队期间 /api/lanes 的 waiting 应大于 0")
	}

	release()
	if code := <-done; code != http.StatusOK {
		t.Fatalf("排队后应 200，得到 %d", code)
	}
	// 跑完后等待数归零。
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if g.laneWaiting("mock-native") == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := g.laneWaiting("mock-native"); got != 0 {
		t.Fatalf("结束后 waiting 应为 0，得到 %d", got)
	}
}

func TestAddLaneWaiterScoped(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	g.addLaneWaiter("a", 1)
	g.addLaneWaiter("a", 1)
	g.addLaneWaiter("b", 1)
	if g.laneWaiting("a") != 2 || g.laneWaiting("b") != 1 || g.laneWaiting("c") != 0 {
		t.Fatalf("waiters = a:%d b:%d c:%d", g.laneWaiting("a"), g.laneWaiting("b"), g.laneWaiting("c"))
	}
	g.addLaneWaiter("a", -1)
	g.addLaneWaiter("a", -1)
	g.addLaneWaiter("a", -1) // 不出现负数
	if g.laneWaiting("a") != 0 {
		t.Fatalf("a = %d", g.laneWaiting("a"))
	}
}

func TestQueueMaxWaitersBackpressure(t *testing.T) {
	g := New(Config{
		MaxAttempts:      1,
		FirstByteTimeout: 5 * time.Second,
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		UsagePath:        "-",
		QueueWait:        2 * time.Second,
		QueueMaxWaiters:  1,
	})
	env := provider.NewAccountEnv("cap-acc", "mock-native")
	env.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	one := 1
	env.SetConcurrencyOverride(&one)
	g.pool.Add(env)
	srv := httptest.NewServer(g)
	defer srv.Close()

	release, _ := env.TryAcquire(1)

	first := make(chan int, 1)
	go func() {
		resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json",
			strings.NewReader(`{"model":"mock-native/echo-pro","messages":[{"role":"user","content":"q1"}]}`))
		if err != nil {
			first <- 0
			return
		}
		resp.Body.Close()
		first <- resp.StatusCode
	}()
	// 等第一个真正进入排队。
	deadline := time.Now().Add(time.Second)
	for g.laneWaiting("mock-native") == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if g.laneWaiting("mock-native") != 1 {
		t.Fatalf("第一个应在排队，waiting=%d", g.laneWaiting("mock-native"))
	}

	// 第二个请求超过排队上限：快速 429，不进队列。
	start := time.Now()
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"mock-native/echo-pro","messages":[{"role":"user","content":"q2"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("超过排队上限应 429，得到 %d", resp.StatusCode)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("应快速失败，实际 %v", d)
	}

	release()
	if code := <-first; code != http.StatusOK {
		t.Fatalf("第一个请求应 200，得到 %d", code)
	}
}

func TestPenalizeUsesResetHint(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	env := provider.NewAccountEnv("antigravity-x", "mock-native")
	g.pool.Add(env)

	ae := &ir.APIError{
		Code:    ir.ErrCodeUpstreamRateLimit,
		Message: `Antigravity 限流：{"error":{"code":429,"message":"Individual quota reached. Resets in 145h10m54s."}}`,
	}
	ae.WithStatus(429)
	g.penalize(env, ae)
	_, _, resetAt := env.StateDetail()
	if d := time.Until(resetAt); d < 140*time.Hour || d > 146*time.Hour {
		t.Fatalf("应按上游提示冷却约 145h，得到 %v", d)
	}

	env2 := provider.NewAccountEnv("plain-x", "mock-native")
	g.pool.Add(env2)
	ae2 := &ir.APIError{Code: ir.ErrCodeUpstreamRateLimit, Message: "rate limited"}
	ae2.WithStatus(429)
	g.penalize(env2, ae2)
	_, _, reset2 := env2.StateDetail()
	if d := time.Until(reset2); d > 3*time.Minute {
		t.Fatalf("无提示应约 2 分钟，得到 %v", d)
	}
}

func TestResetHintDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"Resets in 145h10m54s.": 145*time.Hour + 10*time.Minute + 54*time.Second,
		"reset in 2h":           2 * time.Hour,
		"Resets in 30m":         30 * time.Minute,
		"1 小时 20 分钟后重置":         80 * time.Minute,
		"no hint here":          0,
		"Resets in 999h":        30 * 24 * time.Hour,
	}
	for in, want := range cases {
		if got := resetHintDuration(in); got != want {
			t.Errorf("resetHintDuration(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPenalizeQuotaExhaustedMarksDepleted(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	cases := []string{
		`Trae 额度已耗尽：4008 Your requests have exceeded the quota.`,
		`上游限流: {"type":"error","error":{"type":"FreeUsageLimitError","message":"Free usage limit reached."}}`,
		`qoder: {"error":{"message":"You exceeded your current quota"}}`,
	}
	for _, msg := range cases {
		env := provider.NewAccountEnv("q-"+msg[:8], "mock-native")
		g.pool.Add(env)
		ae := &ir.APIError{Code: ir.ErrCodeUpstreamFailed, Message: msg}
		g.penalize(env, ae)
		st, _, resetAt := env.StateDetail()
		if st != provider.StateDepleted {
			t.Fatalf("%q 应标记耗尽，得到 %s", msg, st)
		}
		if d := time.Until(resetAt); d < 50*time.Minute || d > 70*time.Minute {
			t.Fatalf("应约 1 小时后恢复，得到 %v", d)
		}
		// rest 视图里 why=quota，用户可看到"额度 · 1 小时后恢复"。
		if rest := restOf(env); rest == nil || rest.Why != "quota" {
			t.Fatalf("rest = %#v", rest)
		}
	}
	// 普通失败不该被当成额度耗尽。
	env := provider.NewAccountEnv("normal-x", "mock-native")
	g.pool.Add(env)
	ae := &ir.APIError{Code: ir.ErrCodeUpstreamFailed, Message: "connection reset"}
	g.penalize(env, ae)
	if st, _, _ := env.StateDetail(); st == provider.StateDepleted {
		t.Fatal("普通失败不应标耗尽")
	}
}

func TestStreamErrorPenalizesAccount(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	env := provider.NewAccountEnv("stream-err", "mock-native")
	g.pool.Add(env)

	src := make(chan ir.UnifiedStreamEvent, 2)
	ae := &ir.APIError{Code: ir.ErrCodeUpstreamFailed, Message: "Trae 额度已耗尽：4008 Your requests have exceeded the quota."}
	src <- ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: "部分"}
	src <- ir.UnifiedStreamEvent{Type: ir.EventError, Err: ae}
	close(src)

	var got []ir.StreamEventType
	for ev := range g.watchStreamErrors(src, env, nil) {
		got = append(got, ev.Type)
	}
	if len(got) != 2 || got[0] != ir.EventTextDelta || got[1] != ir.EventError {
		t.Fatalf("事件应原样转发: %v", got)
	}
	st, _, resetAt := env.StateDetail()
	if st != provider.StateDepleted {
		t.Fatalf("流内额度错误应标记耗尽，得到 %s", st)
	}
	if d := time.Until(resetAt); d < 50*time.Minute || d > 70*time.Minute {
		t.Fatalf("恢复时间应约 1 小时，得到 %v", d)
	}
}

func TestRetriesWithinOneRequestCountOnce(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	g := New(Config{
		MaxAttempts:      3,
		FirstByteTimeout: 2 * time.Second,
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		UsagePath:        "-",
	})
	env := provider.NewAccountEnv("fail-acc", "openai-compat")
	env.Credentials["base_url"] = deadURL
	env.Credentials["api_key"] = "k"
	env.NetworkPolicy = provider.NetPolicyDirectForce
	env.HTTPClient = &http.Client{Timeout: 2 * time.Second}
	g.pool.Add(env)

	srv := httptest.NewServer(g)
	defer srv.Close()
	chat := func() {
		resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json",
			strings.NewReader(`{"model":"openai-compat/deepseek-chat","messages":[{"role":"user","content":"x"}]}`))
		if err == nil {
			resp.Body.Close()
		}
	}

	// 第一次请求内部重试 3 次：只应记 1 次连续失败，账号仍 ready。
	chat()
	if got := env.ConsecutiveFailures(); got != 1 {
		t.Fatalf("单次请求应只计 1 次失败，得到 %d", got)
	}
	if st, _, _ := env.StateDetail(); st == provider.StateCoolingDown {
		t.Fatal("一次抖动不应熔断")
	}
	// 后续 4 次请求累计到 5 → 熔断。
	for i := 0; i < 4; i++ {
		chat()
	}
	if st, _, _ := env.StateDetail(); st != provider.StateCoolingDown {
		t.Fatalf("累计 5 次失败后应冷却，得到 %s", st)
	}
}

func TestGetResponseReturnsOutput(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	g.store.PutResponse(&state.ResponseRecord{
		ResponseID: "resp_x", Model: "m",
		Output: []any{map[string]any{"type": "message", "role": "assistant"}},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_x", nil)
	req.SetPathValue("id", "resp_x")
	g.handleGetResponse(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	var body struct {
		Output []any `json:"output"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Output) != 1 {
		t.Fatalf("output = %#v", body.Output)
	}

	// IR 路径：没有 Output 时用累积的助手消息回放。
	g.store.PutResponse(&state.ResponseRecord{
		ResponseID: "resp_y", Model: "m",
		AccumulatedItems: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"},
			{Type: ir.ItemTypeMessage, Role: "assistant", Text: "hello"},
		},
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/responses/resp_y", nil)
	req.SetPathValue("id", "resp_y")
	g.handleGetResponse(rec, req)
	body.Output = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Output) != 1 {
		t.Fatalf("回放 output = %#v", body.Output)
	}
}

func TestInputItemsIncludeContent(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	g.store.PutResponse(&state.ResponseRecord{
		ResponseID: "resp_items", Model: "m",
		AccumulatedItems: []ir.UnifiedItem{
			{ID: "i1", Type: ir.ItemTypeMessage, Role: "user", Text: "你好"},
			{ID: "i2", Type: ir.ItemTypeMessage, Role: "assistant", Text: "hello"},
			{ID: "i3", Type: ir.ItemTypeMessage, Role: "user"},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_items/input_items", nil)
	req.SetPathValue("id", "resp_items")
	g.handleInputItems(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 3 {
		t.Fatalf("data = %#v", body.Data)
	}
	if c, _ := body.Data[0]["content"].([]any); len(c) != 1 || c[0].(map[string]any)["text"] != "你好" {
		t.Fatalf("user 项 content = %#v", body.Data[0]["content"])
	}
	if c, _ := body.Data[1]["content"].([]any); len(c) != 1 || c[0].(map[string]any)["type"] != "output_text" {
		t.Fatalf("assistant 项 content = %#v", body.Data[1]["content"])
	}
	if _, has := body.Data[2]["content"]; has {
		t.Fatalf("空文本不应带 content: %#v", body.Data[2])
	}
}
