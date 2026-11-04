package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// 对话与目录类工具。
//
// 为什么补这些：MCP 层原先只包了图像/视频，Agent 想"用网关里的任意模型聊天"
// 仍然要自己拼 /v1/chat/completions。补上之后，网关对 Agent 才算"一个可调用的
// 模型池"，而不是只有两个媒体端点。
//
// 与媒体工具的区别：媒体返回原始 JSON（用户要的是 URL），对话返回**纯文本 +
// 用量摘要**——MCP 工具的返回值是给模型读的，塞一整段 JSON 只会浪费上下文。

// chatArgs 是 aether_chat 的参数。
type chatArgs struct {
	// Messages 是标准 OpenAI 消息数组；给了就优先于 Prompt。
	Messages    []chatMessage `json:"messages"`
	Prompt      string        `json:"prompt"`
	System      string        `json:"system"`
	Model       string        `json:"model"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature"`
	AllowEmpty  bool          `json:"-"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// callChat 走 /v1/chat/completions，返回助手文本 + 用量摘要。
func (s *server) callChat(in chatArgs) (string, error) {
	msgs := make([]map[string]any, 0, len(in.Messages)+2)
	if sys := strings.TrimSpace(in.System); sys != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": sys})
	}
	for _, m := range in.Messages {
		role := strings.TrimSpace(m.Role)
		if role == "" {
			role = "user"
		}
		msgs = append(msgs, map[string]any{"role": role, "content": m.Content})
	}
	if len(msgs) == 0 || (len(in.Messages) == 0 && strings.TrimSpace(in.Prompt) != "") {
		msgs = append(msgs, map[string]any{"role": "user", "content": strings.TrimSpace(in.Prompt)})
	}
	if len(msgs) == 0 {
		return "", fmt.Errorf("messages 或 prompt 至少要给一个")
	}
	body := map[string]any{"messages": msgs, "stream": false}
	if m := strings.TrimSpace(in.Model); m != "" {
		body["model"] = m
	}
	if in.MaxTokens > 0 {
		body["max_tokens"] = in.MaxTokens
	}
	if in.Temperature > 0 {
		body["temperature"] = in.Temperature
	}

	raw, err := s.do(http.MethodPost, "/v1/chat/completions", body)
	if err != nil {
		return "", err
	}
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		// 上游给的形状不认识时，原样返回，别把内容吞掉。
		return strings.TrimSpace(string(raw)), nil
	}
	if len(out.Choices) == 0 {
		return strings.TrimSpace(string(raw)), nil
	}
	text := out.Choices[0].Message.Content
	var b strings.Builder
	b.WriteString(text)
	if out.Usage.TotalTokens > 0 {
		b.WriteString(fmt.Sprintf("\n\n---\n模型 %s · 输入 %d / 输出 %d / 合计 %d tokens",
			out.Model, out.Usage.PromptTokens, out.Usage.CompletionTokens, out.Usage.TotalTokens))
		if fr := out.Choices[0].FinishReason; fr != "" && fr != "stop" {
			b.WriteString(" · 结束原因 " + fr)
		}
	}
	return b.String(), nil
}

// callModels 列出网关当前可用的模型（走标准 /v1/models）。
func (s *server) callModels() (string, error) {
	raw, err := s.do(http.MethodGet, "/v1/models", nil)
	if err != nil {
		return "", err
	}
	var out struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return strings.TrimSpace(string(raw)), nil
	}
	if len(out.Data) == 0 {
		return "网关当前没有可用模型（先接入账号）", nil
	}
	lines := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		if m.OwnedBy != "" {
			lines = append(lines, m.ID+"  ("+m.OwnedBy+")")
		} else {
			lines = append(lines, m.ID)
		}
	}
	return fmt.Sprintf("共 %d 个模型：\n%s", len(lines), strings.Join(lines, "\n")), nil
}

// do 发一个请求并返回响应体（失败时带上远端说明）。
func (s *server) do(method, path string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, s.addr+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if s.key != "" {
		req.Header.Set("Authorization", "Bearer "+s.key)
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接网关失败（%s）: %w", s.addr, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("网关返回 %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return data, nil
}

// chatToolSpec / modelsToolSpec 供 toolSpecs 复用。
func chatToolSpec() tool {
	return tool{
		Name: "aether_chat",
		Description: "用本机 Aether Cockpit 网关里的任意模型对话（自动选号、计费与脱敏）。" +
			"返回助手文本与 token 用量。",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{"type": "string", "description": "单轮用户输入（messages 为空时使用）"},
				"messages": map[string]any{
					"type":        "array",
					"description": "OpenAI 风格消息数组，优先于 prompt",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"role":    map[string]any{"type": "string", "enum": []string{"system", "user", "assistant"}},
							"content": map[string]any{"type": "string"},
						},
						"required": []string{"role", "content"},
					},
				},
				"system":      map[string]any{"type": "string", "description": "系统提示词（可选）"},
				"model":       map[string]any{"type": "string", "description": "模型名（可选，默认由网关路由）"},
				"max_tokens":  map[string]any{"type": "integer", "description": "输出上限（可选）"},
				"temperature": map[string]any{"type": "number", "description": "温度 0-2（可选）"},
			},
			"anyOf": []any{map[string]any{"required": []string{"prompt"}}, map[string]any{"required": []string{"messages"}}},
		},
	}
}

func modelsToolSpec() tool {
	return tool{
		Name:        "aether_models",
		Description: "列出本机 Aether Cockpit 网关当前可用的模型与其归属渠道。",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}
}
