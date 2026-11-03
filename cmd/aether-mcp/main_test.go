package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// initialize 必须回协议版本、tools 能力与 serverInfo。
func TestMCPInitialize(t *testing.T) {
	s := &server{addr: "http://127.0.0.1:1"}
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}` + "\n")
	var out bytes.Buffer
	if err := s.serve(in, &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Result struct {
			ProtocolVersion string         `json:"protocolVersion"`
			Capabilities    map[string]any `json:"capabilities"`
			ServerInfo      map[string]any `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("响应不是合法 JSON: %s", out.String())
	}
	if resp.Result.ProtocolVersion == "" {
		t.Error("缺 protocolVersion")
	}
	if _, ok := resp.Result.Capabilities["tools"]; !ok {
		t.Error("缺 tools 能力")
	}
	if resp.Result.ServerInfo["name"] != serverName {
		t.Errorf("serverInfo.name = %v", resp.Result.ServerInfo["name"])
	}
}

// 通知（没有 id）不该产生任何响应行。
func TestMCPNotificationIgnored(t *testing.T) {
	s := &server{addr: "http://127.0.0.1:1"}
	in := strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")
	var out bytes.Buffer
	if err := s.serve(in, &out); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("通知不该有响应：%q", out.String())
	}
}

// tools/list 暴露图像与视频两个工具，且 schema 合法。
func TestMCPToolsList(t *testing.T) {
	s := &server{addr: "http://127.0.0.1:1"}
	in := strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")
	var out bytes.Buffer
	if err := s.serve(in, &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Result struct {
			Tools []struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range resp.Result.Tools {
		names[tl.Name] = true
		if tl.Description == "" {
			t.Errorf("%s 缺描述", tl.Name)
		}
		if tl.InputSchema["type"] != "object" {
			t.Errorf("%s 的 inputSchema 不是对象", tl.Name)
		}
	}
	for _, want := range []string{"aether_image", "aether_video"} {
		if !names[want] {
			t.Errorf("缺工具 %s：%v", want, names)
		}
	}
}

// tools/call 真的把请求转发到网关（用假的网关验证路径与请求体）。
func TestMCPToolCallForwards(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"url":"http://img/1.png"}]}`))
	}))
	defer srv.Close()

	s := &server{addr: srv.URL}
	in := strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"aether_image","arguments":{"prompt":"a cat","size":"512x512"}}}` + "\n")
	var out bytes.Buffer
	if err := s.serve(in, &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Result.IsError {
		t.Fatalf("不该是错误：%+v", resp.Result)
	}
	if gotPath != "/v1/images/generations" {
		t.Errorf("转发路径 = %q", gotPath)
	}
	if gotBody["prompt"] != "a cat" || gotBody["size"] != "512x512" {
		t.Errorf("请求体 = %+v", gotBody)
	}
	if len(resp.Result.Content) != 1 || !strings.Contains(resp.Result.Content[0].Text, "img/1.png") {
		t.Errorf("返回内容 = %+v", resp.Result.Content)
	}
}

// 工具执行失败按 MCP 约定放进 result 的 isError，而不是 RPC 层报错。
func TestMCPToolCallErrorShape(t *testing.T) {
	s := &server{addr: "http://127.0.0.1:1"} // 连不上
	in := strings.NewReader(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"aether_image","arguments":{"prompt":"x"}}}` + "\n")
	var out bytes.Buffer
	if err := s.serve(in, &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Error  any `json:"error"`
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("不该是 RPC 错误：%+v", resp.Error)
	}
	if !resp.Result.IsError {
		t.Fatal("应标记 isError")
	}
}

// 空的 prompt 要被拒（避免把空请求打给上游）。
func TestMCPRejectsEmptyPrompt(t *testing.T) {
	s := &server{addr: "http://127.0.0.1:1"}
	if _, err := s.callTool("aether_image", json.RawMessage(`{"prompt":"  "}`)); err == nil {
		t.Fatal("空 prompt 应报错")
	}
	if _, err := s.callTool("nope", json.RawMessage(`{"prompt":"x"}`)); err == nil {
		t.Fatal("未知工具应报错")
	}
}
