package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/library"
)

// 中继的 SSE 路径：tools/list 的流式响应也要去掉被关掉的工具。
func TestMCPRelayFiltersSSE(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"tools\":[{\"name\":\"a\"},{\"name\":\"b\"}]}}\n\n"))
	}))
	defer up.Close()

	g, _, h := agentApplyTestGateway(t)
	entry, err := g.library.SaveMCP(library.MCPEntry{
		Name: "demo", Config: json.RawMessage(`{"type":"http","url":"` + up.URL + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !g.library.UpdateMCPDisabledTools(entry.ID, []string{"b"}) {
		t.Fatal("set disabled tools failed")
	}
	if err := g.mcpAuthStore().put(&mcpAuthRecord{
		Name: "demo", Upstream: up.URL,
		Token:      oauthToken{AccessToken: "at-1", ExpiresAt: time.Now().Add(time.Hour).Unix()},
		SignedInAt: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/mcp/demo", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `"a"`) || strings.Contains(body, `"b"`) {
		t.Fatalf("SSE 未过滤: %s", body)
	}
	if !strings.Contains(body, "event: message") {
		t.Fatalf("SSE 其它字段被破坏: %s", body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q", ct)
	}
}
