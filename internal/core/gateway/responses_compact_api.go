package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"aether-cockpit/internal/core/pool"
	"aether-cockpit/internal/provider"
)

// handleResponsesCompact POST /v1/responses/compact
//
// OpenAI Responses 的上下文压缩：按 model 找到支持 compact 的原生渠道，
// 选一个可用账号，原样转发（只做 instructions/tools 的官方契约清理）。
func (g *Gateway) handleResponsesCompact(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "读取请求体失败"})
		return
	}
	var meta struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(raw, &meta) != nil || meta.Model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 model"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	var lastErr error
	for _, b := range g.models.Resolve(meta.Model) {
		p, ok := provider.Get(b.ProviderID)
		if !ok {
			continue
		}
		cap, ok := p.(provider.ResponsesCompactCapability)
		if !ok {
			continue
		}
		env, selErr := g.pool.Select(b.ProviderID, pool.SelectOptions{Model: b.UpstreamModel})
		if selErr != nil {
			lastErr = selErr
			continue
		}
		out, err := cap.CompactResponses(ctx, env, raw)
		if err != nil {
			lastErr = err
			continue
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(out)
		return
	}
	if lastErr != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": lastErr.Error()})
		return
	}
	writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "没有支持上下文压缩的渠道"})
}
