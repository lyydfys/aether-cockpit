package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckMCPHTTPJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") == "" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("headers = %v", r.Header)
		}
		var body struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch body.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "s-1")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if r.Header.Get("Mcp-Session-Id") != "s-1" {
				t.Errorf("session = %q", r.Header.Get("Mcp-Session-Id"))
			}
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"alpha"},{"name":"beta"}]}}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	res := checkMCPHTTP(context.Background(), srv.URL, map[string]string{"Authorization": "Bearer t"})
	if res.State != "ok" || res.Tools != 2 || len(res.ToolList) != 2 {
		t.Fatalf("res = %+v", res)
	}
	if res.ToolList[0] != "alpha" || res.ToolList[1] != "beta" {
		t.Fatalf("tools = %v", res.ToolList)
	}
}

func TestCheckMCPHTTPSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Method == "tools/list" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"tools\":[{\"name\":\"sse-tool\"}]}}\n\n"))
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()

	res := checkMCPHTTP(context.Background(), srv.URL, nil)
	if res.State != "ok" || res.Tools != 1 || res.ToolList[0] != "sse-tool" {
		t.Fatalf("res = %+v", res)
	}
}

func TestCheckMCPHTTPAuthAndRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"sign in"}`))
	}))
	defer srv.Close()
	res := checkMCPHTTP(context.Background(), srv.URL, nil)
	if res.State != "auth" || res.Code != 401 {
		t.Fatalf("res = %+v", res)
	}

	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := closed.URL
	closed.Close()
	res = checkMCPHTTP(context.Background(), url, nil)
	if res.State != "" || res.Why == "" || res.Step != "initialize" {
		t.Fatalf("closed res = %+v", res)
	}
	if !strings.Contains(res.Detail, "refused") && res.Why != "refused" {
		t.Fatalf("detail = %q why = %q", res.Detail, res.Why)
	}
}
