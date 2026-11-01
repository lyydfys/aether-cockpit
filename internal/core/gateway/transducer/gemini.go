package transducer

import (
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
)

// GeminiTransducer 把 IR 事件流升维成 Gemini 的 streamGenerateContent SSE。
//
// Gemini 的流没有"事件名"，每一帧都是一个完整的 GenerateContentResponse：
// 文本增量就是一帧 {"candidates":[{"content":{"parts":[{"text":"…"}]}}]}；
// 思考增量带 "thought": true；函数调用要求一次性给出完整的 args 对象，
// 所以工具调用的增量在这里攒齐后才发一帧。
type GeminiTransducer struct {
	w   http.ResponseWriter
	sse *sseWriter

	model  string
	respID string

	// 工具调用攒批：id -> 累积状态。
	calls  []geminiCallAcc
	usage  *ir.Usage
	finish string
}

type geminiCallAcc struct {
	id   string
	name string
	args string
}

// NewGeminiTransducer 创建流式转写器。
func NewGeminiTransducer(w http.ResponseWriter, respID, model string) (*GeminiTransducer, error) {
	sse, err := newSSEWriter(w)
	if err != nil {
		return nil, err
	}
	return &GeminiTransducer{w: w, sse: sse, model: model, respID: respID}, nil
}

// Stream 消费事件流并按 Gemini 协议输出，直到流结束。
func (t *GeminiTransducer) Stream(ch <-chan ir.UnifiedStreamEvent) error {
	for ev := range ch {
		switch ev.Type {
		case ir.EventTextDelta:
			if ev.TextDelta == "" {
				continue
			}
			t.emitChunk(t.part(map[string]any{"text": ev.TextDelta}), "")

		case ir.EventReasoningDelta:
			if ev.TextDelta == "" {
				continue
			}
			t.emitChunk(t.part(map[string]any{"text": ev.TextDelta, "thought": true}), "")

		case ir.EventToolCallStart:
			t.calls = append(t.calls, geminiCallAcc{id: ev.ToolCallID, name: ev.ToolName})

		case ir.EventToolCallDelta:
			if len(t.calls) > 0 {
				t.calls[len(t.calls)-1].args += ev.ArgsDelta
			}

		case ir.EventUsage:
			if ev.Usage != nil {
				t.usage = ev.Usage
			}

		case ir.EventDone:
			t.finish = "stop"
			t.emitFinal()
			return t.sse.Err()

		case ir.EventError:
			// 流中途出错：Gemini 用 error 对象表达。已经发过的增量无法撤回，
			// 只能把错误信息补一帧出去。
			msg := "上游错误"
			if ev.Err != nil {
				msg = ev.Err.Error()
			}
			t.sse.writeEvent("", map[string]any{
				"error": map[string]any{"code": 502, "message": msg, "status": "UNAVAILABLE"},
			})
			return t.sse.Err()
		}
	}
	// 上游没发 done 就关闭了流：按正常收尾处理，客户端至少能拿到已攒的内容。
	t.finish = "stop"
	t.emitFinal()
	return t.sse.Err()
}

func (t *GeminiTransducer) part(p map[string]any) []map[string]any {
	return []map[string]any{p}
}

// emitChunk 发一帧 GenerateContentResponse。
func (t *GeminiTransducer) emitChunk(parts []map[string]any, finish string) {
	cand := map[string]any{
		"content": map[string]any{"role": "model", "parts": parts},
		"index":   0,
	}
	if finish != "" {
		cand["finishReason"] = finish
	}
	body := map[string]any{
		"candidates":   []any{cand},
		"modelVersion": t.model,
	}
	if t.respID != "" {
		body["responseId"] = t.respID
	}
	t.sse.writeEvent("", body)
}

// emitFinal 发出收尾帧：工具调用（若有）+ finishReason + usageMetadata。
func (t *GeminiTransducer) emitFinal() {
	parts := make([]map[string]any, 0, len(t.calls))
	for _, c := range t.calls {
		parts = append(parts, map[string]any{
			"functionCall": map[string]any{
				"name": c.name,
				"args": parseArgumentsObject(c.args),
			},
		})
	}
	finish := mapGeminiFinish(t.finish)
	if len(parts) > 0 {
		t.emitChunk(parts, finish)
	} else {
		// 纯文本收尾帧：给一个空 text part，保证 candidates.content 合法。
		t.emitChunk([]map[string]any{{"text": ""}}, finish)
	}

	body := map[string]any{
		"candidates": []any{map[string]any{
			"content":      map[string]any{"role": "model", "parts": []map[string]any{}},
			"finishReason": finish,
			"index":        0,
		}},
		"modelVersion": t.model,
	}
	if t.usage != nil {
		body["usageMetadata"] = geminiUsage(t.usage)
	}
	t.sse.writeEvent("", body)
}

func geminiUsage(u *ir.Usage) map[string]any {
	out := map[string]any{
		"promptTokenCount":     u.InputTokens,
		"candidatesTokenCount": u.OutputTokens,
		"totalTokenCount":      u.TotalTokens,
	}
	if out["totalTokenCount"].(int) == 0 {
		out["totalTokenCount"] = u.InputTokens + u.OutputTokens
	}
	if u.CachedTokens > 0 {
		out["cachedContentTokenCount"] = u.CachedTokens
	}
	if u.ReasoningTokens > 0 {
		out["thoughtsTokenCount"] = u.ReasoningTokens
	}
	return out
}

// mapGeminiFinish 把 IR finish reason 映射成 Gemini 的 finishReason。
func mapGeminiFinish(reason string) string {
	switch strings.ToLower(reason) {
	case "length", "max_tokens":
		return "MAX_TOKENS"
	case "content_filter", "safety":
		return "SAFETY"
	default:
		return "STOP"
	}
}

// --- 非流式 ---

// renderGeminiFull 渲染 :generateContent 的完整响应。
func renderGeminiFull(resp ir.AggregatedResponse, responseID, model string) map[string]any {
	parts := make([]map[string]any, 0, 2+len(resp.ToolCalls))
	if resp.ReasoningText != "" {
		parts = append(parts, map[string]any{"text": resp.ReasoningText, "thought": true})
	}
	if resp.Text != "" {
		parts = append(parts, map[string]any{"text": resp.Text})
	}
	for _, tc := range resp.ToolCalls {
		parts = append(parts, map[string]any{
			"functionCall": map[string]any{
				"name": tc.Name,
				"args": parseArgumentsObject(tc.Arguments),
			},
		})
	}
	if len(parts) == 0 {
		parts = append(parts, map[string]any{"text": ""})
	}

	u := resp.Usage
	if u.TotalTokens == 0 {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}

	body := map[string]any{
		"candidates": []any{map[string]any{
			"content":      map[string]any{"role": "model", "parts": parts},
			"finishReason": mapGeminiFinish(resp.ResolveFinishReason()),
			"index":        0,
		}},
		"modelVersion": model,
		"usageMetadata": map[string]any{
			"promptTokenCount":     u.InputTokens,
			"candidatesTokenCount": u.OutputTokens,
			"totalTokenCount":      u.TotalTokens,
		},
	}
	if responseID != "" {
		body["responseId"] = responseID
	}
	body["createTime"] = time.Now().UTC().Format(time.RFC3339Nano)
	return body
}
