package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 首字节超时必须覆盖"同步建连 / 等响应头"阶段。
//
// 历史缺陷：openStream 先同步调用 h()（内部含建连 15s + TLS 15s + 响应头 60s），
// 之后才启动定时器。上游卡在建连或响应头上时定时器根本没启动，请求能挂到分钟级。
// 现在 h() 跑在 goroutine 里，定时器从请求开始计，超时即取消并返回可重试错误。
func TestOpenStreamTimesOutDuringConnect(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
		FirstByteTimeout: 120 * time.Millisecond,
		MaxAttempts:      1,
		RequestBudget:    time.Second,
	})
	env := provider.NewAccountEnv("acc-hang", "openai-compat")

	// h 故意卡住不返回，模拟"连上了但对端不响应"。
	blocked := make(chan struct{})
	defer close(blocked)
	var called int32
	h := func(ctx context.Context, e *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
		atomic.AddInt32(&called, 1)
		<-blocked // 一直不返回，直到测试结束
		return nil, context.DeadlineExceeded
	}

	start := time.Now()
	_, err := g.openStream(context.Background(), context.Background(), h, env,
		&ir.UnifiedChatRequest{Model: "m", Stream: true}, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("卡在建连时应返回超时错误，而不是一直等")
	}
	if atomic.LoadInt32(&called) != 1 {
		t.Fatalf("h 应被调用一次，实际 %d", called)
	}
	// 必须远小于旧行为的"60 秒起步"。
	if elapsed > 2*time.Second {
		t.Fatalf("超时没有生效，耗时 %v", elapsed)
	}
	ae := ir.AsAPIError(err)
	if ae == nil || !ae.Retryable {
		t.Fatalf("超时应标记为可重试（换账号继续）：%+v", err)
	}
}

// 首字节超时也不能把"正常但稍慢"的上游误杀：首个事件按时到达时必须成功。
func TestOpenStreamSucceedsWhenFirstEventArrives(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
		FirstByteTimeout: 2 * time.Second,
		MaxAttempts:      1,
	})
	env := provider.NewAccountEnv("acc-ok", "openai-compat")
	h := func(ctx context.Context, e *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
		ch := make(chan ir.UnifiedStreamEvent, 2)
		ch <- ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: "hi"}
		close(ch)
		return ch, nil
	}
	ch, err := g.openStream(context.Background(), context.Background(), h, env,
		&ir.UnifiedChatRequest{Model: "m", Stream: true}, nil)
	if err != nil {
		t.Fatalf("首个事件按时到达时不该报错：%v", err)
	}
	if ch == nil {
		t.Fatal("应返回事件通道")
	}
}

// 总预算耗尽后不再换账号重试：通过统计 h 的调用次数验证。
func TestRequestBudgetStopsFurtherAttempts(t *testing.T) {
	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		// 每次都慢过预算：让第一次尝试就用光总预算。
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer upstream.Close()

	g := New(Config{
		SettingsPath:            filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath:        filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:               "-",
		MaxRecords:              10,
		FirstByteTimeout:        5 * time.Second,
		MaxAttempts:             5,
		RequestBudget:           250 * time.Millisecond,
		MaxConcurrentPerAccount: -1,
	})
	// 三个账号都指向同一个慢上游。
	for _, id := range []string{"a1", "a2", "a3"} {
		e := provider.NewAccountEnv(id, "openai-compat")
		e.Credentials = map[string]string{"base_url": upstream.URL + "/v1", "api_key": "sk-x"}
		g.Pool().Add(e)
	}
	g.models.Rebuild()

	srv := httptest.NewServer(g)
	defer srv.Close()

	body := `{"model":"openai-compat/gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":false}`
	start := time.Now()
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)

	if resp.StatusCode == http.StatusOK {
		t.Fatal("上游一直失败，不该返回 200")
	}
	// 预算 250ms + 一次请求开销，不该跑满 5 次 × 300ms 以上。
	if elapsed > 2*time.Second {
		t.Fatalf("总预算没有生效，耗时 %v（hits=%d）", elapsed, atomic.LoadInt32(&hits))
	}
	if n := atomic.LoadInt32(&hits); n > 2 {
		t.Fatalf("预算耗尽后不该继续重试，上游被打了 %d 次", n)
	}
}
