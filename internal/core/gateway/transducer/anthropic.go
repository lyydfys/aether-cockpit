package transducer

import (
	"net/http"
	"strings"

	"aether-cockpit/internal/core/ir"
)

// AnthropicTransducer 输出 Anthropic /v1/messages 的 SSE 流。
//
// 与 Chat 的差别：内容被组织成"带索引的 content_block"，每个 block 有独立的
// start / delta / stop 三段生命周期。Claude Code 会严格按索引重建消息，
// 索引错乱会导致工具调用无法被识别。
type AnthropicTransducer struct {
	w     *sseWriter
	id    string
	model string
	seq   seqCounter

	// blockIndex 是下一个 content block 的下标。
	blockIndex int
	// curBlock 为 "" 表示当前无活跃 block。
	curBlock string
	curIndex int

	inputTokens  int
	outputTokens int
	finished     bool
}

// NewAnthropicTransducer 创建 Anthropic 出站转换器。
func NewAnthropicTransducer(w http.ResponseWriter, responseID, model string) (*AnthropicTransducer, error) {
	sw, err := newSSEWriter(w)
	if err != nil {
		return nil, err
	}
	return &AnthropicTransducer{
		w:          sw,
		id:         responseID,
		model:      model,
		blockIndex: -1,
	}, nil
}

// Stream 消费上游事件并实时写出。
func (t *AnthropicTransducer) Stream(in <-chan ir.UnifiedStreamEvent) error {
	// message_start 必须最先发出，Claude Code 依赖它初始化消息。
	t.emit("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            t.id,
			"type":          "message",
			"role":          "assistant",
			"model":         t.model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		},
	})

	for ev := range in {
		switch ev.Type {

		case ir.EventStart:
			// 上游会话 id 不对外暴露。

		case ir.EventReasoningDelta:
			t.ensureBlock("thinking")
			t.emit("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": t.curIndex,
				"delta": map[string]any{
					"type":     "thinking_delta",
					"thinking": ev.TextDelta,
				},
			})

		case ir.EventTextDelta:
			t.ensureBlock("text")
			t.emit("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": t.curIndex,
				"delta": map[string]any{
					"type": "text_delta",
					"text": ev.TextDelta,
				},
			})

		case ir.EventToolCallStart:
			t.closeBlock()
			t.openToolBlock(ev.ToolCallID, ev.ToolName)

		case ir.EventToolCallDelta:
			if t.curBlock != "tool_use" {
				t.closeBlock()
				t.openToolBlock(ev.ToolCallID, ev.ToolName)
			}
			t.emit("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": t.curIndex,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": ev.ArgsDelta,
				},
			})

		case ir.EventUsage:
			if ev.Usage != nil {
				if ev.Usage.InputTokens > 0 {
					t.inputTokens = ev.Usage.InputTokens
				}
				if ev.Usage.OutputTokens > 0 {
					t.outputTokens = ev.Usage.OutputTokens
				}
			}

		case ir.EventError:
			t.closeBlock()
			ae := ir.AsAPIError(ev.Err)
			t.emit("error", map[string]any{
				"type": "error",
				"error": map[string]any{
					"type":    "api_error",
					"message": ae.PublicMessage(),
				},
			})
			return t.w.Err()

		case ir.EventDone:
			// 收尾统一在循环外处理。
		}
	}

	t.closeBlock()
	t.finish("")
	return t.w.Err()
}

// ensureBlock 在类型切换时关闭旧 block、开启新 block。
func (t *AnthropicTransducer) ensureBlock(kind string) {
	if t.curBlock == kind {
		return
	}
	t.closeBlock()

	t.blockIndex++
	t.curIndex = t.blockIndex
	t.curBlock = kind

	var block map[string]any
	switch kind {
	case "thinking":
		block = map[string]any{"type": "thinking", "thinking": "", "signature": ""}
	case "text":
		block = map[string]any{"type": "text", "text": ""}
	}
	t.emit("content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         t.curIndex,
		"content_block": block,
	})
}

func (t *AnthropicTransducer) openToolBlock(callID, name string) {
	t.blockIndex++
	t.curIndex = t.blockIndex
	t.curBlock = "tool_use"
	t.emit("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": t.curIndex,
		"content_block": map[string]any{
			"type":  "tool_use",
			"id":    callID,
			"name":  name,
			"input": map[string]any{},
		},
	})
}

// closeBlock 闭合当前 block。
//
// 关键点：thinking block 结束时必须补一个 signature_delta，
// 否则下一轮把该 block 回传给上游会因为"缺少签名"被拒绝。
func (t *AnthropicTransducer) closeBlock() {
	if t.curBlock == "" {
		return
	}
	if t.curBlock == "thinking" {
		t.emit("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": t.curIndex,
			"delta": map[string]any{
				"type":      "signature_delta",
				"signature": "",
			},
		})
	}
	t.emit("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": t.curIndex,
	})
	t.curBlock = ""
}

// finish 发送 message_delta + message_stop。
func (t *AnthropicTransducer) finish(reason string) {
	if t.finished {
		return
	}
	t.finished = true
	t.emit("message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   mapStopReason(reason),
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"input_tokens":  t.inputTokens,
			"output_tokens": t.outputTokens,
		},
	})
	t.emit("message_stop", map[string]any{"type": "message_stop"})
}

func (t *AnthropicTransducer) emit(eventType string, payload map[string]any) {
	t.w.writeEvent(eventType, payload)
}

// mapStopReason 把内部结束原因映射为 Anthropic 的枚举。
func mapStopReason(reason string) string {
	switch strings.ToLower(reason) {
	case "tool_calls", "tool_use":
		return "tool_use"
	case "length", "max_tokens":
		return "max_tokens"
	case "content_filter":
		return "end_turn"
	default:
		return "end_turn"
	}
}
