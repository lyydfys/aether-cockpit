// Command aether-mcp 把 Aether Cockpit 的媒体能力包成 stdio MCP server。
//
// 为什么需要它：网关早就有 /v1/images/* 与 /v1/videos/*，但那些是"给客户端调
// 的 HTTP 端点"。Agent（Claude Code / Codex / 任何支持 MCP 的客户端）不会自己
// 拼 HTTP 请求，它们只会调工具。缺了这层包装，"聚合算力"对 agent 就是不可见的。
//
// 形态取舍：走 stdio（MCP 最通用的传输），由客户端拉起子进程；这与"网关本体
// 单二进制、零外挂进程"不冲突——这个进程是**客户端启动的**，不是网关拉起的。
//
// 配置（环境变量）：
//
//	AETHER_ADDR   网关地址，默认 http://127.0.0.1:7860
//	AETHER_API_KEY 网关密钥（本机回环可省略）
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	serverName    = "aether-cockpit"
	serverVersion = "0.1.0"
	// protocolVersion 是 MCP 的协议版本；客户端会回传它自己的，
	// 我们固定回一个兼容值即可（工具能力不依赖版本差异）。
	protocolVersion = "2024-11-05"
)

// rpcRequest 是 JSON-RPC 2.0 请求。
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse 是 JSON-RPC 2.0 响应。
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// tool 是 tools/list 返回的一项。
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func main() {
	addr := strings.TrimSpace(os.Getenv("AETHER_ADDR"))
	if addr == "" {
		addr = "http://127.0.0.1:7860"
	}
	if !strings.HasPrefix(addr, "http") {
		addr = "http://" + addr
	}
	s := &server{addr: strings.TrimSuffix(addr, "/"), key: strings.TrimSpace(os.Getenv("AETHER_API_KEY"))}
	if err := s.serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "aether-mcp:", err)
		os.Exit(1)
	}
}

type server struct {
	addr string
	key  string
}

// serve 跑 stdio 主循环：一行一条 JSON-RPC。
func (s *server) serve(in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	// 图像/视频请求的参数可能很长；给足缓冲，避免被默认 64KB 截断。
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "解析失败: " + err.Error()}})
			continue
		}
		// 通知（没有 id）不需要响应。
		if len(req.ID) == 0 {
			continue
		}
		resp := s.handle(req)
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (s *server) handle(req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
		}
	case "tools/list":
		resp.Result = map[string]any{"tools": toolSpecs()}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: "参数无效: " + err.Error()}
			return resp
		}
		text, err := s.callTool(params.Name, params.Arguments)
		if err != nil {
			// 工具执行失败也要按 MCP 约定放在 result 里（isError），
			// 让模型能看到错误并自行调整，而不是整个 RPC 报错。
			resp.Result = map[string]any{
				"content": []map[string]any{{"type": "text", "text": err.Error()}},
				"isError": true,
			}
			return resp
		}
		resp.Result = map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": false,
		}
	case "ping":
		resp.Result = map[string]any{}
	default:
		resp.Error = &rpcError{Code: -32601, Message: "不支持的方法: " + req.Method}
	}
	return resp
}

// toolSpecs 声明可用工具。
func toolSpecs() []tool {
	return []tool{
		chatToolSpec(),
		modelsToolSpec(),
		{
			Name:        "aether_image",
			Description: "用本机 Aether Cockpit 网关生成图片。返回图片 URL 或 base64。",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string", "description": "图片描述"},
					"model":  map[string]any{"type": "string", "description": "模型名（可选，默认用网关首选）"},
					"size":   map[string]any{"type": "string", "description": "尺寸，如 1024x1024"},
					"n":      map[string]any{"type": "integer", "description": "生成张数，默认 1"},
				},
				"required": []string{"prompt"},
			},
		},
		{
			Name:        "aether_video",
			Description: "用本机 Aether Cockpit 网关生成视频（异步任务，返回任务 ID 与状态）。",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string", "description": "视频描述"},
					"model":  map[string]any{"type": "string", "description": "模型名（可选）"},
				},
				"required": []string{"prompt"},
			},
		},
	}
}

// callTool 执行工具调用：转成对网关的 HTTP 请求。
func (s *server) callTool(name string, args json.RawMessage) (string, error) {
	// 目录类工具没有参数，先处理掉，避免下面要求 prompt。
	if name == "aether_models" {
		return s.callModels()
	}
	var in struct {
		Prompt string `json:"prompt"`
		Model  string `json:"model"`
		Size   string `json:"size"`
		N      int    `json:"n"`
		// 对话工具用到的字段：与媒体工具共用一次解析，避免两套入参结构。
		Messages    []chatMessage `json:"messages"`
		System      string        `json:"system"`
		MaxTokens   int           `json:"max_tokens"`
		Temperature float64       `json:"temperature"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", fmt.Errorf("参数解析失败: %w", err)
		}
	}
	if name == "aether_chat" {
		return s.callChat(chatArgs{
			Messages: in.Messages, Prompt: in.Prompt, System: in.System,
			Model: in.Model, MaxTokens: in.MaxTokens, Temperature: in.Temperature,
		})
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return "", fmt.Errorf("prompt 不能为空")
	}
	switch name {
	case "aether_image":
		body := map[string]any{"prompt": in.Prompt}
		if in.Model != "" {
			body["model"] = in.Model
		}
		if in.Size != "" {
			body["size"] = in.Size
		}
		if in.N > 0 {
			body["n"] = in.N
		}
		return s.post("/v1/images/generations", body)
	case "aether_video":
		body := map[string]any{"prompt": in.Prompt}
		if in.Model != "" {
			body["model"] = in.Model
		}
		return s.post("/v1/videos/generations/async", body)
	}
	return "", fmt.Errorf("未知工具: %s", name)
}

// post 发一个 JSON 请求并返回响应文本（失败时带上远端说明）。
func (s *server) post(path string, body any) (string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, s.addr+path, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.key != "" {
		req.Header.Set("Authorization", "Bearer "+s.key)
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("连接网关失败（%s）: %w", s.addr, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("网关返回 %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return string(data), nil
}
