package gateway

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

// 客户端在流中途断开后，网关仍要把上游读完并记下末尾的 usage（relay-panel ③）。
func TestStreamDrainCapturesUsageAfterClientGone(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, `data: {"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`+"\n\n")
		if fl != nil {
			fl.Flush()
		}
		time.Sleep(400 * time.Millisecond)
		_, _ = io.WriteString(w, `data: {"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`+"\n\n")
		if fl != nil {
			fl.Flush()
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	g := New(Config{
		MaxAttempts:             2,
		FirstByteTimeout:        5 * time.Second,
		MaxConcurrentPerAccount: -1,
		SettingsPath:            filepath.Join(drainDir(t), "settings.json"),
		AccountStorePath:        filepath.Join(drainDir(t), "accounts.json"),
		UsagePath:               filepath.Join(drainDir(t), "usage.json"),
		MaxRecords:              10,
	})
	env := provider.NewAccountEnv("drain-acc", "openai-compat")
	env.Credentials = map[string]string{"base_url": upstream.URL + "/v1", "api_key": "sk-x"}
	env.SetHTTPClient(upstream.Client())
	g.Pool().Add(env)
	g.models.Rebuild()

	srv := httptest.NewServer(g)
	defer srv.Close()

	body := `{"model":"openai-compat/gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":true}`
	// 用裸 TCP 发请求、读到首块 SSE 后直接关 socket：服务端立刻看到 EOF，
	// 等价于 Agent 掉线（比 http.Client 的 cancel 更确定，不受连接复用影响）。
	host := strings.TrimPrefix(srv.URL, "http://")
	conn, err := net.DialTimeout("tcp", host, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	reqText := "POST /v1/chat/completions HTTP/1.1\r\nHost: " + host +
		"\r\nContent-Type: application/json\r\nContent-Length: " + strconv.Itoa(len(body)) +
		"\r\nConnection: close\r\n\r\n" + body
	if _, err := conn.Write([]byte(reqText)); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4096)
	sawData := false
	deadlineRead := time.Now().Add(3 * time.Second)
	_ = conn.SetReadDeadline(deadlineRead)
	for !sawData && time.Now().Before(deadlineRead) {
		n, err := conn.Read(buf)
		if n > 0 && strings.Contains(string(buf[:n]), "data:") {
			sawData = true
			break
		}
		if err != nil {
			break
		}
	}
	_ = conn.Close()
	if !sawData {
		t.Fatal("没有读到首块 SSE")
	}

	// 宽限期 + 余量内应把末尾 usage 记下来。
	deadline := time.Now().Add(6 * time.Second)
	var tokens, requests int64
	for time.Now().Before(deadline) {
		tokens, requests = usageTotalsNow(g)
		if tokens >= 18 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if tokens < 18 {
		for _, rec := range g.logs.Recent(5) {
			t.Logf("log: status=%s code=%s err=%s provider=%s", rec.Status, rec.ErrCode, rec.ErrMsg, rec.ProviderID)
		}
		t.Logf("usage cells: %d", func() int {
			g.usage.mu.Lock()
			defer g.usage.mu.Unlock()
			n := 0
			for _, cs := range g.usage.days {
				n += len(cs)
			}
			return n
		}())
		t.Fatalf("断开后未记录到末尾 usage：tokens=%d requests=%d", tokens, requests)
	}
	if requests != 1 {
		t.Fatalf("应记录 1 次请求，requests=%d", requests)
	}
}

// usageTotalsNow 取此刻的 token/请求总计（重算，不累加）。
func usageTotalsNow(g *Gateway) (tokens, requests int64) {
	g.usage.mu.Lock()
	defer g.usage.mu.Unlock()
	for _, cells := range g.usage.days {
		for _, c := range cells {
			tokens += c.InputTokens + c.OutputTokens
			requests += c.Requests
		}
	}
	return
}

// 对照组：不断开时同一条流应记录 usage（用于区分"解析/上游形状"与"排空"）。
func TestStreamUsageRecordedWithoutDisconnect(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, `data: {"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`+"\n\n")
		if fl != nil {
			fl.Flush()
		}
		time.Sleep(200 * time.Millisecond)
		_, _ = io.WriteString(w, `data: {"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`+"\n\n")
		if fl != nil {
			fl.Flush()
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	g := New(Config{
		MaxAttempts: 2, FirstByteTimeout: 5 * time.Second, MaxConcurrentPerAccount: -1,
		SettingsPath:     filepath.Join(drainDir(t), "settings.json"),
		AccountStorePath: filepath.Join(drainDir(t), "accounts.json"),
		UsagePath:        filepath.Join(drainDir(t), "usage.json"), MaxRecords: 10,
	})
	env := provider.NewAccountEnv("drain-acc2", "openai-compat")
	env.Credentials = map[string]string{"base_url": upstream.URL + "/v1", "api_key": "sk-x"}
	env.SetHTTPClient(upstream.Client())
	g.Pool().Add(env)
	g.models.Rebuild()
	srv := httptest.NewServer(g)
	defer srv.Close()

	body := `{"model":"openai-compat/gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":true}`
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "[DONE]") {
		t.Fatalf("流未读完: %s", b)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if tokens, _ := usageTotalsNow(g); tokens >= 18 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	tokens, requests := usageTotalsNow(g)
	t.Fatalf("未记录 usage：tokens=%d requests=%d", tokens, requests)
}

// drainDir 给每个用例一个临时目录；账本文件可能仍被写入方打开，
// 清理失败就留给系统临时目录，不让 t.TempDir 的清理把测试判成失败。
func drainDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "drain-usage-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		time.Sleep(200 * time.Millisecond)
		_ = os.RemoveAll(dir)
	})
	return dir
}
