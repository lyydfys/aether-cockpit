package workbuddy

import (
	"context"
	"strings"
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

// prompt_tokens 含 cached_tokens（OpenAI 口径），必须拆开后再落库：
// 直接相加会把命中量算两遍，总量翻倍、命中率永远 < 50%。
func TestParseSSEUsageSplitsCached(t *testing.T) {
	sse := "data: {\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":50,\"total_tokens\":1050," +
		"\"prompt_tokens_details\":{\"cached_tokens\":900}}}\n\ndata: [DONE]\n\n"
	out := make(chan ir.UnifiedStreamEvent, 16)
	parseSSE(context.Background(), out, strings.NewReader(sse))
	close(out)

	var got *ir.Usage
	for ev := range out {
		if ev.Type == ir.EventUsage {
			got = ev.Usage
		}
	}
	if got == nil {
		t.Fatal("没有产出 usage 事件")
	}
	if got.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100（prompt 1000 - cached 900）", got.InputTokens)
	}
	if got.CachedTokens != 900 {
		t.Errorf("CachedTokens = %d, want 900", got.CachedTokens)
	}
	if got.TotalTokens != 1050 {
		t.Errorf("TotalTokens = %d, want 1050（旧口径会算成 1950）", got.TotalTokens)
	}
}

// 采样参数要原样下发给上游：客户端直连官方时不会复读，走网关丢参数就会。
func TestBuildPayloadForwardsRepetitionPenalties(t *testing.T) {
	f, p := 0.4, 0.1
	req := &ir.UnifiedChatRequest{
		Items:            []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
		FrequencyPenalty: &f,
		PresencePenalty:  &p,
	}
	payload := buildPayload(req, "deepseek-v4.1-flash", true)
	if got, ok := payload["frequency_penalty"]; !ok || got != 0.4 {
		t.Fatalf("frequency_penalty = %v, want 0.4", payload["frequency_penalty"])
	}
	if got, ok := payload["presence_penalty"]; !ok || got != 0.1 {
		t.Fatalf("presence_penalty = %v, want 0.1", payload["presence_penalty"])
	}

	// 未传时不要凭空塞 0，保持"上游默认"。
	bare := buildPayload(&ir.UnifiedChatRequest{Items: req.Items}, "m", true)
	if _, ok := bare["frequency_penalty"]; ok {
		t.Fatal("未传时不应下发 frequency_penalty")
	}
}
