package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/core/gateway/inbound"
	"aether-cockpit/internal/core/gateway/transducer"
	"aether-cockpit/internal/core/ir"
)

func TestGeminiInboundParse(t *testing.T) {
	body := `{
		"systemInstruction": {"parts": [{"text": "你是一个助手"}]},
		"contents": [
			{"role": "user", "parts": [
				{"text": "看这张图"},
				{"inlineData": {"mimeType": "image/png", "data": "aGk="}}
			]},
			{"role": "model", "parts": [{"functionCall": {"name": "get_weather", "args": {"city": "北京"}}}]},
			{"role": "user", "parts": [{"functionResponse": {"name": "get_weather", "response": {"temp": 25}}}]}
		],
		"tools": [{"functionDeclarations": [{"name": "get_weather", "description": "查天气", "parameters": {"type": "object"}}]}],
		"generationConfig": {"temperature": 0.5, "maxOutputTokens": 1024, "stopSequences": ["STOP"]}
	}`

	req, err := inbound.ParseGemini([]byte(body), "req-1", "models/gemini-2.5-pro")
	if err != nil {
		t.Fatalf("ParseGemini: %v", err)
	}
	if req.Model != "gemini-2.5-pro" {
		t.Errorf("Model = %q", req.Model)
	}
	if req.DownstreamProtocol != ir.ProtocolGemini {
		t.Errorf("Protocol = %q", req.DownstreamProtocol)
	}
	if req.Instructions != "你是一个助手" {
		t.Errorf("Instructions = %q", req.Instructions)
	}
	if req.Temperature == nil || *req.Temperature != 0.5 {
		t.Error("Temperature 未解析")
	}
	if req.MaxOutputTokens == nil || *req.MaxOutputTokens != 1024 {
		t.Error("MaxOutputTokens 未解析")
	}
	if len(req.StopSequences) != 1 || req.StopSequences[0] != "STOP" {
		t.Errorf("StopSequences = %v", req.StopSequences)
	}

	// items: 文本+图 / functionCall / functionResponse。
	if len(req.Items) != 3 {
		t.Fatalf("Items = %d, want 3", len(req.Items))
	}
	if req.Items[0].Role != "user" || req.Items[0].Text != "看这张图" {
		t.Errorf("Items[0] = %+v", req.Items[0])
	}
	if len(req.Items[0].Attachments) != 1 || req.Items[0].Attachments[0].MIME != "image/png" {
		t.Errorf("附件未解析: %+v", req.Items[0].Attachments)
	}
	if req.Items[1].Type != ir.ItemTypeFunctionCall || req.Items[1].Name != "get_weather" {
		t.Errorf("Items[1] = %+v", req.Items[1])
	}
	if !strings.Contains(req.Items[1].Arguments, "北京") {
		t.Errorf("functionCall.args 未保留: %q", req.Items[1].Arguments)
	}
	if req.Items[2].Type != ir.ItemTypeFunctionCallOutput || req.Items[2].Name != "get_weather" {
		t.Errorf("Items[2] = %+v", req.Items[2])
	}

	if len(req.Tools) != 1 || req.Tools[0].Name != "get_weather" {
		t.Errorf("Tools = %+v", req.Tools)
	}
}

func TestGeminiStreamTransducer(t *testing.T) {
	rec := httptest.NewRecorder()
	tr, err := transducer.NewGeminiTransducer(rec, "resp-1", "gemini-2.5-pro")
	if err != nil {
		t.Fatalf("NewGeminiTransducer: %v", err)
	}

	ch := make(chan ir.UnifiedStreamEvent, 8)
	ch <- ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: "你好"}
	ch <- ir.UnifiedStreamEvent{Type: ir.EventReasoningDelta, TextDelta: "思考中"}
	ch <- ir.UnifiedStreamEvent{Type: ir.EventToolCallStart, ToolCallID: "c1", ToolName: "get_weather"}
	ch <- ir.UnifiedStreamEvent{Type: ir.EventToolCallDelta, ArgsDelta: `{"city":`}
	ch <- ir.UnifiedStreamEvent{Type: ir.EventToolCallDelta, ArgsDelta: `"北京"}`}
	ch <- ir.UnifiedStreamEvent{Type: ir.EventUsage, Usage: &ir.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}}
	ch <- ir.UnifiedStreamEvent{Type: ir.EventDone}
	close(ch)

	if err := tr.Stream(ch); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	frames := parseSSEData(t, rec.Body.String())
	if len(frames) < 4 {
		t.Fatalf("SSE 帧数 = %d, want >= 4:\n%s", len(frames), rec.Body.String())
	}

	// 第一帧：文本增量。
	var first struct {
		Candidates []struct {
			Content struct {
				Role  string           `json:"role"`
				Parts []map[string]any `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	mustUnmarshal(t, frames[0], &first)
	if first.Candidates[0].Content.Role != "model" {
		t.Errorf("role = %q", first.Candidates[0].Content.Role)
	}
	if first.Candidates[0].Content.Parts[0]["text"] != "你好" {
		t.Errorf("文本帧不对: %v", first.Candidates[0].Content.Parts[0])
	}

	// 某一帧应有 thought:true 的思考增量。
	foundThought := false
	for _, f := range frames {
		if strings.Contains(f, `"thought":true`) {
			foundThought = true
		}
	}
	if !foundThought {
		t.Errorf("思考帧缺失:\n%s", rec.Body.String())
	}

	// 收尾帧：functionCall 完整 args + finishReason + usageMetadata。
	var last struct {
		Candidates []struct {
			FinishReason string `json:"finishReason"`
			Content      struct {
				Parts []map[string]any `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Usage map[string]int `json:"usageMetadata"`
	}
	mustUnmarshal(t, frames[len(frames)-1], &last)
	if last.Candidates[0].FinishReason != "STOP" {
		t.Errorf("finishReason = %q", last.Candidates[0].FinishReason)
	}
	if last.Usage["promptTokenCount"] != 10 || last.Usage["candidatesTokenCount"] != 5 {
		t.Errorf("usageMetadata = %v", last.Usage)
	}

	// 工具调用帧：args 被攒齐成完整对象。
	foundCall := false
	for _, f := range frames {
		if strings.Contains(f, `"functionCall"`) && strings.Contains(f, `"city":"北京"`) {
			foundCall = true
		}
	}
	if !foundCall {
		t.Errorf("functionCall 帧缺失或 args 未攒齐:\n%s", rec.Body.String())
	}
}

func TestGeminiNonStreamRender(t *testing.T) {
	agg := ir.AggregatedResponse{}
	acc := ir.NewAccumulator()
	acc.Push(ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: "答案"})
	acc.Push(ir.UnifiedStreamEvent{Type: ir.EventUsage, Usage: &ir.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}})
	acc.Push(ir.UnifiedStreamEvent{Type: ir.EventDone})
	agg = acc.Result()

	body := transducer.RenderNonStream(ir.ProtocolGemini, agg, "resp-9", "gemini-2.5-pro")
	b, _ := json.Marshal(body)

	var got struct {
		Candidates []struct {
			Content struct {
				Parts []map[string]any `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Usage map[string]int `json:"usageMetadata"`
	}
	mustUnmarshal(t, string(b), &got)
	if len(got.Candidates) != 1 || got.Candidates[0].Content.Parts[0]["text"] != "答案" {
		t.Errorf("渲染结果不对: %s", b)
	}
	if got.Usage["totalTokenCount"] != 5 {
		t.Errorf("usageMetadata = %v", got.Usage)
	}
}

func TestGeminiErrorShape(t *testing.T) {
	rec := httptest.NewRecorder()
	transducer.WriteError(rec, ir.ProtocolGemini, ir.NewAPIError(ir.ErrCodeBadRequest, "参数错了").WithStatus(400))

	var got struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	mustUnmarshal(t, rec.Body.String(), &got)
	if got.Error.Code != 400 || got.Error.Status != "INVALID_ARGUMENT" || got.Error.Message != "参数错了" {
		t.Errorf("Gemini 错误形状不对: %s", rec.Body.String())
	}
}

// parseSSEData 从 SSE 文本里抽出全部 data 帧的 JSON 字符串。
func parseSSEData(t *testing.T, raw string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "data: ") {
			out = append(out, strings.TrimPrefix(line, "data: "))
		}
	}
	return out
}

func mustUnmarshal(t *testing.T, data string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(data), v); err != nil {
		t.Fatalf("JSON 解析失败: %v\n%s", err, data)
	}
}
