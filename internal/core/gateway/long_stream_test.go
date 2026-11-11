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

// 回归：长流不能被"请求预算"的 cancel 误杀。
//
// 事故经过（真实会话记录佐证）：
//   - dispatch 里为总重试预算创建了 context.WithTimeout + defer cancelBudget()；
//   - 但 dispatch 在**拿到流之后**就返回（流还在传），defer 立刻执行 → ctx 被 cancel；
//   - openStream 里 `context.AfterFunc(ctx, ...)` 把这次 cancel 误判成"客户端断开"，
//     于是在 streamDrainGrace（20 秒）后调用 cancelUp() 掐掉上游连接；
//   - 表现：流在 +19.7 秒处突然结束、没有 finish_reason、工具调用参数断在半截，
//     客户端只报 "Unterminated string in JSON"。
//
// 这里用 25 秒的流验证：修复后必须能完整收完，且不被提前 cancel。
func TestLongStreamNotKilledByRequestBudget(t *testing.T) {
	const upstreamDur = 25 * time.Second

	var upstreamDone int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		// 每 1 秒发一个 delta，持续 upstreamDur，最后发 finish + [DONE]。
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		deadline := time.After(upstreamDur)
		for {
			select {
			case <-r.Context().Done():
				return // 被上游侧（网关）取消：测试会判失败
			case <-deadline:
				_, _ = w.Write([]byte("data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				if fl != nil {
					fl.Flush()
				}
				atomic.StoreInt32(&upstreamDone, 1)
				return
			case <-ticker.C:
				_, _ = w.Write([]byte("data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"},\"finish_reason\":null}]}\n\n"))
				if fl != nil {
					fl.Flush()
				}
			}
		}
	}))
	defer upstream.Close()

	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
		MaxAttempts:      1,
		FirstByteTimeout: 10 * time.Second,
		// 预算故意设得比流长：它只该约束"拿到首字节前的重试"，不该约束流本身。
		RequestBudget: 120 * time.Second,
	})
	env := provider.NewAccountEnv("acc-long", "openai-compat")
	env.SetCredential("base_url", upstream.URL)
	env.SetCredential("api_key", "sk-test")
	g.Pool().Add(env)
	g.models.Rebuild()

	srv := httptest.NewServer(g)
	defer srv.Close()

	body := `{"model":"openai-compat/gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if rerr != nil {
			break
		}
	}
	elapsed := time.Since(start)
	got := sb.String()

	if atomic.LoadInt32(&upstreamDone) != 1 {
		t.Fatalf("上游流被提前掐断（耗时 %v）——预算的 cancel 又被当成客户端断开了", elapsed)
	}
	if elapsed < upstreamDur-2*time.Second {
		t.Fatalf("流只持续了 %v，短于预期的 %v", elapsed, upstreamDur)
	}
	// 收尾必须完整。
	if !strings.Contains(got, `"finish_reason":"stop"`) {
		t.Fatalf("缺少 finish_reason，响应被截断：%s", truncateForTest(got, 300))
	}
	if !strings.Contains(got, "[DONE]") {
		t.Fatal("缺少 [DONE]")
	}
}
