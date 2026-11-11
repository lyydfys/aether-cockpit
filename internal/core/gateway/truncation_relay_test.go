package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"aether-cockpit/internal/provider"
)

// 上游因输出长度上限截断时，网关必须**原样转发**，不能自己"修好"或丢弃。
//
// 事故背景：客户端报
//
//	Tool call "todo_write": Unterminated string in JSON at position 305
//
// 看起来像网关坏了，其实是上游模型写工具调用写到一半撞到 max_tokens，
// finish_reason=length。网关的责任是：① 不篡改内容；② 保留 finish_reason；
// ③ 在日志里说清原因，免得用户误判成网关故障。
func TestTruncatedToolCallIsRelayedFaithfully(t *testing.T) {
	const partial = `{"todos": [{"content": "锁定基线", "status": "completed"}, {"content": "继续", "status": "pending`
	sse := "data: " + `{"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"todo_write","arguments":""}}]},"finish_reason":null}]}` + "\n\n" +
		"data: " + `{"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":` + jsonQuote(partial) + `}}]},"finish_reason":null}]}` + "\n\n" +
		"data: " + `{"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}` + "\n\n" +
		"data: [DONE]\n\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Length", itoaLen(len(sse)))
		_, _ = w.Write([]byte(sse))
	}))
	defer upstream.Close()

	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
		MaxAttempts:      1,
		FirstByteTimeout: 5e9,
		RequestBudget:    30e9,
	})
	env := provider.NewAccountEnv("acc-trunc", "openai-compat")
	env.SetCredential("base_url", upstream.URL+"/v1")
	env.SetCredential("api_key", "sk-test")
	g.Pool().Add(env)
	g.models.Rebuild()

	srv := httptest.NewServer(g)
	defer srv.Close()

	body := `{"model":"openai-compat/gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	got := sb.String()

	// ① 内容不能被篡改：截断的片段要原样出现。
	if !strings.Contains(got, `"pending`) {
		t.Fatalf("截断的参数应原样转发，实际响应：%s", truncateForTest(got, 400))
	}
	// ② finish_reason 必须保留 length（不能改成 stop，否则客户端无从判断是截断）。
	if !strings.Contains(got, `"finish_reason":"length"`) {
		t.Fatalf("finish_reason=length 必须保留，实际响应：%s", truncateForTest(got, 400))
	}
}

// itoaLen 把长度转成字符串（避免为一处引入 strconv）。
func itoaLen(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 && i > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// jsonQuote 把字符串转义成 JSON 字符串字面量的内容（测试辅助）。
func jsonQuote(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

func truncateForTest(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

var _ = atomic.LoadInt32
