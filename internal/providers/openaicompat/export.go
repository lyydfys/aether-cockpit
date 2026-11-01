package openaicompat

import (
	"context"
	"io"
	"net/http"

	"aether-cockpit/internal/core/ir"
)

// 供协议同为「OpenAI 聊天 + SSE」、但需要私有头/请求体加工的渠道
// （如 CodeBuddy）复用，避免复制一份消息与流解析实现。

// BuildChatPayload 把 IR 请求转成 OpenAI chat/completions 请求体。
func BuildChatPayload(req *ir.UnifiedChatRequest, upstreamModel string) map[string]any {
	return buildPayload(req, upstreamModel, "max_tokens")
}

// ParseChatSSE 解析 OpenAI 兼容 SSE，把增量发到 out。
func ParseChatSSE(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader) {
	parseSSE(ctx, out, r)
}

// ParseChatJSON 解析非流式的整段 OpenAI 兼容响应。
// 少数端点会无视 stream=true 直接返回 JSON（如 CodeBuddy 的 XHR 形态），
// 调用方按 Content-Type 选择这条路径，避免把 JSON 当 SSE 解析出空响应。
func ParseChatJSON(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader) {
	parseWholeJSON(ctx, out, r)
}

// MapChatHTTPError 按 HTTP 状态码映射上游错误。
func MapChatHTTPError(status int, body string, hdr http.Header) *ir.APIError {
	return mapStatusCode(status, body, hdr)
}
