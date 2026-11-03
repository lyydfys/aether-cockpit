package inbound

import (
	"encoding/json"
	"strings"

	"aether-cockpit/internal/core/ir"
)

// ParseChatCompletions 解析 POST /v1/chat/completions。
func ParseChatCompletions(body []byte, requestID string) (*ir.UnifiedChatRequest, error) {
	var wire chatRequestWire
	if err := decodeJSONSafe(body, &wire, "chat/completions"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(wire.Model) == "" {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "缺少 model 字段")
	}
	if len(wire.Messages) == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "缺少 messages 字段")
	}

	req := &ir.UnifiedChatRequest{
		RequestID:          requestID,
		DownstreamProtocol: ir.ProtocolOpenAIChat,
		Model:              wire.Model,
		Stream:             wire.Stream,
		Temperature:        wire.Temperature,
		TopP:               wire.TopP,
		FrequencyPenalty:   wire.FrequencyPenalty,
		PresencePenalty:    wire.PresencePenalty,
		ReasoningEffort:    wire.ReasoningEffort,
		ParallelToolCalls:  wire.ParallelToolCalls,
		StopSequences:      decodeStringOrArray(wire.Stop),
		StoreResponse:      false, // Chat 协议本身无状态
		ToolChoice:         decodeToolChoice(wire.ToolChoice),
		ResponseFormat:     decodeResponseFormat(wire.ResponseFormat),
	}
	// max_tokens 已废弃但客户端仍在大量使用，两者都要兼容。
	if wire.MaxCompletionTokens != nil {
		req.MaxOutputTokens = wire.MaxCompletionTokens
	} else if wire.MaxTokens != nil {
		req.MaxOutputTokens = wire.MaxTokens
	}

	for _, m := range wire.Messages {
		role := strings.ToLower(strings.TrimSpace(m.Role))

		switch role {
		case "system", "developer":
			text, _, err := decodeChatContent(m.Content)
			if err != nil {
				return nil, err
			}
			req.Instructions = joinInstructions(req.Instructions, text)

		case "user":
			text, atts, err := decodeChatContent(m.Content)
			if err != nil {
				return nil, err
			}
			req.Items = append(req.Items, ir.UnifiedItem{
				Type:        ir.ItemTypeMessage,
				Role:        "user",
				Text:        text,
				Attachments: atts,
			})

		case "assistant":
			text, _, err := decodeChatContent(m.Content)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(text) != "" {
				req.Items = append(req.Items, ir.UnifiedItem{
					Type: ir.ItemTypeMessage,
					Role: "assistant",
					Text: text,
				})
			}
			for _, tc := range m.ToolCalls {
				req.Items = append(req.Items, ir.UnifiedItem{
					Type:      ir.ItemTypeFunctionCall,
					Role:      "assistant",
					ID:        tc.ID,
					CallID:    tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}

		case "tool", "function":
			text, _, err := decodeChatContent(m.Content)
			if err != nil {
				return nil, err
			}
			req.Items = append(req.Items, ir.UnifiedItem{
				Type:   ir.ItemTypeFunctionCallOutput,
				CallID: m.ToolCallID,
				Name:   m.Name,
				Output: text,
			})

		default:
			// 未知角色按 user 处理，尽量不让请求失败。
			text, atts, err := decodeChatContent(m.Content)
			if err != nil {
				return nil, err
			}
			req.Items = append(req.Items, ir.UnifiedItem{
				Type: ir.ItemTypeMessage, Role: "user", Text: text, Attachments: atts,
			})
		}
	}

	for _, t := range wire.Tools {
		if t.Type != "" && t.Type != "function" {
			// 内置工具（web_search / file_search 等）无法映射到逆向渠道，跳过。
			continue
		}
		if t.Function.Name == "" {
			continue
		}
		req.Tools = append(req.Tools, ir.UnifiedToolSpec{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}

	setClientSession(req, wire.User)
	return req, nil
}

// --- wire 结构 ---

type chatRequestWire struct {
	Model               string            `json:"model"`
	Messages            []chatMessageWire `json:"messages"`
	Tools               []chatToolWire    `json:"tools"`
	Stream              bool              `json:"stream"`
	Temperature         *float64          `json:"temperature"`
	TopP                *float64          `json:"top_p"`
	// 重复惩罚：客户端几乎都会带（尤其是 agent），丢了就可能复读。
	FrequencyPenalty    *float64          `json:"frequency_penalty"`
	PresencePenalty     *float64          `json:"presence_penalty"`
	MaxTokens           *int              `json:"max_tokens"`
	MaxCompletionTokens *int              `json:"max_completion_tokens"`
	Stop                json.RawMessage   `json:"stop"`
	ReasoningEffort     string            `json:"reasoning_effort"`
	ParallelToolCalls   *bool             `json:"parallel_tool_calls"`
	ToolChoice          json.RawMessage   `json:"tool_choice"`
	ResponseFormat      json.RawMessage   `json:"response_format"`
	// User 是 OpenAI 的终端用户标识，充当会话级亲和的第一级来源。
	User string `json:"user"`
}

type chatMessageWire struct {
	Role       string             `json:"role"`
	Content    json.RawMessage    `json:"content"`
	ToolCalls  []chatToolCallWire `json:"tool_calls"`
	ToolCallID string             `json:"tool_call_id"`
	Name       string             `json:"name"`
}

type chatToolCallWire struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatToolWire struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type chatContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL *struct {
		URL    string `json:"url"`
		Detail string `json:"detail"`
	} `json:"image_url"`
}

// decodeChatContent 处理 content 可能是 string / []part / null 三种形态。
func decodeChatContent(raw json.RawMessage) (string, []ir.MediaAttachment, error) {
	if len(raw) == 0 {
		return "", nil, nil
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil, nil
	}

	var parts []chatContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		// null 或未知形态，当作空内容而非报错。
		return "", nil, nil
	}

	var (
		sb   strings.Builder
		atts []ir.MediaAttachment
	)
	for _, p := range parts {
		switch p.Type {
		case "text", "input_text", "output_text", "":
			sb.WriteString(p.Text)
		case "image_url", "input_image":
			if p.ImageURL != nil && p.ImageURL.URL != "" {
				atts = append(atts, imageAttachment(p.ImageURL.URL))
			}
		}
	}
	return sb.String(), atts, nil
}

// imageAttachment 把 data URI 拆成 base64 + MIME，http(s) URL 原样保留。
func imageAttachment(raw string) ir.MediaAttachment {
	if strings.HasPrefix(raw, "data:") {
		if idx := strings.Index(raw, ","); idx > 0 {
			meta := raw[5:idx]
			mime := strings.TrimSuffix(strings.SplitN(meta, ";", 2)[0], ";")
			return ir.MediaAttachment{
				Kind:    "image",
				MIME:    mime,
				DataB64: raw[idx+1:],
			}
		}
	}
	return ir.MediaAttachment{Kind: "image", URL: raw}
}
