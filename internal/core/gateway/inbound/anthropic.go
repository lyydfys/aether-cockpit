package inbound

import (
	"encoding/json"
	"strings"

	"aether-cockpit/internal/core/ir"
)

// ParseAnthropic 解析 POST /v1/messages（Claude Code / Cline / Roo 等使用）。
//
// 与 OpenAI 的关键差异：tool_result 藏在 user 消息的 content 数组里，
// 而 thinking 块自带 signature —— 后者必须原样保留，否则上游会拒绝续轮。
func ParseAnthropic(body []byte, requestID string) (*ir.UnifiedChatRequest, error) {
	var wire anthropicRequestWire
	if err := decodeJSONSafe(body, &wire, "messages"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(wire.Model) == "" {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "缺少 model 字段")
	}
	if len(wire.Messages) == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "缺少 messages 字段")
	}

	instructions, systemCache := decodeAnthropicSystem(wire.System)
	req := &ir.UnifiedChatRequest{
		RequestID:          requestID,
		DownstreamProtocol: ir.ProtocolAnthropic,
		Model:              wire.Model,
		Stream:             wire.Stream,
		Temperature:        wire.Temperature,
		MaxOutputTokens:    wire.MaxTokens,
		StopSequences:      wire.StopSequences,
		StoreResponse:      false,
		Instructions:       instructions,
		SystemCacheControl: systemCache,
	}
	if wire.Thinking != nil && wire.Thinking.Type == "enabled" {
		// budget_tokens 是绝对预算，映射成 OpenAI 的 effort 三档即可。
		switch {
		case wire.Thinking.BudgetTokens >= 16000:
			req.ReasoningEffort = "high"
		case wire.Thinking.BudgetTokens >= 4000:
			req.ReasoningEffort = "medium"
		default:
			req.ReasoningEffort = "low"
		}
	}
	if tc := wire.ToolChoice; tc != nil {
		switch strings.ToLower(strings.TrimSpace(tc.Type)) {
		case "auto":
			req.ToolChoice = &ir.ToolChoice{Mode: "auto"}
		case "any":
			req.ToolChoice = &ir.ToolChoice{Mode: "required"}
		case "tool":
			req.ToolChoice = &ir.ToolChoice{Mode: "function", Name: tc.Name}
		case "none":
			req.ToolChoice = &ir.ToolChoice{Mode: "none"}
		}
		if tc.DisableParallelToolUse != nil {
			parallel := !*tc.DisableParallelToolUse
			req.ParallelToolCalls = &parallel
		}
	}

	for _, m := range wire.Messages {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role == "" {
			role = "user"
		}
		items, err := decodeAnthropicContent(m.Content, role)
		if err != nil {
			return nil, err
		}
		req.Items = append(req.Items, items...)
	}

	for _, t := range wire.Tools {
		if t.Name == "" {
			continue
		}
		req.Tools = append(req.Tools, ir.UnifiedToolSpec{
			Name:         t.Name,
			Description:  t.Description,
			Parameters:   t.InputSchema,
			CacheControl: t.CacheControl,
		})
	}

	setClientSession(req, wire.Metadata.UserID)
	return req, nil
}

// decodeAnthropicSystem system 可能是字符串或 text 块数组。
// 返回值第二项是该数组里出现过的 cache_control 断点（Anthropic 只认
// 最后一个块的断点，这里取最后一个非空值）。
func decodeAnthropicSystem(raw json.RawMessage) (string, json.RawMessage) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var parts []anthropicPartWire
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", nil
	}
	var sb strings.Builder
	var cache json.RawMessage
	for _, p := range parts {
		sb.WriteString(p.Text)
		if len(p.CacheControl) > 0 {
			cache = p.CacheControl
		}
	}
	return sb.String(), cache
}

// decodeAnthropicContent 把一个消息的 content 拆成多个 IR item。
//
// 关键点：一条 assistant 消息里可能同时包含 text / thinking / tool_use，
// 必须拆成三个 item 且保持顺序 —— 因为上游下一轮要求 thinking 出现在 tool_use 之前。
func decodeAnthropicContent(raw json.RawMessage, role string) ([]ir.UnifiedItem, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	// 字符串形态
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if strings.TrimSpace(s) == "" {
			return nil, nil
		}
		return []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: role, Text: s}}, nil
	}

	var parts []anthropicPartWire
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, ir.NewAPIErrorf(ir.ErrCodeBadRequest, "content 结构无法解析: %v", err).WithCause(err)
	}

	out := make([]ir.UnifiedItem, 0, len(parts))
	for _, p := range parts {
		switch p.Type {

		case "text", "", "output_text", "input_text":
			if strings.TrimSpace(p.Text) == "" {
				continue
			}
			out = append(out, ir.UnifiedItem{
				Type:         ir.ItemTypeMessage,
				Role:         role,
				Text:         p.Text,
				CacheControl: p.CacheControl,
			})

		case "thinking":
			// 思考块：signature 必须原样保留（否则上游拒绝续轮）；
			// 只有签名没有明文时也保留一条，避免"签名丢了"。
			if strings.TrimSpace(p.Thinking) == "" && strings.TrimSpace(p.Signature) == "" {
				continue
			}
			out = append(out, ir.UnifiedItem{
				Type:             ir.ItemTypeReasoning,
				Role:             "assistant",
				Text:             p.Thinking,
				ThoughtSignature: p.Signature,
				CacheControl:     p.CacheControl,
			})

		case "redacted_thinking":
			// 加密思考块：明文不可得，但 data 必须原样回传。
			out = append(out, ir.UnifiedItem{
				Type:             ir.ItemTypeReasoning,
				Role:             "assistant",
				EncryptedContent: p.Data,
				CacheControl:     p.CacheControl,
			})

		case "tool_use":
			out = append(out, ir.UnifiedItem{
				Type:         ir.ItemTypeFunctionCall,
				Role:         "assistant",
				ID:           p.ID,
				CallID:       p.ID,
				Name:         p.Name,
				Arguments:    compactJSON(p.Input),
				CacheControl: p.CacheControl,
			})

		case "tool_result":
			out = append(out, ir.UnifiedItem{
				Type:         ir.ItemTypeFunctionCallOutput,
				CallID:       p.ToolUseID,
				Output:       decodeAnthropicToolResult(p.Content),
				CacheControl: p.CacheControl,
			})

		case "image":
			if p.Source != nil {
				att := ir.MediaAttachment{Kind: "image", MIME: p.Source.MediaType}
				if p.Source.Type == "base64" {
					att.DataB64 = p.Source.Data
				} else {
					att.URL = p.Source.URL
				}
				out = append(out, ir.UnifiedItem{
					Type:         ir.ItemTypeMessage,
					Role:         role,
					Attachments:  []ir.MediaAttachment{att},
					CacheControl: p.CacheControl,
				})
			}

		case "document":
			// 文档块在逆向渠道上支持度极低，只保留提示以免上下文静默丢失。
			out = append(out, ir.UnifiedItem{
				Type:         ir.ItemTypeMessage,
				Role:         role,
				Text:         "[document omitted: 当前渠道不支持文档块直传]",
				CacheControl: p.CacheControl,
			})
		}
	}
	return out, nil
}

// decodeAnthropicToolResult tool_result 的 content 可为字符串或内容块数组。
func decodeAnthropicToolResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []anthropicPartWire
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(p.Text)
	}
	return sb.String()
}

// compactJSON 把任意 JSON 压缩成单行字符串，作为工具参数字符串。
func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(b)
}

// --- wire 结构 ---

type anthropicRequestWire struct {
	Model         string                 `json:"model"`
	System        json.RawMessage        `json:"system"`
	Messages      []anthropicMessageWire `json:"messages"`
	Tools         []anthropicToolWire    `json:"tools"`
	Stream        bool                   `json:"stream"`
	Temperature   *float64               `json:"temperature"`
	TopP          *float64               `json:"top_p"`
	MaxTokens     *int                   `json:"max_tokens"`
	StopSequences []string               `json:"stop_sequences"`
	Thinking      *struct {
		Type         string `json:"type"`
		BudgetTokens int    `json:"budget_tokens"`
	} `json:"thinking"`
	ToolChoice *struct {
		Type                   string `json:"type"`
		Name                   string `json:"name"`
		DisableParallelToolUse *bool  `json:"disable_parallel_tool_use"`
	} `json:"tool_choice"`
	// Metadata.UserID 是客户端自己的会话标识（Claude Code 会带）。
	Metadata struct {
		UserID string `json:"user_id"`
	} `json:"metadata"`
}

type anthropicMessageWire struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthropicPartWire struct {
	Type         string          `json:"type"`
	Text         string          `json:"text"`
	Thinking     string          `json:"thinking"`
	Signature    string          `json:"signature"`
	Data         string          `json:"data"`
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Input        json.RawMessage `json:"input"`
	ToolUseID    string          `json:"tool_use_id"`
	Content      json.RawMessage `json:"content"`
	CacheControl json.RawMessage `json:"cache_control"`
	Source       *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
		URL       string `json:"url"`
	} `json:"source"`
}

type anthropicToolWire struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	CacheControl json.RawMessage `json:"cache_control"`
}
