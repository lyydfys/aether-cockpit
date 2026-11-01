package claude

import (
	"context"
	"io"
	"strings"

	"aether-cockpit/internal/core/ir"
)

// 供协议同为 Anthropic Messages、但传输层不同的渠道复用
// （如 Bedrock 原生 invoke / invoke-with-response-stream），
// 避免复制一份消息构造与流解析实现。

// BuildMessagesBody 构造标准 Anthropic Messages 请求体。
func BuildMessagesBody(req *ir.UnifiedChatRequest, model string, thinking bool) map[string]any {
	return buildRequestBody(req, model, thinking)
}

// ParseMessagesSSE 解析 Anthropic SSE，把增量发到 out。
func ParseMessagesSSE(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader) {
	parseSSE(ctx, out, r)
}

// ParseMessagesJSON 解析非流式 Anthropic Messages 响应。
func ParseMessagesJSON(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader) {
	parseJSON(ctx, out, r)
}

// SplitThinkingModel 剥离 manifest 约定的 "-thinking" 后缀，
// 返回真实模型名与是否启用思考。
func SplitThinkingModel(model string) (string, bool) {
	model = strings.TrimSpace(model)
	thinking := strings.HasSuffix(model, "-thinking")
	return strings.TrimSuffix(model, "-thinking"), thinking
}
