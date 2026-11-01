package ir

// ToolCall 聚合后的完整工具调用。
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// AggregatedResponse 是把事件流收集完后得到的完整响应。
//
// 非流式请求需要一次性渲染完整 JSON；同时它也是 Responses 协议
// response.completed 事件里 output 数组的数据来源。
type AggregatedResponse struct {
	ResponseID    string     `json:"response_id"`
	Model         string     `json:"model"`
	ReasoningText string     `json:"reasoning_text,omitempty"`
	Text          string     `json:"text"`
	ToolCalls     []ToolCall `json:"tool_calls,omitempty"`
	Usage         Usage      `json:"usage"`
	FinishReason  string     `json:"finish_reason"`
	// ThoughtSignatures 按工具调用顺序记录思考签名，供状态缝合层落盘。
	ThoughtSignatures map[string]string `json:"thought_signatures,omitempty"`
}

// HasToolCalls 判断是否产生了工具调用。
func (a *AggregatedResponse) HasToolCalls() bool { return len(a.ToolCalls) > 0 }

// ResolveFinishReason 在未显式给出结束原因时按内容推断。
func (a *AggregatedResponse) ResolveFinishReason() string {
	if a.FinishReason != "" {
		return a.FinishReason
	}
	if a.HasToolCalls() {
		return "tool_calls"
	}
	return "stop"
}

// Accumulator 增量聚合事件流。
//
// 单独拆出来是因为它同时被两处使用：
//   - 非流式请求（一次性渲染完整 JSON）
//   - 流式请求结束时构造 response.completed / message_delta 的汇总负载
type Accumulator struct {
	resp AggregatedResponse

	// 当前工具调用序号 -> 在切片中的下标
	activeToolIdx int
	toolOpen      bool
}

// NewAccumulator 创建聚合器。
func NewAccumulator() *Accumulator {
	return &Accumulator{activeToolIdx: -1}
}

// Push 消费一个事件。
func (a *Accumulator) Push(ev UnifiedStreamEvent) {
	switch ev.Type {
	case EventStart:
		if ev.ResponseID != "" {
			a.resp.ResponseID = ev.ResponseID
		}

	case EventReasoningDelta:
		a.resp.ReasoningText += ev.TextDelta

	case EventTextDelta:
		a.resp.Text += ev.TextDelta

	case EventToolCallStart:
		a.toolOpen = true
		a.activeToolIdx = len(a.resp.ToolCalls)
		a.resp.ToolCalls = append(a.resp.ToolCalls, ToolCall{
			ID:   ev.ToolCallID,
			Name: ev.ToolName,
		})

	case EventToolCallDelta:
		if a.activeToolIdx >= 0 && a.activeToolIdx < len(a.resp.ToolCalls) {
			a.resp.ToolCalls[a.activeToolIdx].Arguments += ev.ArgsDelta
		} else {
			// 上游没发 Start 直接发 Delta，兜底补一个。
			a.activeToolIdx = len(a.resp.ToolCalls)
			a.resp.ToolCalls = append(a.resp.ToolCalls, ToolCall{
				ID:        ev.ToolCallID,
				Name:      ev.ToolName,
				Arguments: ev.ArgsDelta,
			})
		}

	case EventUsage:
		if ev.Usage != nil {
			a.resp.Usage = mergeUsage(a.resp.Usage, *ev.Usage)
		}

	case EventDone:
		if ev.FinishReason != "" {
			a.resp.FinishReason = ev.FinishReason
		}

	case EventError:
		// 错误由调用方从 channel 之外的路径获知，这里不聚合。
	}

	if ev.ThoughtSignature != "" && ev.ToolCallID != "" {
		if a.resp.ThoughtSignatures == nil {
			a.resp.ThoughtSignatures = make(map[string]string, 2)
		}
		a.resp.ThoughtSignatures[ev.ToolCallID] = ev.ThoughtSignature
	}
}

// Result 返回聚合结果。
func (a *Accumulator) Result() AggregatedResponse {
	r := a.resp
	r.FinishReason = r.ResolveFinishReason()
	if r.Usage.TotalTokens == 0 {
		r.Usage.TotalTokens = r.Usage.InputTokens + r.Usage.OutputTokens
	}
	return r
}

// mergeUsage 合并多次上报的 usage（有些上游会在中途和末尾各报一次）。
func mergeUsage(dst, src Usage) Usage {
	if src.InputTokens > 0 {
		dst.InputTokens = src.InputTokens
	}
	if src.OutputTokens > 0 {
		dst.OutputTokens = src.OutputTokens
	}
	if src.CachedTokens > 0 {
		dst.CachedTokens = src.CachedTokens
	}
	if src.ReasoningTokens > 0 {
		dst.ReasoningTokens = src.ReasoningTokens
	}
	if src.TotalTokens > 0 {
		dst.TotalTokens = src.TotalTokens
	}
	return dst
}

// Collect 把整个事件流收集为聚合结果。用于非流式请求。
func Collect(in <-chan UnifiedStreamEvent) (AggregatedResponse, error) {
	acc := NewAccumulator()
	for ev := range in {
		if ev.Type == EventError {
			if ev.Err != nil {
				return acc.Result(), ev.Err
			}
			return acc.Result(), NewAPIError(ErrCodeUpstreamFailed, "上游流式返回错误但未携带详情")
		}
		acc.Push(ev)
	}
	return acc.Result(), nil
}
