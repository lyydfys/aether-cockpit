package transducer

import (
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
)

// ResponsesTransducer 输出 OpenAI /v1/responses 的具名事件流。
//
// 协议严格程度远高于 Chat：客户端（Codex CLI / Agents SDK）会按事件树校验，
// 如果先收到 output_text.delta 而没有前置的 output_item.added + content_part.added，
// 会直接解析失败。因此这里必须维护完整的 item 生命周期状态机。
type ResponsesTransducer struct {
	w         *sseWriter
	id        string
	model     string
	createdAt int64
	seq       seqCounter

	// --- 当前活跃 item 的状态 ---
	curType   ir.ItemType
	curID     string
	curIndex  int
	curCallID string
	curName   string
	curBuf    strings.Builder

	// outputIndex 指向上一个已打开（或已关闭）的 item 下标。
	outputIndex int
	// items 保存所有已闭合的 item，用于 response.completed 的 output 数组。
	items []map[string]any

	finished bool
	usage    ir.Usage
}

// NewResponsesTransducer 创建 Responses 出站转换器。
func NewResponsesTransducer(w http.ResponseWriter, responseID, model string) (*ResponsesTransducer, error) {
	sw, err := newSSEWriter(w)
	if err != nil {
		return nil, err
	}
	return &ResponsesTransducer{
		w:           sw,
		id:          responseID,
		model:       model,
		createdAt:   time.Now().Unix(),
		outputIndex: -1,
	}, nil
}

// ResponseID 返回本次响应的 id，网关需要把它写回状态存储。
func (t *ResponsesTransducer) ResponseID() string { return t.id }

// Stream 消费上游事件并实时写出。
func (t *ResponsesTransducer) Stream(in <-chan ir.UnifiedStreamEvent) error {
	t.emitCreated()

	for ev := range in {
		switch ev.Type {

		case ir.EventStart:
			// 上游会话 ID 与 response id 是两套东西，这里不做映射。

		case ir.EventReasoningDelta:
			t.ensureItem(ir.ItemTypeReasoning, "", "")
			t.curBuf.WriteString(ev.TextDelta)
			t.emit("response.reasoning_summary_text.delta", map[string]any{
				"item_id":       t.curID,
				"output_index":  t.curIndex,
				"summary_index": 0,
				"delta":         ev.TextDelta,
			})

		case ir.EventTextDelta:
			t.ensureItem(ir.ItemTypeMessage, "", "")
			t.curBuf.WriteString(ev.TextDelta)
			t.emit("response.output_text.delta", map[string]any{
				"item_id":       t.curID,
				"output_index":  t.curIndex,
				"content_index": 0,
				"delta":         ev.TextDelta,
			})

		case ir.EventToolCallStart:
			// 每个工具调用都是独立的 function_call item，必须强制切换。
			t.closeCurrentItem()
			t.ensureItem(ir.ItemTypeFunctionCall, ev.ToolCallID, ev.ToolName)

		case ir.EventToolCallDelta:
			if t.curType != ir.ItemTypeFunctionCall {
				t.closeCurrentItem()
				t.ensureItem(ir.ItemTypeFunctionCall, ev.ToolCallID, ev.ToolName)
			}
			t.curBuf.WriteString(ev.ArgsDelta)
			t.emit("response.function_call_arguments.delta", map[string]any{
				"item_id":      t.curID,
				"output_index": t.curIndex,
				"delta":        ev.ArgsDelta,
			})

		case ir.EventUsage:
			if ev.Usage != nil {
				t.usage = *ev.Usage
			}

		case ir.EventError:
			t.closeCurrentItem()
			ae := ir.AsAPIError(ev.Err)
			t.emit("response.failed", map[string]any{
				"response": map[string]any{
					"id":     t.id,
					"object": "response",
					"status": "failed",
					"model":  t.model,
					"error": map[string]any{
						"code":    ae.Code,
						"message": ae.PublicMessage(),
					},
				},
			})
			return t.w.Err()

		case ir.EventDone:
			// 结束动作统一在循环外处理。
		}
	}

	t.closeCurrentItem()
	t.emitCompleted()
	return t.w.Err()
}

// emitCreated 发送 response.created + response.in_progress。
func (t *ResponsesTransducer) emitCreated() {
	base := t.baseResponse("in_progress")
	base["output"] = []any{}
	t.emit("response.created", map[string]any{"response": base})
	t.emit("response.in_progress", map[string]any{"response": base})
}

// emitCompleted 发送收尾事件。
func (t *ResponsesTransducer) emitCompleted() {
	if t.finished {
		return
	}
	t.finished = true
	resp := t.baseResponse("completed")
	resp["output"] = t.items
	if resp["output"] == nil {
		resp["output"] = []any{}
	}
	u := t.usage
	if u.TotalTokens == 0 {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}
	resp["usage"] = map[string]any{
		"input_tokens":  u.InputTokens,
		"output_tokens": u.OutputTokens,
		"total_tokens":  u.TotalTokens,
		"input_tokens_details": map[string]any{
			"cached_tokens": u.CachedTokens,
		},
		"output_tokens_details": map[string]any{
			"reasoning_tokens": u.ReasoningTokens,
		},
	}
	t.emit("response.completed", map[string]any{"response": resp})
}

func (t *ResponsesTransducer) baseResponse(status string) map[string]any {
	return map[string]any{
		"id":                  t.id,
		"object":              "response",
		"created_at":          t.createdAt,
		"status":              status,
		"model":               t.model,
		"parallel_tool_calls": true,
		"tool_choice":         "auto",
		"tools":               []any{},
	}
}

// emit 写出一个带 sequence_number 的具名事件。
func (t *ResponsesTransducer) emit(eventType string, payload map[string]any) {
	payload["type"] = eventType
	payload["sequence_number"] = t.seq.next()
	t.w.writeEvent(eventType, payload)
}

// ensureItem 在需要时切换活跃 item。
//
// sameTypeKeep 语义：文本与思考是"同类可续写"，连续 delta 不该重开 item；
// 而 function_call 每次都是新 item，所以强制重开。
func (t *ResponsesTransducer) ensureItem(target ir.ItemType, callID, name string) {
	if t.curType == target {
		if target != ir.ItemTypeFunctionCall {
			return
		}
		// 同一个 function_call 的 Start 已处理过，Delta 阶段不应重开。
		return
	}
	t.closeCurrentItem()
	t.openItem(target, callID, name)
}

// openItem 打开一个新的 output item，并按类型补全其生命周期起点事件。
func (t *ResponsesTransducer) openItem(target ir.ItemType, callID, name string) {
	t.curType = target
	t.curBuf.Reset()
	t.outputIndex++
	t.curIndex = t.outputIndex

	switch target {
	case ir.ItemTypeReasoning:
		t.curID = newID("rs", time.Now().UnixNano())
		t.emit("response.output_item.added", map[string]any{
			"output_index": t.curIndex,
			"item": map[string]any{
				"id":      t.curID,
				"type":    "reasoning",
				"summary": []any{},
			},
		})
		// 补齐 part 生命周期：缺了这两个事件，部分客户端会把 summary 当作空。
		t.emit("response.reasoning_summary_part.added", map[string]any{
			"item_id":       t.curID,
			"output_index":  t.curIndex,
			"summary_index": 0,
			"part":          map[string]any{"type": "summary_text", "text": ""},
		})

	case ir.ItemTypeMessage:
		t.curID = newID("msg", time.Now().UnixNano())
		t.emit("response.output_item.added", map[string]any{
			"output_index": t.curIndex,
			"item": map[string]any{
				"id":      t.curID,
				"type":    "message",
				"status":  "in_progress",
				"role":    "assistant",
				"content": []any{},
			},
		})
		t.emit("response.content_part.added", map[string]any{
			"item_id":       t.curID,
			"output_index":  t.curIndex,
			"content_index": 0,
			"part":          map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
		})

	case ir.ItemTypeFunctionCall:
		t.curID = newID("fc", time.Now().UnixNano())
		t.curCallID = callID
		t.curName = name
		t.emit("response.output_item.added", map[string]any{
			"output_index": t.curIndex,
			"item": map[string]any{
				"id":        t.curID,
				"type":      "function_call",
				"status":    "in_progress",
				"call_id":   callID,
				"name":      name,
				"arguments": "",
			},
		})
	}
}

// closeCurrentItem 闭合当前 item，发送 done 事件并把最终形态存入 items。
func (t *ResponsesTransducer) closeCurrentItem() {
	if t.curType == "" {
		return
	}
	full := t.curBuf.String()
	var final map[string]any

	switch t.curType {
	case ir.ItemTypeMessage:
		t.emit("response.output_text.done", map[string]any{
			"item_id":       t.curID,
			"output_index":  t.curIndex,
			"content_index": 0,
			"text":          full,
		})
		part := map[string]any{"type": "output_text", "text": full, "annotations": []any{}}
		t.emit("response.content_part.done", map[string]any{
			"item_id":       t.curID,
			"output_index":  t.curIndex,
			"content_index": 0,
			"part":          part,
		})
		final = map[string]any{
			"id":      t.curID,
			"type":    "message",
			"status":  "completed",
			"role":    "assistant",
			"content": []any{part},
		}

	case ir.ItemTypeReasoning:
		t.emit("response.reasoning_summary_text.done", map[string]any{
			"item_id":       t.curID,
			"output_index":  t.curIndex,
			"summary_index": 0,
			"text":          full,
		})
		part := map[string]any{"type": "summary_text", "text": full}
		t.emit("response.reasoning_summary_part.done", map[string]any{
			"item_id":       t.curID,
			"output_index":  t.curIndex,
			"summary_index": 0,
			"part":          part,
		})
		final = map[string]any{
			"id":      t.curID,
			"type":    "reasoning",
			"summary": []any{part},
		}

	case ir.ItemTypeFunctionCall:
		t.emit("response.function_call_arguments.done", map[string]any{
			"item_id":      t.curID,
			"output_index": t.curIndex,
			"arguments":    full,
		})
		final = map[string]any{
			"id":        t.curID,
			"type":      "function_call",
			"status":    "completed",
			"call_id":   t.curCallID,
			"name":      t.curName,
			"arguments": full,
		}
	}

	t.emit("response.output_item.done", map[string]any{
		"output_index": t.curIndex,
		"item":         final,
	})
	t.items = append(t.items, final)

	t.curType = ""
	t.curBuf.Reset()
}
