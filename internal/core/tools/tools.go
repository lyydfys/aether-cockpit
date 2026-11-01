// Package tools 提供网关内置的工具服务：网络搜索与图像生成。
//
// 定位与 magpie 的 tool services 一致：客户端（或模型）不必自己持有搜索/生图
// 供应商的密钥，由网关统一出网并计费。这里刻意只做"薄代理 + 结果归一"：
// 上游能力差异全部收敛成两个稳定的出口形状，接入新供应商只需加一个分支。
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SearchResult 是归一后的搜索结果。
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

// SearchConfig 是网络搜索的配置。
type SearchConfig struct {
	// Provider: tavily | serper | searxng | brave | exa | firecrawl | deepseek | you
	Provider string `json:"provider"`
	// Endpoint 覆盖默认地址（自建 searxng 必填）；brave/exa/firecrawl/deepseek/you
	// 视为基地址，路径由内置供应商拼接。
	Endpoint string `json:"endpoint,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
	// APIKeys 是多 Key（优先于 APIKey）；KeyOrder 为 rotate（默认）或 order。
	APIKeys  []string `json:"api_keys,omitempty"`
	KeyOrder string   `json:"key_order,omitempty"`
	// KeyCount 只用于 GET /api/settings 的展示（真实 Key 不外发）。
	KeyCount int `json:"key_count,omitempty"`
	// MaxResults 默认 5，上限 20。
	MaxResults int `json:"max_results,omitempty"`
	// TimeoutSeconds 默认 15。
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// ImageConfig 是图像生成的配置（OpenAI 兼容 /images/generations）。
type ImageConfig struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key,omitempty"`
	Model   string `json:"model,omitempty"`
	// TimeoutSeconds 默认 120（生图普遍慢）。
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

var httpClient = &http.Client{Timeout: 0}

// Search 按配置调用上游搜索并归一结果。
func Search(ctx context.Context, cfg SearchConfig, query string, max int) ([]SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query 不能为空")
	}
	if max <= 0 {
		max = cfg.MaxResults
	}
	if max <= 0 {
		max = 5
	}
	if max > 20 {
		max = 20
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	keys := SearchKeys(cfg)
	if len(keys) == 0 {
		return dispatchSearch(ctx, cfg, query, max)
	}
	var lastErr error
	for _, k := range orderedSearchKeys(cfg, keys) {
		attempt := cfg
		attempt.APIKey = k
		res, err := dispatchSearch(ctx, attempt, query, max)
		if err == nil {
			return res, nil
		}
		if isKeyRefusal(err) {
			coolKey(cfg, k)
			lastErr = err
			continue
		}
		return nil, err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("所有搜索 Key 都被上游拒绝")
	}
	return nil, lastErr
}

func dispatchSearch(ctx context.Context, cfg SearchConfig, query string, max int) ([]SearchResult, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", "tavily":
		return searchTavily(ctx, cfg, query, max)
	case "serper":
		return searchSerper(ctx, cfg, query, max)
	case "searxng":
		return searchSearxng(ctx, cfg, query, max)
	case "brave":
		return searchBrave(ctx, cfg, query, max)
	case "exa":
		return searchExa(ctx, cfg, query, max)
	case "firecrawl":
		return searchFirecrawl(ctx, cfg, query, max)
	case "deepseek":
		return searchDeepseek(ctx, cfg, query, max)
	case "you":
		return searchYou(ctx, cfg, query, max)
	default:
		return nil, fmt.Errorf("不支持的搜索供应商: %s（可选 tavily / serper / searxng / brave / exa / firecrawl / deepseek / you）", cfg.Provider)
	}
}

func searchTavily(ctx context.Context, cfg SearchConfig, query string, max int) ([]SearchResult, error) {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "https://api.tavily.com/search"
	}
	body, _ := json.Marshal(map[string]any{
		"api_key": cfg.APIKey, "query": query, "max_results": max,
	})
	var resp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := postJSON(ctx, endpoint, nil, body, &resp); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Content})
	}
	return out, nil
}

func searchSerper(ctx context.Context, cfg SearchConfig, query string, max int) ([]SearchResult, error) {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "https://google.serper.dev/search"
	}
	body, _ := json.Marshal(map[string]any{"q": query, "num": max})
	hdr := map[string]string{"X-API-KEY": cfg.APIKey}
	var resp struct {
		Organic []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic"`
	}
	if err := postJSON(ctx, endpoint, hdr, body, &resp); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(resp.Organic))
	for i, r := range resp.Organic {
		if i >= max {
			break
		}
		out = append(out, SearchResult{Title: r.Title, URL: r.Link, Snippet: r.Snippet})
	}
	return out, nil
}

func searchSearxng(ctx context.Context, cfg SearchConfig, query string, max int) ([]SearchResult, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("searxng 需要填写 endpoint（如 http://127.0.0.1:8888）")
	}
	u, err := url.Parse(strings.TrimRight(cfg.Endpoint, "/") + "/search")
	if err != nil {
		return nil, fmt.Errorf("endpoint 不是合法 URL: %w", err)
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searxng 请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp.StatusCode, raw)
	}
	var parsed struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("searxng 响应解析失败: %w", err)
	}
	out := make([]SearchResult, 0, len(parsed.Results))
	for i, r := range parsed.Results {
		if i >= max {
			break
		}
		out = append(out, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Content})
	}
	return out, nil
}

// GenerateImage 把请求体透传给上游 OpenAI 兼容的 /images/generations。
// 返回值是上游的原始状态码与响应体：生图响应形状多样（b64/url），透传最稳。
func GenerateImage(ctx context.Context, cfg ImageConfig, body []byte) (int, []byte, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return 0, nil, fmt.Errorf("图像生成未配置 base_url")
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, nil, fmt.Errorf("请求体不是合法 JSON: %w", err)
	}
	if cfg.Model != "" {
		if _, ok := payload["model"]; !ok {
			payload["model"] = cfg.Model
		}
	}
	out, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/images/generations", bytes.NewReader(out))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("图像上游请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("读取图像上游响应失败: %w", err)
	}
	return resp.StatusCode, raw, nil
}

func postJSON(ctx context.Context, endpoint string, headers map[string]string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("搜索上游请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode != http.StatusOK {
		return statusError(resp.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("搜索上游响应解析失败: %w", err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ProxyImage 把请求体原样转发到图像服务的指定路径（multipart/json 均可）。
// 用于 /images/edits 这类不能简单改 JSON 的端点。
func ProxyImage(ctx context.Context, cfg ImageConfig, path, contentType string, body []byte) (int, []byte, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return 0, nil, fmt.Errorf("图像生成未配置 base_url")
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("图像上游请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("读取图像上游响应失败: %w", err)
	}
	return resp.StatusCode, raw, nil
}

// AudioConfig 是语音转写/合成服务的配置（OpenAI 兼容 /audio/*）。
type AudioConfig struct {
	BaseURL        string `json:"base_url"`
	APIKey         string `json:"api_key,omitempty"`
	Model          string `json:"model,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// VideoConfig 是视频生成服务的配置。
//
// 两种主流形态都走同一组代理：
//   - xAI Grok Imagine：POST /videos/generations，GET /videos/{request_id} 轮询；
//   - OpenAI Sora 兼容：POST /videos，GET /videos/{id}/content 取结果。
type VideoConfig struct {
	BaseURL        string `json:"base_url"`
	APIKey         string `json:"api_key,omitempty"`
	Model          string `json:"model,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// RealtimeConfig 是实时语音（OpenAI Realtime / Live 兼容）服务的配置。
type RealtimeConfig struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key,omitempty"`
	Model   string `json:"model,omitempty"`
}

// ProxyRealtimeSessions 代理 POST /realtime/sessions（换取临时密钥）。
func ProxyRealtimeSessions(ctx context.Context, cfg RealtimeConfig, body []byte) (int, []byte, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return 0, nil, fmt.Errorf("实时语音未配置 base_url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/realtime/sessions", bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("实时语音上游请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("读取实时语音上游响应失败: %w", err)
	}
	return resp.StatusCode, raw, nil
}

// ProxyVideo 转发视频创建/查询请求，返回上游状态码、Content-Type 与响应体。
func ProxyVideo(ctx context.Context, cfg VideoConfig, method, path, contentType string, body []byte) (int, string, []byte, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return 0, "", nil, fmt.Errorf("视频服务未配置 base_url")
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return 0, "", nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, "", nil, fmt.Errorf("视频上游请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return resp.StatusCode, "", nil, fmt.Errorf("读取视频上游响应失败: %w", err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), raw, nil
}

// ProxyAudio 把请求体原样转发到音频服务的指定路径，并返回上游 Content-Type。
func ProxyAudio(ctx context.Context, cfg AudioConfig, path, contentType string, body []byte) (int, string, []byte, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return 0, "", nil, fmt.Errorf("音频服务未配置 base_url")
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
	if err != nil {
		return 0, "", nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, "", nil, fmt.Errorf("音频上游请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return resp.StatusCode, "", nil, fmt.Errorf("读取音频上游响应失败: %w", err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), raw, nil
}
