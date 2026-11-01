package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"aether-cockpit/internal/core/tools"
)

// TestImageBatchFanOut 验证本地批量扇出：逐条透传、逐条结果、上游请求数正确。
func TestImageBatchFanOut(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var payload map[string]any
		body, _ := json.Marshal(nil)
		_ = body
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("上游请求不是 JSON: %v", err)
		}
		if payload["model"] != "img-1" {
			t.Errorf("model 未注入: %v", payload["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"url":"https://cdn.example/x.png"}]}`))
	}))
	defer upstream.Close()

	g := New(Config{Addr: "127.0.0.1:0", UsagePath: "-"})
	g.settings.Tools = &ToolsConfig{Image: &tools.ImageConfig{BaseURL: upstream.URL, Model: "img-1"}}

	body := `{"requests":[{"prompt":"a"},{"prompt":"b"},{"prompt":"c"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/batches", strings.NewReader(body))
	g.handleImageBatch(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Count   int `json:"count"`
		Results []struct {
			Index  int             `json:"index"`
			Status int             `json:"status"`
			Result json.RawMessage `json:"result"`
			Error  string          `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应非法: %s", rec.Body.String())
	}
	if out.Count != 3 || len(out.Results) != 3 {
		t.Fatalf("结果数量 = %+v", out)
	}
	for i, item := range out.Results {
		if item.Index != i || item.Status != http.StatusOK || item.Error != "" {
			t.Fatalf("第 %d 条 = %+v", i, item)
		}
		if !strings.Contains(string(item.Result), "cdn.example") {
			t.Fatalf("第 %d 条结果未透传: %s", i, item.Result)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("上游请求数 = %d", calls.Load())
	}

	// 上限保护。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/images/batches", strings.NewReader(`{"requests":[{}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}]}`))
	g.handleImageBatch(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("超过 16 条应拒绝，实际 %d", rec.Code)
	}
}
