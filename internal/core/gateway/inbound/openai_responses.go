package inbound

import (
	"encoding/json"
	"strings"

	"aether-cockpit/internal/core/ir"
)

// ParseResponses 解析 POST /v1/responses。
//
// 这是三种协议里结构最复杂的一个：input 既可以是纯字符串，也可以是混合了
// message / function_call / function_call_output / reasoning / item_reference
// 的 Item 数组。归一化的关键在于：保持数组顺序（工具调用链的顺序有语义）。
func ParseResponses(body []byte, requestID string) (*ir.UnifiedChatRequest, error) {
	var wire responsesRequestWire
	if err := decodeJSONSafe(body, &wire, "responses"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(wire.Model) == "" {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "缺少 model 字段")
	}

	req := &ir.UnifiedChatRequest{
		RequestID:          requestID,
		DownstreamProtocol: ir.ProtocolOpenAIResponses,
		Model:              wire.Model,
		PreviousResponseID: wire.PreviousResponseID,
		// 语义对齐：未显式传 store 时默认 true（与 OpenAI 一致）。
		StoreResponse:     wire.Store == nil || *wire.Store,
		Instructions:      wire.Instructions,
		Stream:            wire.Stream,
		Temperature:       wire.Temperature,
		TopP:              wire.TopP,
		MaxOutputTokens:   wire.MaxOutputTokens,
		ParallelToolCalls: wire.ParallelToolCalls,
		ReasoningEffort:   wire.Reasoning.Effort,
		ToolChoice:        decodeToolChoice(wire.ToolChoice),
		ResponseFormat:    decodeResponseFormat(wire.Text.Format),
	}
	if len(wire.Stop) > 0 {
		req.StopSequences = wire.Stop
	}

	items, err := decodeResponsesInput(wire.Input)
	if err != nil {
		return nil, err
	}
	req.Items = items

	for _, t := range wire.Tools {
		if t.Type != "" && t.Type != "function" {
			continue // 内置工具（web_search / file_search / computer_use）无法映射
		}
		if t.Name == "" {
			continue
		}
		req.Tools = append(req.Tools, ir.UnifiedToolSpec{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		})
	}

	setClientSession(req, firstNonEmpty(wire.Metadata.UserID, wire.User))
	return req, nil
}

// decodeResponsesInput 处理 input 的三种形态：字符串 / Item 数组 / null。
func decodeResponsesInput(raw json.RawMessage) ([]ir.UnifiedItem, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if strings.TrimSpace(s) == "" {
			return nil, nil
		}
		return []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: s}}, nil
	}

	var wires []responsesItemWire
	if err := json.Unmarshal(raw, &wires); err != nil {
		return nil, ir.NewAPIErrorf(ir.ErrCodeBadRequest, "input 既不是字符串也不是数组: %v", err).WithCause(err)
	}

	out := make([]ir.UnifiedItem, 0, len(wires))
	for _, w := range wires {
		switch w.Type {

		case "message", "":
			text, atts := decodeResponsesContent(w.Content)
			role := strings.ToLower(w.Role)
			if role == "" {
				role = "user"
			}
			out = append(out, ir.UnifiedItem{
				ID:          w.ID,
				Type:        ir.ItemTypeMessage,
				Role:        role,
				Text:        text,
				Attachments: atts,
			})

		case "reasoning":
			out = append(out, ir.UnifiedItem{
				ID:               w.ID,
				Type:             ir.ItemTypeReasoning,
				Role:             "assistant",
				Text:             decodeReasoningSummary(w.Summary),
				EncryptedContent: w.EncryptedContent,
			})

		case "function_call":
			out = append(out, ir.UnifiedItem{
				ID:        w.ID,
				Type:      ir.ItemTypeFunctionCall,
				Role:      "assistant",
				CallID:    w.CallID,
				Name:      w.Name,
				Arguments: w.Arguments,
			})

		case "function_call_output":
			out = append(out, ir.UnifiedItem{
				Type:   ir.ItemTypeFunctionCallOutput,
				CallID: w.CallID,
				Output: decodeFunctionOutput(w.Output),
			})

		case "item_reference":
			// 只记录引用，展开交给 StateSuture（它才有历史快照）。
			out = append(out, ir.UnifiedItem{
				Type:  ir.ItemTypeMessage,
				RefID: w.ID,
			})

		default:
			// 未知类型（如 web_search_call / computer_call）在逆向渠道上无意义，
			// 但如果带文本就保留文本，避免上下文断裂。
			text, atts := decodeResponsesContent(w.Content)
			if strings.TrimSpace(text) != "" || len(atts) > 0 {
				out = append(out, ir.UnifiedItem{
					ID:          w.ID,
					Type:        ir.ItemTypeMessage,
					Role:        "user",
					Text:        text,
					Attachments: atts,
				})
			}
		}
	}
	return out, nil
}

// decodeResponsesContent 解析 message 的 content 数组。
func decodeResponsesContent(raw json.RawMessage) (string, []ir.MediaAttachment) {
	if len(raw) == 0 {
		return "", nil
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}

	var parts []responsesPartWire
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", nil
	}

	var (
		sb   strings.Builder
		atts []ir.MediaAttachment
	)
	for _, p := range parts {
		switch p.Type {
		case "input_text", "output_text", "text", "summary_text", "":
			sb.WriteString(p.Text)
		case "input_image", "image_url":
			if p.ImageURL != "" {
				atts = append(atts, imageAttachment(p.ImageURL))
			}
		case "refusal":
			sb.WriteString(p.Refusal)
		}
	}
	return sb.String(), atts
}

// decodeReasoningSummary 把 summary 数组拼成纯文本。
func decodeReasoningSummary(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var parts []responsesPartWire
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(p.Text)
	}
	return sb.String()
}

// decodeFunctionOutput output 可能是字符串，也可能是 content part 数组。
func decodeFunctionOutput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	text, _ := decodeResponsesContent(raw)
	return text
}

// --- wire 结构 ---

type responsesRequestWire struct {
	Model              string              `json:"model"`
	Instructions       string              `json:"instructions"`
	Input              json.RawMessage     `json:"input"`
	PreviousResponseID string              `json:"previous_response_id"`
	Store              *bool               `json:"store"`
	Tools              []responsesToolWire `json:"tools"`
	Stream             bool                `json:"stream"`
	Temperature        *float64            `json:"temperature"`
	TopP               *float64            `json:"top_p"`
	MaxOutputTokens    *int                `json:"max_output_tokens"`
	Stop               []string            `json:"stop"`
	ParallelToolCalls  *bool               `json:"parallel_tool_calls"`
	ToolChoice         json.RawMessage     `json:"tool_choice"`
	Text               struct {
		Format json.RawMessage `json:"format"`
	} `json:"text"`
	User     string `json:"user"`
	Metadata struct {
		UserID string `json:"user_id"`
	} `json:"metadata"`
	Reasoning struct {
		Effort string `json:"effort"`
	} `json:"reasoning"`
}

type responsesItemWire struct {
	Type             string          `json:"type"`
	ID               string          `json:"id"`
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content"`
	Summary          json.RawMessage `json:"summary"`
	EncryptedContent string          `json:"encrypted_content"`
	CallID           string          `json:"call_id"`
	Name             string          `json:"name"`
	Arguments        string          `json:"arguments"`
	Output           json.RawMessage `json:"output"`
}

type responsesPartWire struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Refusal  string `json:"refusal"`
	ImageURL string `json:"image_url"`
}

type responsesToolWire struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}
