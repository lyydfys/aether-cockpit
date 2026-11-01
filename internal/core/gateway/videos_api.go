package gateway

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aether-cockpit/internal/core/tools"
)

// 视频生成：代理到设置页配置的视频服务。
//
// 兼容两种上游形状：
//   - xAI Grok Imagine：POST /v1/videos/generations → {request_id}，
//     再 GET /v1/videos/{request_id} 轮询 status/progress/video.url；
//   - OpenAI Sora：POST /v1/videos（multipart/JSON），GET /v1/videos/{id}/content。
//
// 另外提供 /v1/videos/generations/async + /v1/video-tasks/{id} 的本地
// 异步包装，供不支持任务轮询的客户端使用（任务表与图像异步共用）。
// 任务查询刻意不用 /v1/videos/tasks/{id}：它会和 /v1/videos/{id}/content
// 在 Go 1.22 路由表里互相冲突。

// videoConfig 返回当前视频配置（无配置时 ok=false）。
func (g *Gateway) videoConfig() (tools.VideoConfig, bool) {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.Tools == nil || g.settings.Tools.Video == nil {
		return tools.VideoConfig{}, false
	}
	cfg := *g.settings.Tools.Video
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return tools.VideoConfig{}, false
	}
	return cfg, true
}

func (g *Gateway) writeVideoError(w http.ResponseWriter) {
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"error": "尚未配置视频生成：请在设置页「工具服务 · 视频服务」填写 base_url（xAI /v1/videos/generations 或 OpenAI /v1/videos）",
	})
}

// handleVideoGenerate POST /v1/videos/generations
func (g *Gateway) handleVideoGenerate(w http.ResponseWriter, r *http.Request) {
	cfg, ok := g.videoConfig()
	if !ok {
		g.writeVideoError(w)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "读取请求体失败"})
		return
	}
	if cfg.Model != "" {
		body, _ = injectJSONModel(body, cfg.Model)
	}
	code, ct, out, err := tools.ProxyVideo(r.Context(), cfg, http.MethodPost, "/videos/generations", "application/json", body)
	g.writeVideoResult(w, code, ct, out, err)
}

// handleVideoCreate POST /v1/videos（OpenAI Sora 兼容，multipart 或 JSON）
func (g *Gateway) handleVideoCreate(w http.ResponseWriter, r *http.Request) {
	cfg, ok := g.videoConfig()
	if !ok {
		g.writeVideoError(w)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 128<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "读取请求体失败"})
		return
	}
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(strings.ToLower(ct), "application/json") && cfg.Model != "" {
		body, _ = injectJSONModel(body, cfg.Model)
	}
	code, outCT, out, err := tools.ProxyVideo(r.Context(), cfg, http.MethodPost, "/videos", ct, body)
	g.writeVideoResult(w, code, outCT, out, err)
}

// handleVideoGet GET /v1/videos/{id}（状态轮询）
func (g *Gateway) handleVideoGet(w http.ResponseWriter, r *http.Request) {
	cfg, ok := g.videoConfig()
	if !ok {
		g.writeVideoError(w)
		return
	}
	id := url.PathEscape(r.PathValue("id"))
	code, ct, out, err := tools.ProxyVideo(r.Context(), cfg, http.MethodGet, "/videos/"+id, "", nil)
	g.writeVideoResult(w, code, ct, out, err)
}

// handleVideoContent GET /v1/videos/{id}/content（Sora 结果下载）
func (g *Gateway) handleVideoContent(w http.ResponseWriter, r *http.Request) {
	cfg, ok := g.videoConfig()
	if !ok {
		g.writeVideoError(w)
		return
	}
	id := url.PathEscape(r.PathValue("id"))
	code, ct, out, err := tools.ProxyVideo(r.Context(), cfg, http.MethodGet, "/videos/"+id+"/content", "", nil)
	g.writeVideoResult(w, code, ct, out, err)
}

// handleVideoGenerateAsync POST /v1/videos/generations/async
//
// 本地异步包装：立刻返回任务 id，后台跑上游生成（可能耗时数分钟）。
func (g *Gateway) handleVideoGenerateAsync(w http.ResponseWriter, r *http.Request) {
	cfg, ok := g.videoConfig()
	if !ok {
		g.writeVideoError(w)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "读取请求体失败"})
		return
	}
	if cfg.Model != "" {
		body, _ = injectJSONModel(body, cfg.Model)
	}
	t := g.imageTaskStart()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		code, _, out, err := tools.ProxyVideo(ctx, cfg, http.MethodPost, "/videos/generations", "application/json", body)
		g.imageTaskFinish(t, code, out, err)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"id": t.ID, "request_id": t.ID, "status": "pending"})
}

// handleVideoTaskGet GET /v1/video-tasks/{id}
func (g *Gateway) handleVideoTaskGet(w http.ResponseWriter, r *http.Request) {
	t := g.imageTaskSnapshot(r.PathValue("id"))
	if t == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "任务不存在或已过期"})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// writeVideoResult 透传上游状态码/Content-Type（二进制结果不能按 JSON 写出）。
func (g *Gateway) writeVideoResult(w http.ResponseWriter, code int, ct string, body []byte, err error) {
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	if ct == "" {
		ct = "application/json"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(code)
	_, _ = w.Write(body)
}
