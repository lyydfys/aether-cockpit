package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/tools"
)

// TestVideoProxyFamily 覆盖视频生成的三条路径：
// 创建（补模型 + Bearer）、轮询、内容下载（二进制 §Content-Type）。
func TestVideoProxyFamily(t *testing.T) {
	var gotAuth, gotModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/videos/generations" && r.Method == http.MethodPost:
			gotAuth = r.Header.Get("Authorization")
			var payload map[string]any
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &payload)
			gotModel, _ = payload["model"].(string)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"request_id":"req_1","status":"pending"}`))
		case r.URL.Path == "/videos/req_1" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"request_id":"req_1","status":"done","video":{"url":"https://cdn.example/v.mp4"}}`))
		case r.URL.Path == "/videos/req_1/content":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("MP4DATA"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	g := New(Config{Addr: "127.0.0.1:0", UsagePath: "-"})
	g.settings.Tools = &ToolsConfig{Video: &tools.VideoConfig{
		BaseURL: upstream.URL, APIKey: "xai-key", Model: "grok-imagine-video",
	}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/videos/generations", strings.NewReader(`{"prompt":"a cat surfing"}`))
	g.handleVideoGenerate(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	if gotAuth != "Bearer xai-key" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotModel != "grok-imagine-video" {
		t.Fatalf("model 未注入: %q", gotModel)
	}
	if !strings.Contains(rec.Body.String(), "req_1") {
		t.Fatalf("响应未透传: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/videos/req_1", nil)
	req.SetPathValue("id", "req_1")
	g.handleVideoGet(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"done"`) {
		t.Fatalf("poll = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/videos/req_1/content", nil)
	req.SetPathValue("id", "req_1")
	g.handleVideoContent(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "video/mp4" || rec.Body.String() != "MP4DATA" {
		t.Fatalf("content = %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
}

func TestVideoAsyncTask(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		_, _ = w.Write([]byte(`{"request_id":"req_2","status":"pending"}`))
	}))
	defer upstream.Close()

	g := New(Config{Addr: "127.0.0.1:0", UsagePath: "-"})
	g.settings.Tools = &ToolsConfig{Video: &tools.VideoConfig{BaseURL: upstream.URL}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/videos/generations/async", strings.NewReader(`{"prompt":"x"}`))
	g.handleVideoGenerateAsync(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("async status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("任务 id 缺失: %s", rec.Body.String())
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/v1/video-tasks/"+created.ID, nil)
		req.SetPathValue("id", created.ID)
		g.handleVideoTaskGet(rec, req)
		var task imageTask
		if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
			t.Fatalf("任务响应非法: %s", rec.Body.String())
		}
		if task.Status == "succeeded" {
			if !strings.Contains(string(task.Result), "req_2") {
				t.Fatalf("结果未保存: %s", task.Result)
			}
			return
		}
		if task.Status == "failed" {
			t.Fatalf("任务失败: %+v", task)
		}
		if time.Now().After(deadline) {
			t.Fatalf("任务未在期限内完成: %+v", task)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestVideoConfigMasking(t *testing.T) {
	s := runtimeSettings{Tools: &ToolsConfig{Video: &tools.VideoConfig{BaseURL: "https://api.x.ai/v1", APIKey: "xai-secret"}}}
	masked := maskTools(s)
	if masked.Tools.Video.APIKey != webdavPasswordMask {
		t.Fatalf("mask = %q", masked.Tools.Video.APIKey)
	}
	if s.Tools.Video.APIKey != "xai-secret" {
		t.Fatal("原始配置被改写")
	}

	g := New(Config{Addr: "127.0.0.1:0", UsagePath: "-"})
	g.settings.Tools = &ToolsConfig{Video: &tools.VideoConfig{BaseURL: "https://api.x.ai/v1", APIKey: "xai-secret"}}
	g.applyToolsConfig(&ToolsConfig{Video: &tools.VideoConfig{BaseURL: "https://api.x.ai/v1", APIKey: webdavPasswordMask}})
	if g.settings.Tools.Video.APIKey != "xai-secret" {
		t.Fatalf("打码占位符未沿用旧密钥: %q", g.settings.Tools.Video.APIKey)
	}
}
