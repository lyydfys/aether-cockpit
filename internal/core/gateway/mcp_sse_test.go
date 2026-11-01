package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckMCPSSE(t *testing.T) {
	msgCh := make(chan string, 16)
	done := make(chan struct{})
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			fmt.Fprint(w, "event: endpoint\ndata: "+srv.URL+"/messages?sessionId=1\n\n")
			fl.Flush()
			for {
				select {
				case m := <-msgCh:
					fmt.Fprintf(w, "event: message\ndata: %s\n\n", m)
					fl.Flush()
				case <-done:
					return
				}
			}
		}
		var body struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch body.Method {
		case "initialize":
			msgCh <- fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{}}`, body.ID)
		case "tools/list":
			msgCh <- fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"s1"},{"name":"s2"}]}}`, body.ID)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer func() {
		close(done)
		srv.Close()
	}()

	res := checkMCPSSE(context.Background(), srv.URL+"/sse", map[string]string{"Authorization": "Bearer t"})
	if res.State != "ok" || res.Tools != 2 || len(res.ToolList) != 2 {
		t.Fatalf("res = %+v", res)
	}
	if res.ToolList[0] != "s1" || res.ToolList[1] != "s2" {
		t.Fatalf("tools = %v", res.ToolList)
	}
}

func TestCheckMCPSSEAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	res := checkMCPSSE(context.Background(), srv.URL, nil)
	if res.State != "auth" || res.Code != 401 {
		t.Fatalf("res = %+v", res)
	}
}

func TestCheckMCPSSETimeout(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		<-done
	}))
	defer func() {
		close(done)
		srv.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	res := checkMCPSSE(ctx, srv.URL, nil)
	if res.Why != "timeout" || res.Step != "endpoint" {
		t.Fatalf("res = %+v", res)
	}
}
