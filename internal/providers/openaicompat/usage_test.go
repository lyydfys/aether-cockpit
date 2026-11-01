package openaicompat

import "testing"

func TestUsageToIRNormalizesCacheSpellings(t *testing.T) {
	// OpenAI：cached 是 prompt_tokens 的子集。
	openai := &chatUsage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110}
	openai.PromptTokensDetails = &struct {
		CachedTokens int `json:"cached_tokens"`
	}{CachedTokens: 40}
	got := usageToIR(openai)
	if got.InputTokens != 60 || got.CachedTokens != 40 || got.OutputTokens != 10 {
		t.Fatalf("OpenAI 口径归一错误: %+v", got)
	}
	if got.TotalTokens != 110 {
		t.Fatalf("TotalTokens = %d, want 110", got.TotalTokens)
	}

	// DeepSeek：hit/miss 平级字段，miss 即普通输入。
	ds := usageToIR(&chatUsage{
		PromptTokens:          100,
		CompletionTokens:      10,
		PromptCacheHitTokens:  40,
		PromptCacheMissTokens: 60,
	})
	if ds.InputTokens != 60 || ds.CachedTokens != 40 {
		t.Fatalf("DeepSeek 口径归一错误: %+v", ds)
	}

	// 无缓存：原样。
	plain := usageToIR(&chatUsage{PromptTokens: 7, CompletionTokens: 3})
	if plain.InputTokens != 7 || plain.CachedTokens != 0 {
		t.Fatalf("无缓存口径错误: %+v", plain)
	}

	// 思考 token 透传。
	reason := &chatUsage{PromptTokens: 5, CompletionTokens: 20}
	reason.CompletionTokensDetails = &struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	}{ReasoningTokens: 12}
	if got := usageToIR(reason); got.ReasoningTokens != 12 {
		t.Fatalf("思考 token 丢失: %+v", got)
	}
}
