package gateway

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/tools"
)

func TestImageEditsAndAsync(t *testing.T) {
	var editCT string
	var genCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-img" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/images/generations":
			genCalls++
			_, _ = w.Write([]byte(`{"data":[{"url":"https://img/gen.png"}]}`))
		case "/images/edits":
			editCT = r.Header.Get("Content-Type")
			_, _ = w.Write([]byte(`{"data":[{"b64_json":"ZWRpdA=="}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	g := New(Config{Addr: "127.0.0.1:0", UsagePath: "-", AccountStorePath: filepath.Join(t.TempDir(), "accounts.json")})
	g.settings.Tools = &ToolsConfig{Image: &tools.ImageConfig{BaseURL: srv.URL, APIKey: "sk-img", Model: "gpt-image-1"}}

	// multipart edits 透传
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("prompt", "make it blue")
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	g.handleImageEdit(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ZWRpdA") {
		t.Fatalf("edit status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(editCT, "multipart/form-data") {
		t.Fatalf("edit content-type = %q", editCT)
	}

	// 异步生成 → 轮询
	req = httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"prompt":"a cat"}`))
	rec = httptest.NewRecorder()
	g.handleImageGenerateAsync(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("async status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("async body: %v %s", err, rec.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	var task imageTask
	for time.Now().Before(deadline) {
		got := g.imageTaskSnapshot(created.ID)
		if got != nil {
			task = *got
			if task.Status != "pending" {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if task.Status != "succeeded" || !strings.Contains(string(task.Result), "gen.png") {
		t.Fatalf("task = %+v", task)
	}
	if genCalls == 0 {
		t.Fatal("generations 未被调用")
	}

	// JSON edits 也应补默认模型并透传
	req = httptest.NewRequest(http.MethodPost, "/v1/images/edits", strings.NewReader(`{"prompt":"x","image":"https://img/a.png"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	g.handleImageEdit(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("json edit status=%d", rec.Code)
	}
}
