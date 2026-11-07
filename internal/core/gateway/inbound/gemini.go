package inbound

import (
	"encoding/json"
	"strings"

	"aether-cockpit/internal/core/ir"
)

// ParseGemini 解析 POST /v1beta/models/{model}:generateContent / :streamGenerateContent。
//
// model 来自 URL 路径（models/<model>:<action>），不在请求体里；
// 调用方负责从路径里剥出来传进来。兼容 "models/" 前缀与渠道前缀
// （如 kiro/claude-sonnet-4-6），后者交给模型路由器的既有规则处理。
func ParseGemini(body []byte, requestID, model string) (*ir.UnifiedChatRequest, error) {
	var wire geminiRequestWire
	if err := decodeJSONSafe(body, &wire, "gemini"); err != nil {
		return nil, err
	}
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	if model == "" {
		model = strings.TrimSpace(wire.Model)
	}
	if model == "" {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "缺少模型名（URL 路径 models/<model>:generateContent）")
	}
	if len(wire.Contents) == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "缺少 contents 字段")
	}

	req := &ir.UnifiedChatRequest{
		RequestID:          requestID,
		DownstreamProtocol: ir.ProtocolGemini,
		Model:              model,
		Stream:             false, // 流式与否由 URL 的 :streamGenerateContent 与 alt=sse 决定，调用方回填
		StoreResponse:      false,
	}

	// generationConfig → 采样参数。
	if gc := wire.GenerationConfig; gc != nil {
		req.Temperature = gc.Temperature
		req.TopP = gc.TopP
		if gc.MaxOutputTokens != nil && *gc.MaxOutputTokens > 0 {
			req.MaxOutputTokens = gc.MaxOutputTokens
		}
		req.StopSequences = gc.StopSequences
	}

	// systemInstruction → Instructions。
	if si := wire.SystemInstruction; si != nil {
		req.Instructions = joinInstructions(req.Instructions, si.Text())
	}

	for _, c := range wire.Contents {
		role := strings.ToLower(strings.TrimSpace(c.Role))
		if role == "model" || role == "assistant" {
			for _, p := range c.Parts {
				if p.FunctionCall != nil {
					args, _ := json.Marshal(p.FunctionCall.Args)
					if len(p.FunctionCall.Args) == 0 {
						args = []byte("{}")
					}
					req.Items = append(req.Items, ir.UnifiedItem{
						Type:      ir.ItemTypeFunctionCall,
						Role:      "assistant",
						CallID:    p.FunctionCall.Name + "-" + shortKey(args),
						Name:      p.FunctionCall.Name,
						Arguments: string(args),
					})
					continue
				}
				if strings.TrimSpace(p.Text) != "" {
					// thought=true 是 Gemini 的思考段：必须作为 reasoning item
					// 单独保留，混进正文会让下一轮上下文被上游判成"篡改历史"。
					itemType := ir.ItemTypeMessage
					if p.Thought {
						itemType = ir.ItemTypeReasoning
					}
					req.Items = append(req.Items, ir.UnifiedItem{
						Type: itemType, Role: "assistant", Text: p.Text,
					})
				}
			}
			continue
		}

		// user / function / 其它角色：普通 parts 合并成一条消息，
		// functionCall / functionResponse 各自成条（与 Chat 解析的合并语义一致）。
		var sb strings.Builder
		var atts []ir.MediaAttachment
		flush := func() {
			if sb.Len() > 0 || len(atts) > 0 {
				req.Items = append(req.Items, ir.UnifiedItem{
					Type: ir.ItemTypeMessage, Role: "user", Text: sb.String(), Attachments: atts,
				})
				sb.Reset()
				atts = nil
			}
		}
		for _, p := range c.Parts {
			if p.FunctionResponse != nil {
				flush()
				respBytes, _ := json.Marshal(p.FunctionResponse.Response)
				out := string(respBytes)
				if out == "" || out == "null" {
					out = "{}"
				}
				req.Items = append(req.Items, ir.UnifiedItem{
					Type:   ir.ItemTypeFunctionCallOutput,
					CallID: p.FunctionResponse.Name + "-" + shortKey(respBytes),
					Name:   p.FunctionResponse.Name,
					Output: out,
				})
				continue
			}
			if p.FunctionCall != nil {
				flush()
				args, _ := json.Marshal(p.FunctionCall.Args)
				if len(p.FunctionCall.Args) == 0 {
					args = []byte("{}")
				}
				req.Items = append(req.Items, ir.UnifiedItem{
					Type: ir.ItemTypeFunctionCall, Role: "assistant",
					CallID: p.FunctionCall.Name + "-" + shortKey(args),
					Name:   p.FunctionCall.Name, Arguments: string(args),
				})
				continue
			}
			sb.WriteString(p.Text)
			if a := geminiAttachment(p); a != nil {
				atts = append(atts, a...)
			}
		}
		flush()
	}

	// tools[].functionDeclarations → UnifiedToolSpec。
	for _, t := range wire.Tools {
		for _, fd := range t.FunctionDeclarations {
			if fd.Name == "" {
				continue
			}
			req.Tools = append(req.Tools, ir.UnifiedToolSpec{
				Name:        fd.Name,
				Description: fd.Description,
				Parameters:  fd.Parameters,
			})
		}
	}

	return req, nil
}

// shortKey 为没有 call_id 概念的 Gemini 生成一个稳定短标识。
// functionResponse 靠 name 配对，历史重放时同一工具名需要可区分。
func shortKey(b []byte) string {
	if len(b) > 8 {
		b = b[len(b)-8:]
	}
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, hexdigits[c%16], hexdigits[(c/16)%16])
	}
	return string(out)
}

func geminiAttachment(p geminiPartWire) []ir.MediaAttachment {
	if p.InlineData == nil || p.InlineData.Data == "" {
		return nil
	}
	mime := p.InlineData.MimeType
	if mime == "" {
		mime = "application/octet-stream"
	}
	kind := "file"
	if strings.HasPrefix(mime, "image/") {
		kind = "image"
	} else if strings.HasPrefix(mime, "audio/") {
		kind = "audio"
	}
	return []ir.MediaAttachment{{
		Kind: kind, MIME: mime, DataB64: p.InlineData.Data,
	}}
}

// --- wire 结构 ---

type geminiRequestWire struct {
	Contents          []geminiContentWire  `json:"contents"`
	SystemInstruction *geminiContentWire   `json:"systemInstruction"`
	Tools             []geminiToolWire     `json:"tools"`
	GenerationConfig  *geminiGenConfigWire `json:"generationConfig"`
	Model             string               `json:"model"`
}

type geminiGenConfigWire struct {
	Temperature     *float64 `json:"temperature"`
	TopP            *float64 `json:"topP"`
	MaxOutputTokens *int     `json:"maxOutputTokens"`
	StopSequences   []string `json:"stopSequences"`
}

type geminiContentWire struct {
	Role  string           `json:"role"`
	Parts []geminiPartWire `json:"parts"`
}

// Text 把 parts 里的文本段拼起来（systemInstruction 常是 {parts:[{text}]}）。
func (c *geminiContentWire) Text() string {
	var sb strings.Builder
	for _, p := range c.Parts {
		sb.WriteString(p.Text)
	}
	return sb.String()
}

type geminiPartWire struct {
	Text             string            `json:"text"`
	Thought          bool              `json:"thought"`
	InlineData       *geminiInlineWire `json:"inlineData"`
	FunctionCall     *geminiFnCallWire `json:"functionCall"`
	FunctionResponse *geminiFnRespWire `json:"functionResponse"`
}

type geminiInlineWire struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFnCallWire struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type geminiFnRespWire struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type geminiToolWire struct {
	FunctionDeclarations []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"functionDeclarations"`
}
