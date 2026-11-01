package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestIsContextOverflow(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   bool
	}{
		{400, `{"error":{"code":"context_length_exceeded","message":"x"}}`, true},
		{400, `{"error":{"message":"This model's maximum context length is 200000 tokens"}}`, true},
		{400, `{"error":{"message":"Input exceeds the context window for this model"}}`, true},
		{413, `{"error":"prompt is too long"}`, true},
		{400, `{"error":"invalid api key"}`, false},
		{401, `{"error":"context window"}`, false},
		{500, `context length`, false},
		{400, `{"error":"tool schema invalid"}`, false},
	}
	for _, c := range cases {
		if got := isContextOverflow(c.status, []byte(c.body)); got != c.want {
			t.Errorf("isContextOverflow(%d, %q) = %v, want %v", c.status, c.body, got, c.want)
		}
	}
}

func buildResponsesBody(n int) []byte {
	items := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		role := "assistant"
		if i%2 == 0 {
			role = "user"
		}
		items = append(items, map[string]any{"type": "message", "role": role, "content": "m"})
	}
	body, _ := json.Marshal(map[string]any{
		"model":        "codex-mini-latest",
		"instructions": "system prompt",
		"input":        items,
		"stream":       true,
		"temperature":  0.2,
	})
	return body
}

func TestShrinkResponsesInput(t *testing.T) {
	body := buildResponsesBody(100)
	shrunk, dropped, ok := shrinkResponsesInput(body, 10)
	if !ok || dropped != 90 {
		t.Fatalf("shrunk ok=%v dropped=%d", ok, dropped)
	}
	var root struct {
		Instructions string           `json:"instructions"`
		Model        string           `json:"model"`
		Input        []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(shrunk, &root); err != nil {
		t.Fatal(err)
	}
	if len(root.Input) != 10 {
		t.Fatalf("保留条目 = %d", len(root.Input))
	}
	if root.Instructions != "system prompt" || root.Model != "codex-mini-latest" {
		t.Fatal("instructions/model 被破坏")
	}
	if role, _ := root.Input[0]["role"].(string); role != "user" {
		t.Fatalf("裁剪点不在 user 边界: %v", root.Input[0]["role"])
	}

	// 太短不裁。
	if _, _, ok := shrinkResponsesInput(buildResponsesBody(5), 10); ok {
		t.Fatal("条目数不足时不应裁剪")
	}
	// input 是字符串（无法安全裁剪）。
	strBody := []byte(`{"model":"x","input":"hello","stream":true}`)
	if _, _, ok := shrinkResponsesInput(strBody, 1); ok {
		t.Fatal("字符串 input 不应裁剪")
	}
}

type fakeNative struct {
	calls [][]byte
	fn    func(body []byte) (*http.Response, error)
}

func (f *fakeNative) ResponsesPassthrough(_ context.Context, _ *provider.AccountEnv, raw []byte, _ string) (*http.Response, error) {
	f.calls = append(f.calls, append([]byte(nil), raw...))
	return f.fn(raw)
}

func httpResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestTryContextContinue(t *testing.T) {
	env := provider.NewAccountEnv("a", "codex")
	body := buildResponsesBody(100)

	// 超限 → 裁剪重试成功。
	fake := &fakeNative{fn: func(raw []byte) (*http.Response, error) { return httpResp(200, `{"id":"resp_1"}`), nil }}
	fail := httpResp(400, `{"error":{"code":"context_length_exceeded"}}`)
	retry, shrunk, dropped, ok := tryContextContinue(context.Background(), fake, env, body, "codex-mini-latest", fail)
	if !ok || retry.StatusCode != 200 {
		t.Fatalf("ok=%v retry=%v", ok, retry)
	}
	if retry.Body != nil {
		retry.Body.Close()
	}
	if dropped != 100-contextKeepTailItems || len(fake.calls) != 1 {
		t.Fatalf("dropped=%d calls=%d", dropped, len(fake.calls))
	}
	if countResponseItems(shrunk) != contextKeepTailItems {
		t.Fatalf("重试条目 = %d", countResponseItems(shrunk))
	}

	// 非超限错误 → 不重试。
	fake = &fakeNative{fn: func(raw []byte) (*http.Response, error) { return httpResp(200, "ok"), nil }}
	if _, _, _, ok := tryContextContinue(context.Background(), fake, env, body, "m",
		httpResp(400, `{"error":"invalid api key"}`)); ok || len(fake.calls) != 0 {
		t.Fatal("非超限错误不应重试")
	}

	// 超限但条目太少 → 不重试。
	fake = &fakeNative{fn: func(raw []byte) (*http.Response, error) { return httpResp(200, "ok"), nil }}
	if _, _, _, ok := tryContextContinue(context.Background(), fake, env, buildResponsesBody(5), "m",
		httpResp(400, `{"error":"context window"}`)); ok || len(fake.calls) != 0 {
		t.Fatal("条目不足时不应重试")
	}

	// 重试仍失败 → 返回 false，且关闭重试响应体。
	fake = &fakeNative{fn: func(raw []byte) (*http.Response, error) { return httpResp(500, "boom"), nil }}
	if _, _, _, ok := tryContextContinue(context.Background(), fake, env, body, "m",
		httpResp(400, `{"error":"context window"}`)); ok || len(fake.calls) != 1 {
		t.Fatal("重试失败时应放弃")
	}
}
