package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func applyPromptBody(t *testing.T, raw, prompt string, concat bool) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "http://example.com/v1/chat/completions", strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	applySystemPromptBody(req, prompt, concat)
	data, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("body = %s: %v", data, err)
	}
	return out
}

func TestSystemPromptOpenAIChat(t *testing.T) {
	out := applyPromptBody(t, `{"messages":[{"role":"user","content":"hi"}]}`, "你是助手", false)
	msgs := out["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" || msgs[0].(map[string]any)["content"] != "你是助手" {
		t.Fatalf("system prompt 未前置: %+v", out)
	}
}

func TestSystemPromptUserFirstAndConcat(t *testing.T) {
	out := applyPromptBody(t, `{"messages":[{"role":"system","content":"用户设定"},{"role":"user","content":"hi"}]}`, "渠道设定", false)
	got := out["messages"].([]any)[0].(map[string]any)["content"]
	if got != "用户设定" {
		t.Fatalf("用户提示词优先，got %v", got)
	}
	out = applyPromptBody(t, `{"messages":[{"role":"system","content":"用户设定"},{"role":"user","content":"hi"}]}`, "渠道设定", true)
	got = out["messages"].([]any)[0].(map[string]any)["content"]
	if got != "用户设定\n\n渠道设定" {
		t.Fatalf("concat = %v", got)
	}
}

func TestSystemPromptAnthropicAndGemini(t *testing.T) {
	anth := applyPromptBody(t, `{"system":"用户设定","messages":[{"role":"user","content":"hi"}]}`, "渠道设定", true)
	if anth["system"] != "用户设定\n\n渠道设定" {
		t.Fatalf("anthropic system = %v", anth["system"])
	}
	if len(anth["messages"].([]any)) != 1 {
		t.Fatalf("anthropic messages 被误当 OpenAI 注入: %+v", anth["messages"])
	}
	gem := applyPromptBody(t, `{"systemInstruction":{"parts":[{"text":"用户设定"}]},"contents":[]}`, "渠道设定", true)
	si := gem["systemInstruction"].(map[string]any)
	parts := si["parts"].([]any)
	if len(parts) != 2 || parts[1].(map[string]any)["text"] != "渠道设定" {
		t.Fatalf("gemini systemInstruction = %+v", si)
	}
}

func TestSystemPromptResponses(t *testing.T) {
	out := applyPromptBody(t, `{"instructions":"用户设定","input":"hi"}`, "渠道设定", false)
	if out["instructions"] != "用户设定" {
		t.Fatalf("responses instructions = %v", out["instructions"])
	}
	out = applyPromptBody(t, `{"input":"hi"}`, "渠道设定", false)
	if out["instructions"] != "渠道设定" {
		t.Fatalf("responses 无 instructions 时应注入: %v", out)
	}
}
