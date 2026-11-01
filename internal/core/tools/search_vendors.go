package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// 与 aether 对齐的搜索供应商（形状取自其 internal/gateway/search_api.go）：
// brave / exa / firecrawl / deepseek / you（有 Key 走 Search API，无 Key 走免费搜索）。
// Endpoint 在这几家是“基地址”，路径由这里拼接；tavily/serper 仍允许直接给完整地址。

var searchHTML = regexp.MustCompile(`<[^>]*>`)

func plainText(s string) string {
	return strings.TrimSpace(searchHTML.ReplaceAllString(s, ""))
}

func baseOf(endpoint, def string) string {
	if strings.TrimSpace(endpoint) == "" {
		return def
	}
	return strings.TrimRight(strings.TrimSpace(endpoint), "/")
}

func doSearchJSON(ctx context.Context, method, at string, headers map[string]string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, at, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
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
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return statusError(resp.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("搜索上游响应解析失败: %w", err)
	}
	return nil
}

type vendorHit struct {
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Content     string   `json:"content"`
	Description string   `json:"description"`
	Text        string   `json:"text"`
	Markdown    string   `json:"markdown"`
	Summary     string   `json:"summary"`
	Snippet     string   `json:"snippet"`
	Snippets    []string `json:"snippets"`
}

func hitsToResults(hits []vendorHit, max int) []SearchResult {
	out := make([]SearchResult, 0, len(hits))
	for _, h := range hits {
		if len(out) >= max || h.URL == "" {
			continue
		}
		text := h.Content
		for _, cand := range []string{h.Summary, h.Text, h.Description, h.Markdown, h.Snippet, strings.Join(h.Snippets, " ")} {
			if text == "" {
				text = cand
			}
		}
		title := plainText(h.Title)
		if title == "" {
			title = h.URL
		}
		out = append(out, SearchResult{Title: title, URL: h.URL, Snippet: plainText(text)})
	}
	return out
}

func searchBrave(ctx context.Context, cfg SearchConfig, query string, max int) ([]SearchResult, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("brave 需要 API Key")
	}
	at := baseOf(cfg.Endpoint, "https://api.search.brave.com") + "/res/v1/web/search?" +
		url.Values{"q": {query}, "count": {strconv.Itoa(max)}}.Encode()
	var resp struct {
		Web struct {
			Results []vendorHit `json:"results"`
		} `json:"web"`
	}
	if err := doSearchJSON(ctx, http.MethodGet, at, map[string]string{"X-Subscription-Token": cfg.APIKey}, nil, &resp); err != nil {
		return nil, err
	}
	return hitsToResults(resp.Web.Results, max), nil
}

func searchExa(ctx context.Context, cfg SearchConfig, query string, max int) ([]SearchResult, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("exa 需要 API Key")
	}
	at := baseOf(cfg.Endpoint, "https://api.exa.ai") + "/search"
	body := map[string]any{
		"query": query, "numResults": max,
		"contents": map[string]any{"text": map[string]any{"maxCharacters": 2000}},
	}
	var resp struct {
		Results []vendorHit `json:"results"`
	}
	if err := doSearchJSON(ctx, http.MethodPost, at, map[string]string{"X-API-Key": cfg.APIKey}, body, &resp); err != nil {
		return nil, err
	}
	return hitsToResults(resp.Results, max), nil
}

func searchFirecrawl(ctx context.Context, cfg SearchConfig, query string, max int) ([]SearchResult, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("firecrawl 需要 API Key")
	}
	at := baseOf(cfg.Endpoint, "https://api.firecrawl.dev") + "/v2/search"
	var resp struct {
		Data json.RawMessage `json:"data"`
	}
	body := map[string]any{"query": query, "limit": max}
	if err := doSearchJSON(ctx, http.MethodPost, at, map[string]string{"Authorization": "Bearer " + cfg.APIKey}, body, &resp); err != nil {
		return nil, err
	}
	var v2 struct {
		Web []vendorHit `json:"web"`
	}
	if json.Unmarshal(resp.Data, &v2) == nil && v2.Web != nil {
		return hitsToResults(v2.Web, max), nil
	}
	var flat []vendorHit
	_ = json.Unmarshal(resp.Data, &flat)
	return hitsToResults(flat, max), nil
}

func searchDeepseek(ctx context.Context, cfg SearchConfig, query string, max int) ([]SearchResult, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("deepseek 需要 API Key")
	}
	at := baseOf(cfg.Endpoint, "https://api.deepseek.com") + "/v1/tools/search"
	q := query
	if r := []rune(q); len(r) > 128 {
		q = string(r[:128])
	}
	var resp struct {
		Results []vendorHit `json:"results"`
	}
	body := map[string]any{"query": q, "count": max}
	if err := doSearchJSON(ctx, http.MethodPost, at, map[string]string{"Authorization": "Bearer " + cfg.APIKey}, body, &resp); err != nil {
		return nil, err
	}
	return hitsToResults(resp.Results, max), nil
}

func searchYou(ctx context.Context, cfg SearchConfig, query string, max int) ([]SearchResult, error) {
	var at string
	var method = http.MethodGet
	var body any
	headers := map[string]string{}
	if cfg.APIKey != "" {
		at = baseOf(cfg.Endpoint, "https://ydc-index.io") + "/v1/search"
		method, body = http.MethodPost, map[string]any{"query": query, "count": max}
		headers["X-API-Key"] = cfg.APIKey
	} else {
		base := baseOf(cfg.Endpoint, "https://api.you.com")
		at = base + "/v1/agents/search?" + url.Values{"query": {query}, "count": {strconv.Itoa(max)}}.Encode()
	}
	var resp struct {
		Results *struct {
			Web  []vendorHit `json:"web"`
			News []vendorHit `json:"news"`
		} `json:"results"`
	}
	if err := doSearchJSON(ctx, method, at, headers, body, &resp); err != nil {
		return nil, err
	}
	if resp.Results == nil {
		return nil, fmt.Errorf("you 响应里没有 results")
	}
	return hitsToResults(append(resp.Results.Web, resp.Results.News...), max), nil
}
