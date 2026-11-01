package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderDetectPerProtocol(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
		case "/v1/messages":
			if r.Header.Get("x-api-key") != "sk-ant" {
				w.WriteHeader(401)
				return
			}
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"pong"}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	g := &Gateway{}
	body := `{"base":"` + srv.URL + `/v1","key":"sk-ant","model":"m"}`
	req := httptest.NewRequest("POST", "/api/provider/detect", strings.NewReader(body))
	rec := httptest.NewRecorder()
	g.handleProviderDetect(rec, req)

	var resp struct {
		Results []detectResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析失败: %v: %s", err, rec.Body.String())
	}
	byProto := map[string]detectResult{}
	for _, r := range resp.Results {
		byProto[r.Protocol] = r
	}
	if !byProto["chat"].OK {
		t.Errorf("chat 应通过: %+v", byProto["chat"])
	}
	// base 已带 /v1：anthropic 应请求 /v1/messages（不是 /v1/v1/messages）
	if !byProto["anthropic"].OK {
		t.Errorf("anthropic 应通过（base 带 /v1 时走 /messages）: %+v", byProto["anthropic"])
	}
	if byProto["responses"].OK || byProto["responses"].Status != 404 {
		t.Errorf("responses 应为 404 失败: %+v", byProto["responses"])
	}
	if byProto["chat"].Model != "m" || byProto["chat"].Ms < 0 {
		t.Errorf("结果元数据不对: %+v", byProto["chat"])
	}
}

func TestProviderModelList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-1" {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-chat"},{"id":"deepseek-reasoner"}]}`))
	}))
	defer srv.Close()

	g := &Gateway{}
	body := `{"chat":"` + srv.URL + `/v1","key":"sk-1"}`
	req := httptest.NewRequest("POST", "/api/provider/models", strings.NewReader(body))
	rec := httptest.NewRecorder()
	g.handleProviderModelList(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Count  int      `json:"count"`
		Models []string `json:"models"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Count != 2 || len(resp.Models) != 2 || resp.Models[0] != "deepseek-chat" {
		t.Fatalf("models = %+v", resp)
	}
}

func TestProviderModelListFallsBackToRegistry(t *testing.T) {
	g := &Gateway{}
	req := httptest.NewRequest("POST", "/api/provider/models", strings.NewReader(`{"id":"mock-native"}`))
	rec := httptest.NewRecorder()
	g.handleProviderModelList(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"source":"registry"`) {
		t.Fatalf("内置渠道应退回清单: %d %s", rec.Code, rec.Body.String())
	}
}
