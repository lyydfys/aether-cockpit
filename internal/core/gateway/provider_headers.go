package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"aether-cockpit/internal/provider"
)

// 渠道级自定义请求头（渠道编辑器 "高级 → 请求头"）。
//
// 这几项之前只在前端 draft 里，providerDraft 没有对应字段，保存时被静默丢弃。
// 现在落进 settings.ProviderHeaders，并在账号的 http.Client 上套一层
// headerTransport：每个发往该渠道的上游请求都会带上这些头（不覆盖请求自身
// 已设置的同名头——渠道代码显式设置的优先级更高）。
//
// 安全：名字里带 authorization / token / key / secret / cookie 的值在
// /api/providers 回显时打码（只留末 4 位）；保存时若回传的仍是打码值，
// 保留旧值（与 API Key 的掩码约定一致）。

// headerTransport 给每个请求补固定的头，并按渠道/路由预设合并 extraBody。
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
	// extra 是要往 JSON 请求体里补的字段（渠道编辑器 "额外请求体"，extraBody）。
	extra map[string]any
	// passUA 为真时，用下游 Agent 的 User-Agent 覆盖上游 UA。
	passUA     bool
	providerID string
	// overlay 返回本次请求要合并的渠道/路由预设；nil 表示未启用。
	overlay func(context.Context, string) requestOverlay
}

// headerRoundTripper 便于测试与"识别已包裹"。
type headerRoundTripper = headerTransport

func (t *headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var ov requestOverlay
	if t.overlay != nil {
		ov = t.overlay(r.Context(), t.providerID)
	}
	if len(t.headers) == 0 && len(t.extra) == 0 && !t.passUA && len(ov.Body) == 0 && len(ov.Headers) == 0 && ov.SystemPrompt == "" {
		return t.base.RoundTrip(r)
	}
	// 克隆请求：不能改动调用方的 Header/Body（重试会复用同一个 Request）。
	r2 := r.Clone(r.Context())
	headers := make(map[string]string, len(t.headers)+len(ov.Headers))
	for k, v := range t.headers {
		headers[k] = v
	}
	// 预设比渠道静态头更具体；同名时预设覆盖静态头，但不覆盖渠道代码显式设置的头。
	for k, v := range ov.Headers {
		headers[k] = v
	}
	for k, v := range headers {
		if r2.Header.Get(k) == "" {
			r2.Header.Set(k, v)
		}
	}
	if t.passUA {
		if ua := downstreamUAFrom(r.Context()); ua != "" {
			r2.Header.Set("User-Agent", ua)
		}
	}
	extra := cloneAnyMap(t.extra)
	if len(ov.Body) > 0 {
		if extra == nil {
			extra = map[string]any{}
		}
		deepMergeExtra(extra, ov.Body)
	}
	mergeExtraBody(r2, extra)
	if ov.SystemPrompt != "" {
		applySystemPromptBody(r2, ov.SystemPrompt, ov.ConcatSystem)
	}
	return t.base.RoundTrip(r2)
}

// mergeExtraBody 把 extra 深度合并进 JSON 请求体（extra 覆盖同名标量，
// 两边都是对象时递归合并）。非 JSON 请求体原样放回，不报错——渠道的
// 表单/二进制请求不该被这项功能影响。
func mergeExtraBody(r *http.Request, extra map[string]any) {
	if len(extra) == 0 || r.Body == nil || r.Body == http.NoBody {
		return
	}
	if !strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "json") {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	_ = r.Body.Close()
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil || body == nil {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return
	}
	deepMergeExtra(body, extra)
	out, err := json.Marshal(body)
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(out))
	r.ContentLength = int64(len(out))
	r.Header.Set("Content-Length", strconv.Itoa(len(out)))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(out)), nil
	}
}

func deepMergeExtra(dst, src map[string]any) {
	for k, v := range src {
		if sub, ok := v.(map[string]any); ok {
			if cur, ok := dst[k].(map[string]any); ok {
				deepMergeExtra(cur, sub)
				continue
			}
		}
		dst[k] = v
	}
}

// applySystemPromptBody 在最终上游 JSON 里注入渠道/预设系统提示词。
//
// 用户已有 system prompt 时默认不覆盖（用户优先）；只有 concat=true 才在
// 用户提示词之后追加渠道提示词。覆盖 OpenAI Chat（messages）、Responses
// （instructions）、Anthropic（system）和 Gemini（systemInstruction）。
func applySystemPromptBody(r *http.Request, prompt string, concat bool) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" || r.Body == nil || r.Body == http.NoBody {
		return
	}
	if !strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "json") {
		return
	}
	path := strings.ToLower(r.URL.Path)
	if !strings.Contains(path, "chat/completions") && !strings.Contains(path, "/responses") &&
		!strings.Contains(path, "/messages") && !strings.Contains(path, "generatecontent") && !strings.Contains(path, "/completions") {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	_ = r.Body.Close()
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return
	}
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil || body == nil {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return
	}
	changed := false
	// 先认 Anthropic / Responses / Gemini 的顶层 system 字段，最后才是
	// OpenAI Chat 的 messages：Anthropic 请求同时带 system 和 messages。
	if _, ok := body["instructions"]; ok {
		changed = injectInstructions(body, prompt, concat) || changed
	} else if _, ok := body["system"]; ok {
		changed = injectAnthropicSystem(body, prompt, concat) || changed
	} else if _, ok := body["systemInstruction"]; ok {
		changed = injectGeminiSystem(body, "systemInstruction", prompt, concat) || changed
	} else if _, ok := body["system_instruction"]; ok {
		changed = injectGeminiSystem(body, "system_instruction", prompt, concat) || changed
	} else if _, ok := body["input"]; ok {
		// Responses API 没有 instructions 时，input 就是用户内容。
		body["instructions"] = prompt
		changed = true
	} else if _, ok := body["messages"]; ok {
		changed = injectChatSystem(body, prompt, concat) || changed
	}
	if !changed {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return
	}
	out, err := json.Marshal(body)
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(out))
	r.ContentLength = int64(len(out))
	r.Header.Set("Content-Length", strconv.Itoa(len(out)))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(out)), nil
	}
}

func injectChatSystem(body map[string]any, prompt string, concat bool) bool {
	msgs, ok := body["messages"].([]any)
	if !ok {
		return false
	}
	for _, raw := range msgs {
		m, _ := raw.(map[string]any)
		if m == nil {
			continue
		}
		role, _ := m["role"].(string)
		if role != "system" && role != "developer" {
			continue
		}
		if !concat {
			return false
		}
		switch c := m["content"].(type) {
		case string:
			m["content"] = c + "\n\n" + prompt
		case []any:
			m["content"] = append(c, map[string]any{"type": "text", "text": prompt})
		case nil:
			m["content"] = prompt
		default:
			return false
		}
		return true
	}
	body["messages"] = append([]any{map[string]any{"role": "system", "content": prompt}}, msgs...)
	return true
}

func injectInstructions(body map[string]any, prompt string, concat bool) bool {
	switch v := body["instructions"].(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			body["instructions"] = prompt
			return true
		}
		if concat {
			body["instructions"] = v + "\n\n" + prompt
			return true
		}
	case nil:
		body["instructions"] = prompt
		return true
	}
	return false
}

func injectAnthropicSystem(body map[string]any, prompt string, concat bool) bool {
	switch v := body["system"].(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			body["system"] = prompt
			return true
		}
		if concat {
			body["system"] = v + "\n\n" + prompt
			return true
		}
	case []any:
		if concat {
			body["system"] = append(v, map[string]any{"type": "text", "text": prompt})
			return true
		}
	case nil:
		body["system"] = prompt
		return true
	}
	return false
}

func injectGeminiSystem(body map[string]any, key, prompt string, concat bool) bool {
	switch v := body[key].(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			body[key] = map[string]any{"parts": []any{map[string]any{"text": prompt}}}
			return true
		}
		if concat {
			body[key] = v + "\n\n" + prompt
			return true
		}
	case map[string]any:
		parts, _ := v["parts"].([]any)
		if len(parts) == 0 {
			v["parts"] = []any{map[string]any{"text": prompt}}
			return true
		}
		if concat {
			v["parts"] = append(parts, map[string]any{"text": prompt})
			return true
		}
	case nil:
		body[key] = map[string]any{"parts": []any{map[string]any{"text": prompt}}}
		return true
	}
	return false
}

// unwrapHeaders 取出原始 RoundTripper（幂等套壳用）。
func unwrapHeaders(rt http.RoundTripper) http.RoundTripper {
	for {
		ht, ok := rt.(*headerRoundTripper)
		if !ok {
			return rt
		}
		rt = ht.base
	}
}

// applyProviderTransport 把渠道自定义头 / extraBody 套到账号 client 上；没有配置时还原。
func (g *Gateway) applyProviderTransport(env *provider.AccountEnv) {
	if env == nil || env.HTTPClient == nil {
		return
	}
	headers := g.providerHeaders(env.ProviderID)
	extra := g.providerExtraBody(env.ProviderID)
	passUA := g.providerPassUA(env.ProviderID)
	base := unwrapHeaders(env.HTTPClient.Transport)
	if len(headers) == 0 && len(extra) == 0 && !passUA && !g.presetsConfigured() {
		env.HTTPClient.Transport = base
		return
	}
	env.HTTPClient.Transport = &headerRoundTripper{
		base: base, headers: headers, extra: extra, passUA: passUA,
		providerID: env.ProviderID, overlay: g.presetsForRequest,
	}
}

// applyProviderTransportAll 把所有账号重套一遍（启动恢复后 / 保存请求头后）。
func (g *Gateway) applyProviderTransportAll() {
	for _, env := range g.pool.All() {
		g.applyProviderTransport(env)
	}
}

// providerHeaders 返回某渠道已保存的自定义头（副本）。
func (g *Gateway) providerHeaders(providerID string) map[string]string {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	src := g.settings.ProviderHeaders[providerID]
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// setProviderHeaders 保存某渠道的自定义头。masked 表示"值未改"的掩码值，
// 遇到时保留旧值；全部为空则删除条目。
func (g *Gateway) setProviderHeaders(providerID string, headers map[string]string) error {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.ProviderHeaders == nil {
		g.settings.ProviderHeaders = map[string]map[string]string{}
	}
	old := g.settings.ProviderHeaders[providerID]
	next := map[string]string{}
	for name, value := range headers {
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" {
			continue
		}
		if err := validateHeaderName(name); err != nil {
			return err
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("请求头 %s 的值不能包含换行", name)
		}
		if value == "" {
			continue
		}
		if isMaskedHeader(value) {
			if prev, ok := old[name]; ok {
				next[name] = prev
			}
			continue
		}
		next[name] = value
	}
	if len(next) == 0 {
		delete(g.settings.ProviderHeaders, providerID)
	} else {
		g.settings.ProviderHeaders[providerID] = next
	}
	return g.saveSettings()
}

// providerHeadersView 是 /api/providers 回显的形态：敏感值打码。
func (g *Gateway) providerHeadersView(providerID string) map[string]string {
	src := g.providerHeaders(providerID)
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		if sensitiveHeader(k) {
			out[k] = maskHeaderValue(v)
			continue
		}
		out[k] = v
	}
	return out
}

func sensitiveHeader(name string) bool {
	l := strings.ToLower(name)
	for _, mark := range []string{"authorization", "token", "key", "secret", "cookie", "password"} {
		if strings.Contains(l, mark) {
			return true
		}
	}
	return false
}

func maskHeaderValue(v string) string {
	if len(v) <= 4 {
		return "••••"
	}
	return "••••" + v[len(v)-4:]
}

// isMaskedHeader 判断提交上来的值是不是我们自己打的掩码。
func isMaskedHeader(v string) bool {
	return strings.HasPrefix(v, "••••")
}

// validateHeaderName 只允许 RFC 7230 token 字符，且拒绝几个会破坏请求的头。
func validateHeaderName(name string) error {
	switch strings.ToLower(name) {
	case "host", "content-length", "transfer-encoding", "connection":
		return fmt.Errorf("请求头 %s 由 HTTP 客户端管理，不能在这里设置", name)
	}
	for _, r := range name {
		if r <= 32 || r >= 127 {
			return fmt.Errorf("请求头名字含非法字符: %q", name)
		}
		switch r {
		case '(', ')', ',', '/', ':', ';', '<', '=', '>', '?', '@', '[', '\\', ']', '{', '}', '"':
			return fmt.Errorf("请求头名字含非法字符: %q", name)
		}
	}
	return nil
}

// 编译期断言：headerRoundTripper 实现 http.RoundTripper。
var _ http.RoundTripper = (*headerRoundTripper)(nil)

// --- 渠道级"额外请求体"（extraBody）---

// extraBodyReserved 与前端 EXTRA_BODY_RESERVED 一致：这些字段由网关自己
// 生成（模型名、消息、流开关、工具定义），不允许通过 extraBody 覆盖。
var extraBodyReserved = map[string]bool{
	"model": true, "stream": true, "messages": true, "input": true,
	"contents": true, "system": true, "systeminstruction": true,
	"system_instruction": true, "instructions": true, "tools": true,
}

// providerExtraBody 返回某渠道已保存的额外请求体字段（解析后的对象）。
func (g *Gateway) providerExtraBody(providerID string) map[string]any {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	raw := g.settings.ProviderExtraBody[providerID]
	if len(raw) == 0 {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// providerExtraBodyView 是 /api/providers 回显的形态：**对象**（前端
// extraBodyText 负责格式化成文本）。早先这里回的是 JSON 文本，前端又用
// JSON.stringify 包了一层，编辑器把它显示成带引号的字符串，再保存时报
// "请写一个 JSON 对象"——往返就坏了。空配置回 nil。
func (g *Gateway) providerExtraBodyView(providerID string) map[string]any {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	raw := g.settings.ProviderExtraBody[providerID]
	if len(raw) == 0 {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// setProviderExtraBody 保存额外请求体：必须是 JSON 对象、不超过 64KB、
// 不含网关自管字段；空对象表示清除。
func (g *Gateway) setProviderExtraBody(providerID string, raw json.RawMessage) error {
	trim := bytes.TrimSpace(raw)
	g.settingsMu.Lock()
	old := g.settings.ProviderExtraBody[providerID]
	g.settingsMu.Unlock()
	_ = old
	if len(trim) == 0 || string(trim) == "null" || string(trim) == "{}" {
		g.settingsMu.Lock()
		defer g.settingsMu.Unlock()
		if g.settings.ProviderExtraBody == nil {
			return nil
		}
		delete(g.settings.ProviderExtraBody, providerID)
		return g.saveSettings()
	}
	if len(trim) > 64<<10 {
		return fmt.Errorf("额外请求体过大（上限 64KB）")
	}
	var obj map[string]any
	if err := json.Unmarshal(trim, &obj); err != nil || obj == nil {
		return fmt.Errorf("额外请求体必须是 JSON 对象，例如 {\"prompt_cache_key\": \"\"}")
	}
	for k := range obj {
		name := strings.TrimSpace(strings.TrimPrefix(k, "!"))
		if name == "" {
			return fmt.Errorf("额外请求体里有空字段名")
		}
		if extraBodyReserved[strings.ToLower(name)] {
			return fmt.Errorf("字段 %s 由网关自己设置，不能通过额外请求体覆盖", name)
		}
	}
	normalized, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("额外请求体无法序列化: %w", err)
	}
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.ProviderExtraBody == nil {
		g.settings.ProviderExtraBody = map[string]json.RawMessage{}
	}
	g.settings.ProviderExtraBody[providerID] = normalized
	return g.saveSettings()
}
