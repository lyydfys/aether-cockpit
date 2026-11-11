package openaicompat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
)

// 上游断流且工具参数残缺时，必须标记为 length，不能报 tool_calls。
//
// 事故背景：客户端（DSH）报
//
//	Tool call "spawn_teammate": Unterminated string in JSON at position 623
//
// 网关侧查证：上游在工具参数写到一半时结束流，且**没有发 finish_reason**。
// 旧逻辑此时一律编造 "tool_calls"，等于告诉客户端"这次响应完整结束"，
// 客户端只能在 JSON.parse 时报错，问题被推给调用方，极难定位。
func TestIncompleteToolArgsDetected(t *testing.T) {
	cases := []struct {
		name   string
		direct map[int]*strings.Builder
		held   map[int]*heldToolCall
		want   bool
	}{
		{
			name: "直发路径参数残缺",
			direct: map[int]*strings.Builder{
				0: builderOf(`{"name":"x","prompt":"被截断`),
			},
			want: true,
		},
		{
			name: "直发路径参数完整",
			direct: map[int]*strings.Builder{
				0: builderOf(`{"name":"x","prompt":"完整"}`),
			},
			want: false,
		},
		{
			name: "无参工具的空参数不算残缺",
			direct: map[int]*strings.Builder{
				0: builderOf(""),
			},
			want: false,
		},
		{
			name: "暂存（乱序）路径参数残缺",
			held: map[int]*heldToolCall{
				1: {id: "c", name: "f", args: *builderOf(`{"a":`)},
			},
			want: true,
		},
		{
			name: "暂存路径参数完整",
			held: map[int]*heldToolCall{
				1: {id: "c", name: "f", args: *builderOf(`{"a":1}`)},
			},
			want: false,
		},
		{
			name:   "完全没有工具调用",
			direct: map[int]*strings.Builder{},
			want:   false,
		},
	}
	for _, c := range cases {
		if got := hasIncompleteToolArgs(c.direct, c.held); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func builderOf(s string) *strings.Builder {
	var b strings.Builder
	b.WriteString(s)
	return &b
}

// 端到端：模拟"上游断流且不发 finish_reason"，网关必须输出 finish_reason=length。
func TestCutStreamProducesLengthFinish(t *testing.T) {
	res := runMockStream(t, "cut")
	if res.FinishReason != "length" {
		t.Fatalf("finish_reason = %q，残缺参数应标记 length", res.FinishReason)
	}
	if res.Args == "" {
		t.Fatal("应收到部分工具参数")
	}
}

// 对照：上游正常发 finish_reason=tool_calls 时不能误判成截断。
func TestNormalToolCallKeepsFinishReason(t *testing.T) {
	res := runMockStream(t, "normal")
	if res.FinishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q，正常结束应为 tool_calls", res.FinishReason)
	}
	if !jsonValid(res.Args) {
		t.Fatalf("正常流的参数应完整：%q", res.Args)
	}
}

// streamResult 是测试用的一次流式结果。
type streamResult struct {
	Args         string
	FinishReason string
}

func jsonValid(s string) bool { return json.Valid([]byte(s)) }

// runMockStream 用 httptest 造两种上游：
//   - "cut"：工具参数写到一半断流，且不发 finish_reason（真实故障形态）；
//   - "normal"：参数完整，并正常发 finish_reason=tool_calls。
func runMockStream(t *testing.T, mode string) streamResult {
	t.Helper()
	var sse strings.Builder
	emit := func(obj map[string]any) {
		b, _ := json.Marshal(obj)
		sse.WriteString("data: ")
		sse.Write(b)
		sse.WriteString("\n\n")
	}
	emit(map[string]any{"id": "c1", "object": "chat.completion.chunk", "model": "m",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{
			"role": "assistant",
			"tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "type": "function",
				"function": map[string]any{"name": "spawn_teammate", "arguments": ""}}},
		}, "finish_reason": nil}}})

	switch mode {
	case "cut":
		emit(map[string]any{"id": "c1", "object": "chat.completion.chunk", "model": "m",
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{
				"tool_calls": []any{map[string]any{"index": 0,
					"function": map[string]any{"arguments": `{"name":"x","prompt":"被截断`}}},
			}, "finish_reason": nil}}})
		// 故意不发 finish_reason，也不发 [DONE]
	case "normal":
		emit(map[string]any{"id": "c1", "object": "chat.completion.chunk", "model": "m",
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{
				"tool_calls": []any{map[string]any{"index": 0,
					"function": map[string]any{"arguments": `{"name":"x","prompt":"完整"}`}}},
			}, "finish_reason": nil}}})
		emit(map[string]any{"id": "c1", "object": "chat.completion.chunk", "model": "m",
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{},
				"finish_reason": "tool_calls"}}})
		sse.WriteString("data: [DONE]\n\n")
	}

	body := sse.String()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Length", itoaTest(len(body)))
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	p := &Provider{cfg: Config{ID: "openai-compat"}}
	env := newTestEnv(t, map[string]string{
		"base_url": srv.URL,
		"api_key":  "sk-test",
	})
	req := &ir.UnifiedChatRequest{
		Model:  "gpt-4o",
		Stream: true,
		Items:  []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "go"}},
	}
	ch, err := p.ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var res streamResult
	for ev := range ch {
		switch ev.Type {
		case ir.EventToolCallDelta:
			res.Args += ev.ArgsDelta
		case ir.EventDone:
			res.FinishReason = ev.FinishReason
		}
	}
	return res
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 && i > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
