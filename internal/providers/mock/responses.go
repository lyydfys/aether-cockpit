package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// nativeResponsesProvider 模拟一个"上游本身就是 OpenAI Responses 协议"的渠道。
//
// 它存在的唯一目的是验证 Fast-Path 零损直通：
// 直通时客户端能收到上游特有的非标准字段（x_vendor_meta），
// 而走 IR 转换路径时该字段必然丢失。这个差异就是"零损"的判据。
type nativeResponsesProvider struct{}

func (p *nativeResponsesProvider) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID:            "mock-responses",
		DisplayName:   "Mock Responses 原生渠道",
		Family:        "mock",
		RegionTag:     "LOCAL",
		BrandColor:    "#F59E0B",
		Description:   "模拟上游原生即 Responses 协议的渠道。下游同为 /v1/responses 时走 Fast-Path 零损直通，非标准字段会被完整保留。",
		NetworkPolicy: provider.NetPolicySystem,
		SupportedAuths: []provider.AuthMethod{
			provider.AuthManualInput,
		},
		AuthFormSchema: []provider.FormFieldSchema{
			{Key: "api_key", Label: "API Key", Type: "text", Placeholder: "任意字符串", Required: true},
		},
		Models: []ir.ModelMeta{
			{
				ID:            "mock-responses/codex-mini-latest",
				UpstreamModel: "codex-mini-latest",
				DisplayName:   "Codex Mini",
				ContextWindow: 200000,
				Capabilities:  ir.ModelCapabilities{ToolCall: true, Reasoning: true, Streaming: true},
			},
		},
		// 关键：声明上游原生支持 Responses 协议。
		NativeResponses:     true,
		RequiredMiddlewares: []string{"stateful_session"},
		Capabilities: provider.CapabilitySet{
			Chat:                  true,
			Quota:                 true,
			ResponsivePassthrough: true,
		},
	}
}

// ResponsesPassthrough 直接把原始请求体转发给"上游"，并返回原始 SSE 流。
//
// 真实实现里这一步是：改 Authorization 头、把 model 字段替换成上游模型名、
// 注入设备指纹头，然后 env.Client().Do()。
// 这里用本地构造的响应代替，但字节形态完全一致（含上游特有的非标准字段）。
func (p *nativeResponsesProvider) ResponsesPassthrough(ctx context.Context, env *provider.AccountEnv, rawReq []byte, upstreamModel string) (*http.Response, error) {
	var probe struct {
		Input string `json:"input"`
	}
	_ = json.Unmarshal(rawReq, &probe)

	echo := probe.Input
	if len([]rune(echo)) > 60 {
		echo = string([]rune(echo)[:60]) + "…"
	}
	if echo == "" {
		echo = "（空输入）"
	}

	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		write := func(event string, payload map[string]any) bool {
			// 刻意塞入一个非标准字段：只有原样透传才能到达客户端。
			payload["x_vendor_meta"] = "preserved-by-fastpath"
			b, err := json.Marshal(payload)
			if err != nil {
				return false
			}
			select {
			case <-ctx.Done():
				return false
			default:
			}
			if _, err := fmt.Fprintf(pw, "event: %s\ndata: %s\n\n", event, b); err != nil {
				return false
			}
			time.Sleep(12 * time.Millisecond)
			return true
		}

		respID := "resp_fast_" + shortHash(echo)
		base := map[string]any{
			"id":     respID,
			"object": "response",
			"status": "in_progress",
			"model":  upstreamModel,
		}

		if !write("response.created", map[string]any{"response": base, "type": "response.created", "sequence_number": 1}) {
			return
		}
		if !write("response.in_progress", map[string]any{"response": base, "type": "response.in_progress", "sequence_number": 2}) {
			return
		}

		itemID := "msg_" + shortHash(respID)
		if !write("response.output_item.added", map[string]any{
			"output_index": 0,
			"item": map[string]any{
				"id": itemID, "type": "message", "status": "in_progress",
				"role": "assistant", "content": []any{},
			},
			"type": "response.output_item.added", "sequence_number": 3,
		}) {
			return
		}

		text := "我是原生 Responses 渠道（直通），收到：" + echo
		for i, chunk := range chunkString(text, 8) {
			if !write("response.output_text.delta", map[string]any{
				"item_id": itemID, "output_index": 0, "content_index": 0,
				"delta": chunk,
				"type":  "response.output_text.delta", "sequence_number": 10 + i,
			}) {
				return
			}
		}

		finalItem := map[string]any{
			"id": itemID, "type": "message", "status": "completed", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
		}
		write("response.output_item.done", map[string]any{
			"output_index": 0, "item": finalItem,
			"type": "response.output_item.done", "sequence_number": 99,
		})

		completed := map[string]any{
			"id": respID, "object": "response", "status": "completed", "model": upstreamModel,
			"output":  []any{finalItem},
			"usage":   map[string]any{"input_tokens": 10, "output_tokens": 20, "total_tokens": 30},
			"// note": "上游特有的未来字段，比如 encrypted_content，在此完整保留",
		}
		write("response.completed", map[string]any{
			"response": completed,
			"type":     "response.completed", "sequence_number": 100,
		})
	}()

	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":  []string{"text/event-stream; charset=utf-8"},
			"Cache-Control": []string{"no-cache"},
			// 这个头也只有直通才会传到客户端。
			"X-Mock-Upstream-Mode": []string{"native-passthrough"},
		},
		Body: pr,
	}, nil
}

// ChatStream 是 IR 路径的兜底实现。
//
// 当下游不是 Responses 协议（比如用 /v1/messages），或者带了
// previous_response_id 需要状态缝合时，请求会走这里。
// 注意它产生的输出里没有 x_vendor_meta —— 这正是两条路径的区分点。
func (p *nativeResponsesProvider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	reply, _ := buildReply(req, true)

	out := make(chan ir.UnifiedStreamEvent, 32)
	go func() {
		defer close(out)
		emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventStart, ResponseID: "native_" + shortHash(req.SessionKey)})

		for _, chunk := range chunkString(reply, 10) {
			if !emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: chunk}) {
				return
			}
			sleep(ctx, 10*time.Millisecond)
		}
		emit(ctx, out, ir.UnifiedStreamEvent{
			Type:  ir.EventUsage,
			Usage: &ir.Usage{InputTokens: estimateTokens(req), OutputTokens: len([]rune(reply)) / 3},
		})
		emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: "stop"})
	}()
	return out, nil
}

// QueryQuota 模拟额度查询。
func (p *nativeResponsesProvider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	return &provider.QuotaStatus{Total: 500, Remaining: 420, Unit: "次"}, nil
}
