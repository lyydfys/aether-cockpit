package workbuddy

import (
	"testing"

	"aether-cockpit/internal/core/ir"
)

func TestIntlInjectsSystemPrompt(t *testing.T) {
	req := &ir.UnifiedChatRequest{Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}}}
	cn := buildPayload(req, "glm-5.3", false)
	msgs := cn["messages"].([]map[string]any)
	if len(msgs) == 0 || msgs[0]["role"] == "sys" || msgs[0]["role"] == "system" {
		t.Fatalf("CN 不应自动补 system: %+v", msgs)
	}
	intl := buildPayload(req, "glm-5.3", true)
	msgs = intl["messages"].([]map[string]any)
	if len(msgs) == 0 || msgs[0]["role"] != "system" {
		t.Fatalf("Intl 应补 system: %+v", msgs)
	}
}
