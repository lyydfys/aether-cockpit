package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/provider"
)

// 「检测协议」与「获取模型」——供应商编辑器里用来摸清一个未知端点。
//
// 两者都真发请求：检测按四种协议各发一次最小调用，获取模型读 /models。
// 失败如实带状态码与响应片段，不把"字段非空"当连通性。

const detectTimeout = 12 * time.Second

type detectResult struct {
	Protocol string `json:"protocol"`
	Base     string `json:"base,omitempty"`
	OK       bool   `json:"ok"`
	Status   int    `json:"status,omitempty"`
	Ms       int64  `json:"ms"`
	Model    string `json:"model,omitempty"`
	Error    string `json:"error,omitempty"`
}

// baseFor 去掉已知端点后缀，得到协议无关的 base。
func baseFor(raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	for _, suffix := range []string{"/chat/completions", "/responses", "/v1/messages", "/messages"} {
		raw = strings.TrimSuffix(raw, suffix)
	}
	return strings.TrimRight(raw, "/")
}

func joinURL(base, path string) string {
	if base == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return strings.TrimRight(base, "/") + path
}

// handleProviderDetect POST /api/provider/detect
func (g *Gateway) handleProviderDetect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID          string            `json:"id"`
		Base        string            `json:"base"`
		Chat        string            `json:"chat"`
		Responses   string            `json:"responses"`
		Anthropic   string            `json:"anthropic"`
		Gemini      string            `json:"gemini"`
		Key         string            `json:"key"`
		Model       string            `json:"model"`
		Headers     map[string]string `json:"headers"`
		InsecureTLS bool              `json:"insecureTLS"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	key := strings.TrimSpace(body.Key)
	if key == "" && body.ID != "" && g.pool != nil {
		for _, e := range g.pool.ListByProvider(body.ID) {
			key = e.GetCredential("api_key")
			if key == "" {
				key = e.GetCredential("access_token")
			}
			if key != "" {
				break
			}
		}
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		model = "gpt-4o-mini"
		if p, ok := provider.Get(body.ID); ok {
			if ms := p.Manifest().Models; len(ms) > 0 {
				model = ms[0].UpstreamModel
				if model == "" {
					model = ms[0].ID
				}
			}
		}
	}

	// 每个协议候选：显式端点优先，否则用通用 base 拼出该协议的标准路径。
	type candidate struct{ protocol, base string }
	var cands []candidate
	for _, c := range []struct{ protocol, raw string }{
		{"chat", body.Chat}, {"responses", body.Responses},
		{"anthropic", body.Anthropic}, {"gemini", body.Gemini},
	} {
		if b := baseFor(c.raw); b != "" {
			cands = append(cands, candidate{c.protocol, b})
		}
	}
	if b := baseFor(body.Base); b != "" {
		seen := map[string]bool{}
		for _, c := range cands {
			seen[c.protocol] = true
		}
		for _, p := range []string{"chat", "responses", "anthropic", "gemini"} {
			if !seen[p] {
				cands = append(cands, candidate{p, b})
			}
		}
	}
	if len(cands) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "先填一个 Base URL"})
		return
	}

	client := &http.Client{Timeout: detectTimeout}
	results := make([]detectResult, 0, len(cands))
	for _, c := range cands {
		results = append(results, probeProtocol(r.Context(), client, c.protocol, c.base, key, model, body.Headers))
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func probeProtocol(ctx context.Context, client *http.Client, protocol, base, key, model string, headers map[string]string) detectResult {
	start := time.Now()
	res := detectResult{Protocol: protocol, Base: base, Model: model}
	ctx, cancel := context.WithTimeout(ctx, detectTimeout)
	defer cancel()

	var req *http.Request
	var err error
	switch protocol {
	case "chat":
		payload, _ := json.Marshal(map[string]any{
			"model": model, "messages": []map[string]string{{"role": "user", "content": "ping"}}, "max_tokens": 8,
		})
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, joinURL(base, "/chat/completions"), bytes.NewReader(payload))
		if req != nil {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	case "responses":
		payload, _ := json.Marshal(map[string]any{"model": model, "input": "ping", "max_output_tokens": 8})
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, joinURL(base, "/responses"), bytes.NewReader(payload))
		if req != nil {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	case "anthropic":
		payload, _ := json.Marshal(map[string]any{
			"model": model, "max_tokens": 8, "messages": []map[string]string{{"role": "user", "content": "ping"}},
		})
		// base 可能已经带了 /v1（用户粘贴的就是消息端点前缀）。
		path := "/v1/messages"
		if strings.HasSuffix(strings.ToLower(base), "/v1") {
			path = "/messages"
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, joinURL(base, path), bytes.NewReader(payload))
		if req != nil {
			req.Header.Set("x-api-key", key)
			req.Header.Set("anthropic-version", "2023-06-01")
		}
	case "gemini":
		payload, _ := json.Marshal(map[string]any{
			"contents": []map[string]any{{"parts": []map[string]string{{"text": "ping"}}}},
		})
		prefix := "/v1beta"
		if strings.HasSuffix(strings.ToLower(base), "/v1beta") {
			prefix = ""
		}
		endpoint := joinURL(base, prefix+"/models/"+model+":generateContent")
		if key != "" {
			endpoint += "?key=" + key
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	}
	if err != nil {
		res.Error = err.Error()
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	res.Ms = time.Since(start).Milliseconds()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer resp.Body.Close()
	res.Status = resp.StatusCode
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		res.OK = true
		return res
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	res.Error = strings.TrimSpace(string(snippet))
	if res.Error == "" {
		res.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return res
}

// handleProviderModelList POST /api/provider/models —— 真向供应商要模型清单。
func (g *Gateway) handleProviderModelList(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID    string `json:"id"`
		Base  string `json:"base"`
		Chat  string `json:"chat"`
		Key   string `json:"key"`
		Model string `json:"model"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	key := strings.TrimSpace(body.Key)
	id := strings.TrimSpace(body.ID)
	base := baseFor(body.Chat)
	if base == "" {
		base = baseFor(body.Base)
	}
	if base == "" && body.ID != "" && g.pool != nil {
		for _, e := range g.pool.ListByProvider(body.ID) {
			base = baseFor(e.GetCredential("base_url"))
			if key == "" {
				key = e.GetCredential("api_key")
			}
			if base != "" {
				break
			}
		}
	}

	// 1) 优先用渠道自己的模型目录实现（CodeBuddy/WorkBuddy → /v3/config，
	//    OpenAI 兼容 → spec 的端点，而不是硬拼 /models）。这是 CodeBuddy
	//    "登录成功但获取模型 404"的根因修复点：它的站点没有 /models。
	var vendorErr error
	if id != "" {
		if p, ok := provider.Get(id); ok {
			if lister, ok := p.(provider.ModelLister); ok {
				env := g.modelListEnv(id)
				supported := env != nil
				if env == nil {
					vendorErr = fmt.Errorf("渠道 %s 还没有可用账号，无法读取上游模型目录", id)
				} else if support, ok := p.(provider.ModelListSupport); ok && !support.SupportsModelList(env) {
					supported = false
					vendorErr = fmt.Errorf("渠道 %s 没有可用的模型列表端点", id)
				}
				if supported {
					ids, err := lister.ListModels(r.Context(), env)
					if err == nil && len(ids) > 0 {
						writeJSON(w, http.StatusOK, map[string]any{
							"count": len(ids), "models": ids, "dropped": []string{},
							"source": "vendor",
						})
						return
					}
					if err != nil {
						vendorErr = err
					} else {
						vendorErr = fmt.Errorf("渠道 %s 上游返回了空模型列表", id)
					}
				}
			}
			// 2) 上游目录不可用时退回内置/已保存清单：即便账号 token 过期或
			//    站点没有 /models，页面也不该只弹一个 404。
			if ids := registryModelIDs(id); len(ids) > 0 {
				resp := map[string]any{
					"count": len(ids), "models": ids, "dropped": []string{},
					"source": "registry",
				}
				if vendorErr != nil {
					resp["vendor_error"] = vendorErr.Error()
				}
				writeJSON(w, http.StatusOK, resp)
				return
			}
		}
	}

	// 3) 未知渠道 / 还没保存的自定义端点：沿用"直接 GET base+/models"的探测。
	if base == "" {
		if vendorErr != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": vendorErr.Error()})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "先填一个 Base URL，或先保存渠道"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), detectTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(base, "/models"), nil)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: detectTimeout}
	resp, err := client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "连接失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": fmt.Sprintf("供应商返回 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw[:min(len(raw), 300)]))),
		})
		return
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	_ = json.Unmarshal(raw, &payload)
	ids := make([]string, 0, len(payload.Data)+len(payload.Models))
	for _, d := range payload.Data {
		if d.ID != "" {
			ids = append(ids, d.ID)
		}
	}
	for _, m := range payload.Models {
		name := strings.TrimPrefix(m.Name, "models/")
		if name != "" {
			ids = append(ids, name)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count": len(ids), "models": ids, "dropped": []string{},
	})
}

// modelListEnv 选一个可用于读取上游模型目录的账号：优先可用账号，
// 其次第一个未停用账号；没有账号返回 nil。
func (g *Gateway) modelListEnv(pid string) *provider.AccountEnv {
	if g.pool == nil {
		return nil
	}
	var fallback *provider.AccountEnv
	for _, env := range g.pool.ListByProvider(pid) {
		if env == nil || env.Disabled() {
			continue
		}
		if env.Available() {
			return env
		}
		if fallback == nil {
			fallback = env
		}
	}
	return fallback
}

// registryModelIDs 返回渠道静态清单里的模型名（去掉渠道前缀）。
func registryModelIDs(pid string) []string {
	p, ok := provider.Get(pid)
	if !ok {
		return nil
	}
	m := p.Manifest()
	ids := make([]string, 0, len(m.Models))
	for _, mm := range m.Models {
		id := strings.TrimSpace(mm.UpstreamModel)
		if id == "" {
			id = strings.TrimPrefix(mm.ID, m.ID+"/")
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
