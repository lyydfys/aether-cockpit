package antigravity

import (
	"context"
	"io"
	"strings"

	"aether-cockpit/internal/core/ir"
)

// GeminiBody 构造原生 generateContent 请求体（v1internal 信封里的 request 部分）。
// gemini-compat 渠道复用同一套 IR→Gemini 翻译，保证两个渠道行为一致。
func GeminiBody(req *ir.UnifiedChatRequest, model string) map[string]any {
	body := map[string]any{"contents": toContents(req)}
	if s := strings.TrimSpace(req.Instructions); s != "" {
		body["systemInstruction"] = map[string]any{
			"parts": []any{map[string]any{"text": s}},
		}
	}
	if tools := toGeminiTools(req.Tools); len(tools) > 0 {
		body["tools"] = tools
	}
	if cfg := toolConfig(req); cfg != nil {
		body["toolConfig"] = cfg
	}
	if gc := generationConfig(req, model); len(gc) > 0 {
		body["generationConfig"] = gc
	}
	return body
}

// ParseGeminiSSE 解析原生 Gemini SSE（同时兼容 v1internal 的 response 信封）。
func ParseGeminiSSE(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader) {
	parseSSEAs(ctx, out, r, "gemini-compat", "gemini")
}
