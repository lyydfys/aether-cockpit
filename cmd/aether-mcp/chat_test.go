package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// aether_chat 走标准 /v1/chat/completions，并返回助手文本 + 用量摘要。
func TestMCPChatForwards(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gpt-4o","choices":[{"message":{"content":"你好"},
			"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
	defer srv.Close()

	s := &server{addr: srv.URL}
	out, err := s.callTool("aether_chat", json.RawMessage(`{"prompt":"hi","system":"你是助手","max_tokens":64}`))
	if err != nil {
		t.Fatalf("调用失败：%v", err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("路径 = %q", gotPath)
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("应包含 system + user 两条消息，得到 %+v", gotBody["messages"])
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" {
		t.Errorf("首条应为 system，得到 %+v", first)
	}
	if gotBody["max_tokens"].(float64) != 64 {
		t.Errorf("max_tokens 未透传：%+v", gotBody)
	}
	if !strings.Contains(out, "你好") {
		t.Errorf("应返回助手文本，得到 %q", out)
	}
	if !strings.Contains(out, "15 tokens") {
		t.Errorf("应带用量摘要，得到 %q", out)
	}
}

// messages 数组优先于 prompt，且角色透传。
func TestMCPChatPrefersMessages(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	s := &server{addr: srv.URL}
	args := `{"prompt":"忽略我","messages":[{"role":"user","content":"第一句"},{"role":"assistant","content":"第二句"}]}`
	if _, err := s.callTool("aether_chat", json.RawMessage(args)); err != nil {
		t.Fatalf("调用失败：%v", err)
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("应只有两条消息（prompt 被忽略），得到 %+v", gotBody["messages"])
	}
}

// 什么都不给要报错，而不是把空 messages 打给上游。
func TestMCPChatRequiresInput(t *testing.T) {
	s := &server{addr: "http://127.0.0.1:1"}
	if _, err := s.callTool("aether_chat", json.RawMessage(`{}`)); err == nil {
		t.Fatal("空输入应报错")
	}
}

// aether_models 走 GET /v1/models 并列出模型。
func TestMCPModels(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o","owned_by":"openai"},{"id":"claude-opus","owned_by":"claude"}]}`))
	}))
	defer srv.Close()

	s := &server{addr: srv.URL}
	out, err := s.callTool("aether_models", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("调用失败：%v", err)
	}
	if gotPath != "/v1/models" {
		t.Errorf("路径 = %q", gotPath)
	}
	if !strings.Contains(out, "共 2 个模型") || !strings.Contains(out, "gpt-4o") {
		t.Errorf("输出不对：%q", out)
	}
}

// 网关密钥要带到请求头（远端/局域网访问时必需）。
func TestMCPForwardsAPIKey(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	s := &server{addr: srv.URL, key: "sk-test-123"}
	if _, err := s.callTool("aether_models", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer sk-test-123" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}
