package openaicompat

import (
	"testing"

	"aether-cockpit/internal/core/ir"
)

func structuredReq() *ir.UnifiedChatRequest {
	strict := true
	return &ir.UnifiedChatRequest{
		Model: "vendor/m", UpstreamModel: "m", Stream: true,
		Items:      []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
		ToolChoice: &ir.ToolChoice{Mode: "function", Name: "read"},
		ResponseFormat: &ir.ResponseFormat{
			Type: "json_schema", Name: "out", Strict: &strict,
			Schema: []byte(`{"type":"object"}`),
		},
	}
}

func TestOpenAIStructuredPayload(t *testing.T) {
	payload := buildPayload(structuredReq(), "m", "max_tokens")
	tc, _ := payload["tool_choice"].(map[string]any)
	fn, _ := tc["function"].(map[string]any)
	if tc["type"] != "function" || fn["name"] != "read" {
		t.Fatalf("tool_choice = %v", payload["tool_choice"])
	}
	rf, _ := payload["response_format"].(map[string]any)
	spec, _ := rf["json_schema"].(map[string]any)
	if rf["type"] != "json_schema" || spec["name"] != "out" || spec["strict"] != true {
		t.Fatalf("response_format = %v", payload["response_format"])
	}
}

func TestResponsesStructuredPayload(t *testing.T) {
	payload := buildResponsesPayload(structuredReq(), "m")
	tc, _ := payload["tool_choice"].(map[string]any)
	if tc["type"] != "function" || tc["name"] != "read" {
		t.Fatalf("tool_choice = %v", payload["tool_choice"])
	}
	text, _ := payload["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	if format["type"] != "json_schema" || format["name"] != "out" || format["strict"] != true {
		t.Fatalf("text.format = %v", payload["text"])
	}
}
