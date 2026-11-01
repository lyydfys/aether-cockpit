package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/library"
)

func TestMCPToolsFilterAndSave(t *testing.T) {
	var calls []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var msg struct {
			Method string `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&msg)
		w.Header().Set("Content-Type", "application/json")
		switch msg.Method {
		case "tools/list":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"a"},{"name":"b"},{"name":"c"}]}}`))
		case "tools/call":
			calls = append(calls, msg.Params.Name)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"ok":true}}`))
		default:
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":3,"result":{}}`))
		}
	}))
	defer up.Close()

	g, _, h := agentApplyTestGateway(t)
	if _, err := g.library.SaveMCP(library.MCPEntry{
		Name: "demo", Config: json.RawMessage(`{"type":"http","url":"` + up.URL + `"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := g.mcpAuthStore().put(&mcpAuthRecord{
		Name: "demo", Upstream: up.URL,
		Token:      oauthToken{AccessToken: "at-1", ExpiresAt: time.Now().Add(time.Hour).Unix()},
		SignedInAt: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	out := applyReq(t, h, "/api/library/servers/tools", `{"name":"demo","names":["b"]}`)
	row := findRow(t, out, "servers", "demo")
	if off, _ := row["disabledTools"].([]any); len(off) != 1 || off[0] != "b" {
		t.Fatalf("disabledTools = %v", row["disabledTools"])
	}

	relay := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/mcp/demo", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:5555"
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := relay(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var list struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("tools/list 响应: %s", rec.Body.String())
	}
	names := []string{}
	for _, tool := range list.Result.Tools {
		names = append(names, tool.Name)
	}
	if len(names) != 2 || names[0] != "a" || names[1] != "c" {
		t.Fatalf("tools/list = %v", names)
	}

	rec = relay(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"b"}}`)
	if !strings.Contains(rec.Body.String(), "关闭") || len(calls) != 0 {
		t.Fatalf("关闭的工具应被拒绝: %s calls=%v", rec.Body.String(), calls)
	}

	rec = relay(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"a"}}`)
	if rec.Code != http.StatusOK || len(calls) != 1 || calls[0] != "a" {
		t.Fatalf("放行的工具应转发: %d %s calls=%v", rec.Code, rec.Body.String(), calls)
	}

	// 编辑服务器（同 URL）不应丢掉工具开关。
	out = applyReq(t, h, "/api/library/servers/save",
		`{"old":"demo","server":{"name":"demo","transport":"http","url":"`+up.URL+`","agents":[]}}`)
	row = findRow(t, out, "servers", "demo")
	if off, _ := row["disabledTools"].([]any); len(off) != 1 || off[0] != "b" {
		t.Fatalf("保存后 disabledTools = %v", row["disabledTools"])
	}
}

func TestFilterMCPToolsSSE(t *testing.T) {
	in := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"tools\":[{\"name\":\"a\"},{\"name\":\"b\"}]}}\n\n"
	var out bytes.Buffer
	filterMCPToolsSSE(&out, strings.NewReader(in), map[string]bool{"b": true})
	body := out.String()
	if !strings.Contains(body, `"a"`) || strings.Contains(body, `"b"`) {
		t.Fatalf("SSE 过滤 = %s", body)
	}
	if !strings.Contains(body, "event: message") {
		t.Fatalf("SSE 其它行被破坏: %s", body)
	}
}
