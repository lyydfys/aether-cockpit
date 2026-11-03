package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 图像：edits、异步任务与任务轮询。上游统一走设置页配置的 OpenAI 兼容图像服务。

type imageTask struct {
	ID          string          `json:"id"`
	Status      string          `json:"status"`
	StatusCode  int             `json:"status_code,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   int64           `json:"created_at"`
	CompletedAt int64           `json:"completed_at,omitempty"`
}

func (g *Gateway) imageTaskStart() *imageTask {
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	t := &imageTask{ID: "img_" + hex.EncodeToString(id), Status: "pending", CreatedAt: time.Now().Unix()}
	g.imageTasksMu.Lock()
	if g.imageTasks == nil {
		g.imageTasks = map[string]*imageTask{}
	}
	// 清理 1 小时前的任务，避免长跑后无限增长。
	for k, v := range g.imageTasks {
		if time.Since(time.Unix(v.CreatedAt, 0)) > time.Hour {
			delete(g.imageTasks, k)
		}
	}
	g.imageTasks[t.ID] = t
	g.imageTasksMu.Unlock()
	return t
}

func (g *Gateway) imageTaskFinish(t *imageTask, code int, out []byte, err error) {
	g.imageTasksMu.Lock()
	defer g.imageTasksMu.Unlock()
	t.CompletedAt = time.Now().Unix()
	t.StatusCode = code
	if err != nil {
		t.Status = "failed"
		t.Error = err.Error()
		return
	}
	if code < 200 || code >= 300 {
		t.Status = "failed"
		t.Error = strings.TrimSpace(string(out))
		if len(t.Error) > 500 {
			t.Error = t.Error[:500]
		}
		return
	}
	t.Status = "succeeded"
	t.Result = json.RawMessage(out)
}

func (g *Gateway) imageTaskSnapshot(id string) *imageTask {
	g.imageTasksMu.Lock()
	defer g.imageTasksMu.Unlock()
	t := g.imageTasks[id]
	if t == nil {
		return nil
	}
	cp := *t
	return &cp
}

// handleImageEdit POST /v1/images/edits（multipart 或 JSON 原样透传）
func (g *Gateway) handleImageEdit(w http.ResponseWriter, r *http.Request) {
	cfg, settingsOK := g.imageConfig()
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "读取请求体失败"})
		return
	}
	target, terr := g.mediaTargetFor(r, body)
	if terr != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": mediaTargetError(terr)})
		return
	}
	if !settingsOK && target.BaseURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "尚未配置图像生成：请在设置页填写 base_url，或把生图模型挂到某个渠道上"})
		return
	}
	// multipart 里夹着图片二进制，无法安全重写 model 字段；只有 JSON 编辑请求
	// 能把虚拟名换成渠道的上游名（multipart 场景客户端直接填上游模型名或 ?model=）。
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		body = applyModelToBody(body, target, cfg.Model)
	}
	cfg = target.applyToImage(cfg)
	code, out, err := proxyImageEdit(r.Context(), cfg, body, r.Header.Get("Content-Type"))
	g.noteMediaResult(target, code, err)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(out)
}

// handleImageGenerateAsync POST /v1/images/generations/async
func (g *Gateway) handleImageGenerateAsync(w http.ResponseWriter, r *http.Request) {
	cfg, settingsOK := g.imageConfig()
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "读取请求体失败"})
		return
	}
	target, terr := g.mediaTargetFor(r, body)
	if terr != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": mediaTargetError(terr)})
		return
	}
	if !settingsOK && target.BaseURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "尚未配置图像生成：请在设置页填写 base_url，或把生图模型挂到某个渠道上"})
		return
	}
	body = applyModelToBody(body, target, cfg.Model)
	cfg = target.applyToImage(cfg)
	t := g.imageTaskStart()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		code, out, err := generateImage(ctx, cfg, body)
		g.noteMediaResult(target, code, err)
		g.imageTaskFinish(t, code, out, err)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"id": t.ID, "request_id": t.ID, "status": "pending"})
}

// handleImageEditAsync POST /v1/images/edits/async
func (g *Gateway) handleImageEditAsync(w http.ResponseWriter, r *http.Request) {
	cfg, settingsOK := g.imageConfig()
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "读取请求体失败"})
		return
	}
	target, terr := g.mediaTargetFor(r, body)
	if terr != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": mediaTargetError(terr)})
		return
	}
	if !settingsOK && target.BaseURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "尚未配置图像生成：请在设置页填写 base_url，或把生图模型挂到某个渠道上"})
		return
	}
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(strings.ToLower(ct), "application/json") {
		body = applyModelToBody(body, target, cfg.Model)
	}
	cfg = target.applyToImage(cfg)
	t := g.imageTaskStart()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		code, out, err := proxyImageEdit(ctx, cfg, body, ct)
		g.noteMediaResult(target, code, err)
		g.imageTaskFinish(t, code, out, err)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"id": t.ID, "request_id": t.ID, "status": "pending"})
}

// handleImageTaskGet GET /v1/images/tasks/{id}
func (g *Gateway) handleImageTaskGet(w http.ResponseWriter, r *http.Request) {
	t := g.imageTaskSnapshot(r.PathValue("id"))
	if t == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "任务不存在或已过期"})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// handleImageBatch POST /v1/images/batches
//
// 本地批量包装：最多 16 个生成请求并行（并发 4），返回逐条结果。
// 上游没有统一的批处理协议，这里只做扇出/聚合，错误按条返回不影响其他条。
func (g *Gateway) handleImageBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Requests []json.RawMessage `json:"requests"`
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "读取请求体失败"})
		return
	}
	if err := json.Unmarshal(raw, &body); err != nil || len(body.Requests) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "请求体需要 {requests:[{prompt:...}, ...]}（最多 16 条）",
		})
		return
	}
	if len(body.Requests) > 16 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "一次最多 16 条请求"})
		return
	}
	cfg, settingsOK := g.imageConfig()
	target, terr := g.mediaTargetFor(r, raw)
	if terr != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": mediaTargetError(terr)})
		return
	}
	if !settingsOK && target.BaseURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "尚未配置图像生成：请在设置页填写 base_url，或把生图模型挂到某个渠道上"})
		return
	}
	// 批量里的每一条只补缺省模型、不改写：同一次批量可能故意混用不同模型，
	// 统一改写会把用户的显式选择覆盖掉。
	cfg = target.applyToImage(cfg)

	type batchItem struct {
		Index     int             `json:"index"`
		Status    int             `json:"status,omitempty"`
		Result    json.RawMessage `json:"result,omitempty"`
		Error     string          `json:"error,omitempty"`
		ElapsedMS int64           `json:"elapsed_ms"`
	}
	results := make([]batchItem, len(body.Requests))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, req := range body.Requests {
		wg.Add(1)
		go func(i int, req json.RawMessage) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			started := time.Now()
			code, out, err := generateImage(r.Context(), cfg, req)
			g.noteMediaResult(target, code, err)
			item := batchItem{Index: i, Status: code, ElapsedMS: time.Since(started).Milliseconds()}
			if err != nil {
				item.Error = err.Error()
			} else if code < 200 || code >= 300 {
				item.Status = code
				item.Error = truncateForBatch(string(out))
			} else {
				item.Result = json.RawMessage(out)
			}
			results[i] = item
		}(i, req)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"count": len(results), "results": results})
}

func truncateForBatch(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300]
	}
	return s
}
