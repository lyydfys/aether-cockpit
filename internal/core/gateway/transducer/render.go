package transducer

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"aether-cockpit/internal/core/ir"
)

// RenderNonStream 渲染非流式的完整响应。
func RenderNonStream(proto ir.Protocol, resp ir.AggregatedResponse, responseID, model string) any {
	switch proto {
	case ir.ProtocolOpenAIResponses:
		return renderResponsesFull(resp, responseID, model)
	case ir.ProtocolAnthropic:
		return renderAnthropicFull(resp, responseID, model)
	case ir.ProtocolGemini:
		return renderGeminiFull(resp, responseID, model)
	default:
		return renderChatFull(resp, responseID, model)
	}
}

// --- OpenAI Chat ---

func renderChatFull(resp ir.AggregatedResponse, id, model string) map[string]any {
	msg := map[string]any{"role": "assistant"}
	if resp.Text != "" {
		msg["content"] = resp.Text
	} else if len(resp.ToolCalls) == 0 {
		msg["content"] = ""
	} else {
		msg["content"] = nil
	}
	// 思考内容用 reasoning_content 承载（DeepSeek / 通义 / 智谱 的通用约定）。
	if resp.ReasoningText != "" {
		msg["reasoning_content"] = resp.ReasoningText
	}
	if len(resp.ToolCalls) > 0 {
		tcs := make([]any, 0, len(resp.ToolCalls))
		for _, tc := range resp.ToolCalls {
			tcs = append(tcs, map[string]any{
				"id":   tc.ID,
				"type": "function",
				"function": map[string]any{
					"name":      tc.Name,
					"arguments": tc.Arguments,
				},
			})
		}
		msg["tool_calls"] = tcs
	}

	return map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       msg,
			"finish_reason": resp.ResolveFinishReason(),
		}},
		"usage": map[string]any{
			"prompt_tokens":     resp.Usage.InputTokens,
			"completion_tokens": resp.Usage.OutputTokens,
			"total_tokens":      resp.Usage.TotalTokens,
		},
	}
}

// --- OpenAI Responses ---

func renderResponsesFull(resp ir.AggregatedResponse, id, model string) map[string]any {
	output := make([]any, 0, 3)

	if resp.ReasoningText != "" {
		output = append(output, map[string]any{
			"id":      newID("rs", time.Now().UnixNano()),
			"type":    "reasoning",
			"summary": []any{map[string]any{"type": "summary_text", "text": resp.ReasoningText}},
		})
	}
	if resp.Text != "" {
		output = append(output, map[string]any{
			"id":     newID("msg", time.Now().UnixNano()),
			"type":   "message",
			"status": "completed",
			"role":   "assistant",
			"content": []any{map[string]any{
				"type":        "output_text",
				"text":        resp.Text,
				"annotations": []any{},
			}},
		})
	}
	for _, tc := range resp.ToolCalls {
		output = append(output, map[string]any{
			"id":        newID("fc", time.Now().UnixNano()),
			"type":      "function_call",
			"status":    "completed",
			"call_id":   tc.ID,
			"name":      tc.Name,
			"arguments": tc.Arguments,
		})
	}

	u := resp.Usage
	if u.TotalTokens == 0 {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}

	return map[string]any{
		"id":                  id,
		"object":              "response",
		"created_at":          time.Now().Unix(),
		"status":              "completed",
		"model":               model,
		"output":              output,
		"parallel_tool_calls": true,
		"tool_choice":         "auto",
		"tools":               []any{},
		"usage": map[string]any{
			"input_tokens":          u.InputTokens,
			"output_tokens":         u.OutputTokens,
			"total_tokens":          u.TotalTokens,
			"input_tokens_details":  map[string]any{"cached_tokens": u.CachedTokens},
			"output_tokens_details": map[string]any{"reasoning_tokens": u.ReasoningTokens},
		},
	}
}

// --- Anthropic Messages ---

func renderAnthropicFull(resp ir.AggregatedResponse, id, model string) map[string]any {
	content := make([]any, 0, 3)

	if resp.ReasoningText != "" {
		content = append(content, map[string]any{
			"type":      "thinking",
			"thinking":  resp.ReasoningText,
			"signature": "",
		})
	}
	if resp.Text != "" {
		content = append(content, map[string]any{"type": "text", "text": resp.Text})
	}
	for _, tc := range resp.ToolCalls {
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    tc.ID,
			"name":  tc.Name,
			"input": parseArgumentsObject(tc.Arguments),
		})
	}

	return map[string]any{
		"id":            id,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       content,
		"stop_reason":   mapStopReason(resp.ResolveFinishReason()),
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
		},
	}
}

// parseArgumentsObject 把工具参数 JSON 字符串解析成对象。
// Anthropic 要求 tool_use.input 必须是对象而不是字符串。
func parseArgumentsObject(raw string) any {
	if raw == "" {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return map[string]any{}
	}
	if v == nil {
		return map[string]any{}
	}
	return v
}

// WriteError 按下游协议写出错误响应。
func WriteError(w http.ResponseWriter, proto ir.Protocol, err error) {
	ae := ir.AsAPIError(err)
	status := ae.StatusCode()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// 上游给了退避窗口时，把 Retry-After 透传给调用方（429/503 才有意义）。
	if (status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable) && ae.RetryAfter > 0 {
		secs := int(ae.RetryAfter / time.Second)
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
	w.WriteHeader(status)

	var body any
	if proto == ir.ProtocolAnthropic {
		body = map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    anthropicErrorType(ae.Code),
				"message": ae.PublicMessage(),
			},
		}
	} else if proto == ir.ProtocolGemini {
		body = map[string]any{
			"error": map[string]any{
				"code":    status,
				"message": ae.PublicMessage(),
				"status":  geminiErrorStatus(status),
			},
		}
	} else {
		body = map[string]any{
			"error": map[string]any{
				"message": ae.PublicMessage(),
				"type":    ae.Code,
				"code":    ae.Code,
			},
		}
	}
	_ = json.NewEncoder(w).Encode(body)
}

// geminiErrorStatus 把 HTTP 状态码映射成 Google API 的 status 枚举。
func geminiErrorStatus(status int) string {
	switch status {
	case 400:
		return "INVALID_ARGUMENT"
	case 401, 403:
		return "PERMISSION_DENIED"
	case 404:
		return "NOT_FOUND"
	case 429:
		return "RESOURCE_EXHAUSTED"
	case 503:
		return "UNAVAILABLE"
	default:
		return "INTERNAL"
	}
}

func anthropicErrorType(code string) string {
	switch code {
	case ir.ErrCodeBadRequest, ir.ErrCodeUnsupported:
		return "invalid_request_error"
	case ir.ErrCodeUpstreamAuth:
		return "authentication_error"
	case ir.ErrCodeUpstreamRateLimit:
		return "rate_limit_error"
	case ir.ErrCodeNoProvider, ir.ErrCodeNoAccount:
		return "overloaded_error"
	default:
		return "api_error"
	}
}
