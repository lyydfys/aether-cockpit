package transducer

import (
	"net/http"
	"time"

	"aether-cockpit/internal/core/ir"
)

// ChatTransducer 输出 OpenAI /v1/chat/completions 的 SSE 流。
//
// 这是最宽松的协议：没有具名事件，纯 data 增量，客户端容错度高。
// 因此它也最适合作为"调试基准"——先让 Chat 通了，再验证另外两个。
type ChatTransducer struct {
	w        *sseWriter
	id       string
	model    string
	created  int64
	roleSent bool

	// toolIndex 是当前工具调用在 tool_calls 数组中的位置。
	toolIndex int
	toolOpen  bool
	finished  bool
	// pendingUsage 缓存中途上报的 usage，必须等 finish_reason 之后再发，
	// 否则部分严格遵守顺序的客户端会把 usage 当成最后一帧而丢掉结束标记。
	pendingUsage *ir.Usage
}

// NewChatTransducer 创建 Chat 出站转换器。
func NewChatTransducer(w http.ResponseWriter, responseID, model string) (*ChatTransducer, error) {
	sw, err := newSSEWriter(w)
	if err != nil {
		return nil, err
	}
	return &ChatTransducer{
		w:         sw,
		id:        responseID,
		model:     model,
		created:   time.Now().Unix(),
		toolIndex: -1,
	}, nil
}

// Stream 消费上游事件并实时写出。
func (t *ChatTransducer) Stream(in <-chan ir.UnifiedStreamEvent) error {
	for ev := range in {
		switch ev.Type {

		case ir.EventStart:
			// Chat 协议没有专门的"开始"事件，忽略即可。

		case ir.EventReasoningDelta:
			t.ensureRole()
			t.chunk(map[string]any{"reasoning_content": ev.TextDelta}, nil)

		case ir.EventTextDelta:
			t.ensureRole()
			t.chunk(map[string]any{"content": ev.TextDelta}, nil)

		case ir.EventToolCallStart:
			t.ensureRole()
			// 上游可能连续发多个 Start，每次都要推进 index。
			if t.toolOpen {
				t.toolIndex++
			} else {
				t.toolIndex = 0
				t.toolOpen = true
			}
			t.chunk(map[string]any{
				"tool_calls": []any{map[string]any{
					"index": t.toolIndex,
					"id":    ev.ToolCallID,
					"type":  "function",
					"function": map[string]any{
						"name":      ev.ToolName,
						"arguments": "",
					},
				}},
			}, nil)

		case ir.EventToolCallDelta:
			if !t.toolOpen {
				t.toolIndex = 0
				t.toolOpen = true
			}
			t.chunk(map[string]any{
				"tool_calls": []any{map[string]any{
					"index": t.toolIndex,
					"function": map[string]any{
						"arguments": ev.ArgsDelta,
					},
				}},
			}, nil)

		case ir.EventUsage:
			if ev.Usage != nil {
				u := *ev.Usage
				t.pendingUsage = &u
			}

		case ir.EventError:
			t.writeError(ev.Err)
			return t.w.Err()

		case ir.EventDone:
			t.finish(ev.FinishReason)
		}
	}

	// 上游没有发 Done 就关流（渠道实现不规范）时兜底收尾，避免客户端挂死。
	if !t.finished {
		t.finish("")
	}
	t.w.writeRaw("[DONE]")
	return t.w.Err()
}

func (t *ChatTransducer) ensureRole() {
	if t.roleSent {
		return
	}
	t.roleSent = true
	t.chunk(map[string]any{"role": "assistant", "content": ""}, nil)
}

// chunk 构造并写出一个标准 chat.completion.chunk。
func (t *ChatTransducer) chunk(delta map[string]any, finishReason *string) {
	t.w.writeEvent("", map[string]any{
		"id":      t.id,
		"object":  "chat.completion.chunk",
		"created": t.created,
		"model":   t.model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         delta,
			"finish_reason": finishReason,
		}},
	})
}

func (t *ChatTransducer) finish(reason string) {
	if t.finished {
		return
	}
	t.finished = true
	if reason == "" {
		if t.toolOpen {
			reason = "tool_calls"
		} else {
			reason = "stop"
		}
	}
	// 顺序严格遵循 OpenAI 规范：finish_reason 先到，usage 收尾，最后 [DONE]。
	t.chunk(map[string]any{}, &reason)
	if t.pendingUsage != nil {
		t.writeUsage(*t.pendingUsage)
	}
}

func (t *ChatTransducer) writeUsage(u ir.Usage) {
	t.w.writeEvent("", map[string]any{
		"id":      t.id,
		"object":  "chat.completion.chunk",
		"created": t.created,
		"model":   t.model,
		"choices": []any{},
		"usage": map[string]any{
			"prompt_tokens":     u.InputTokens,
			"completion_tokens": u.OutputTokens,
			"total_tokens":      u.TotalTokens,
		},
	})
}

func (t *ChatTransducer) writeError(err error) {
	ae := ir.AsAPIError(err)
	t.w.writeRaw(`{"error":{"message":` + jsonQuote(ae.PublicMessage()) + `,"type":"` + ae.Code + `"}}`)
}
