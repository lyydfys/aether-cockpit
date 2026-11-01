package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"aether-cockpit/internal/provider"
)

const typesafeDefaultBase = "https://api.typesafe.ai"

// handleSystemOne POST /v1/systemone
//
// 把 TypeSafe Jev 的 System One 请求原样转发到 typesafe 渠道：
// Bearer API Key、非流式、不做 IR 翻译（决策请求的 state/questions 结构
// 由调用方给出，网关不解释）。没有可用账号时如实报 503。
func (g *Gateway) handleSystemOne(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeDecisionError(w, http.StatusBadRequest, "读取请求体失败: "+err.Error())
		return
	}
	var probe struct {
		Model  string `json:"model"`
		Stream *bool  `json:"stream"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		writeDecisionError(w, http.StatusBadRequest, "请求体必须是 JSON")
		return
	}
	model := strings.TrimPrefix(strings.TrimSpace(probe.Model), "typesafe/")
	if model == "" {
		writeDecisionError(w, http.StatusBadRequest, "缺少 model（固定 jev）")
		return
	}
	if model != "jev" {
		writeDecisionError(w, http.StatusBadRequest, "TypeSafe 只接受 model=jev")
		return
	}
	if probe.Stream != nil && *probe.Stream {
		writeDecisionError(w, http.StatusBadRequest, "System One 不支持流式")
		return
	}

	var env *provider.AccountEnv
	for _, e := range g.pool.ListByProvider("typesafe") {
		if e.Available() {
			env = e
			break
		}
	}
	if env == nil {
		writeDecisionError(w, http.StatusServiceUnavailable, "没有可用的 TypeSafe 账号，请先在供应商页添加")
		return
	}
	key := strings.TrimSpace(env.GetCredential("api_key"))
	if key == "" {
		writeDecisionError(w, http.StatusBadGateway, "TypeSafe 账号缺少 API Key")
		return
	}
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		base = typesafeDefaultBase
	}
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, base+"/v1/systemone", bytes.NewReader(raw))
	if err != nil {
		writeDecisionError(w, http.StatusBadGateway, "构造上游请求失败: "+err.Error())
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+key)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := env.Client().Do(httpReq)
	if err != nil {
		writeDecisionError(w, http.StatusBadGateway, "TypeSafe 上游连接失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 8<<20))
}

func writeDecisionError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": msg, "type": "gateway_error"},
	})
}
