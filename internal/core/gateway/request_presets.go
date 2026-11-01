package gateway

// 请求预设：一组可复用的 Body / Headers 覆盖，挂在渠道或路由组上。
//
// 只做静态合并，不做条件/操作引擎：
//   - 渠道静态 extraBody/headers → 渠道预设 → 路由预设；
//   - 路由预设需要当前请求命中虚拟路由组；
//   - 预设停用或没有挂载 = 不生效。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/provider"
)

// requestPreset 是一个请求预设的落盘形态。
type requestPreset struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Enabled      bool              `json:"enabled"`
	Body         map[string]any    `json:"body,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	SystemPrompt string            `json:"system_prompt,omitempty"`
	ConcatSystem bool              `json:"concat_system,omitempty"`
	UpdatedAt    int64             `json:"updated_at,omitempty"`
}

type presetRouteCtxKey struct{}

func withPresetRoute(ctx context.Context, route string) context.Context {
	if route == "" {
		return ctx
	}
	return context.WithValue(ctx, presetRouteCtxKey{}, route)
}

func presetRouteFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(presetRouteCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// presetRouteFor 返回请求命中的路由组名；普通模型名返回空。
func (g *Gateway) presetRouteFor(model string) string {
	if g == nil || g.models == nil {
		return ""
	}
	if g.models.HasGroup(model) {
		return model
	}
	return ""
}

// requestOverlay 是一次请求要叠加的预设结果。
type requestOverlay struct {
	Body         map[string]any
	Headers      map[string]string
	SystemPrompt string
	ConcatSystem bool
	Names        []string
}

// presetsConfigured 报告是否至少有一个需要叠加的渠道/路由配置。
func (g *Gateway) presetsConfigured() bool {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if len(g.settings.ProviderSystemPrompt) > 0 {
		return true
	}
	if len(g.settings.RequestPresets) == 0 {
		return false
	}
	return len(g.settings.ProviderPresets) > 0 || len(g.settings.RoutePresets) > 0
}

// presetsForRequest 解析本次请求要应用的预设。
// 顺序：渠道直接配置 → 渠道预设 → 路由预设；后者覆盖前者的 Body/Header/系统提示词。
func (g *Gateway) presetsForRequest(ctx context.Context, providerID string) requestOverlay {
	g.settingsMu.Lock()
	providerIDs := append([]string(nil), g.settings.ProviderPresets[providerID]...)
	routeIDs := append([]string(nil), g.settings.RoutePresets[presetRouteFrom(ctx)]...)
	directPrompt := strings.TrimSpace(g.settings.ProviderSystemPrompt[providerID])
	directConcat := g.settings.ProviderSystemPromptConcat[providerID]
	if len(providerIDs) == 0 && len(routeIDs) == 0 && directPrompt == "" {
		g.settingsMu.Unlock()
		return requestOverlay{}
	}
	byID := make(map[string]requestPreset, len(g.settings.RequestPresets))
	for _, p := range g.settings.RequestPresets {
		byID[p.ID] = p
	}
	g.settingsMu.Unlock()

	ov := requestOverlay{SystemPrompt: directPrompt, ConcatSystem: directConcat}
	body := map[string]any{}
	headers := map[string]string{}
	apply := func(ids []string) {
		for _, id := range ids {
			p, ok := byID[id]
			if !ok || !p.Enabled {
				continue
			}
			if len(p.Body) > 0 {
				deepMergeExtra(body, cloneAnyMap(p.Body))
			}
			for k, v := range p.Headers {
				headers[k] = v
			}
			if prompt := strings.TrimSpace(p.SystemPrompt); prompt != "" {
				ov.SystemPrompt = prompt
				ov.ConcatSystem = p.ConcatSystem
			}
			ov.Names = append(ov.Names, p.Name)
		}
	}
	apply(providerIDs)
	apply(routeIDs)

	if len(body) > 0 {
		ov.Body = body
	}
	if len(headers) > 0 {
		ov.Headers = headers
	}
	return ov
}

func (g *Gateway) providerSystemPrompt(pid string) (string, bool) {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	return strings.TrimSpace(g.settings.ProviderSystemPrompt[pid]), g.settings.ProviderSystemPromptConcat[pid]
}

func (g *Gateway) providerSystemPromptView(pid string) string {
	p, _ := g.providerSystemPrompt(pid)
	return p
}

func (g *Gateway) providerSystemPromptConcatView(pid string) bool {
	_, c := g.providerSystemPrompt(pid)
	return c
}

// setProviderSystemPrompt 保存渠道默认系统提示词；空提示词同时清掉连接开关。
func (g *Gateway) setProviderSystemPrompt(pid string, prompt *string, concat *bool) error {
	if prompt == nil && concat == nil {
		return nil
	}
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if prompt != nil {
		v := strings.TrimSpace(*prompt)
		if len(v) > 32<<10 {
			return fmt.Errorf("系统提示词过长（上限 32KB）")
		}
		if v == "" {
			delete(g.settings.ProviderSystemPrompt, pid)
		} else {
			if g.settings.ProviderSystemPrompt == nil {
				g.settings.ProviderSystemPrompt = map[string]string{}
			}
			g.settings.ProviderSystemPrompt[pid] = v
		}
	}
	if concat != nil {
		if !*concat {
			delete(g.settings.ProviderSystemPromptConcat, pid)
		} else if strings.TrimSpace(g.settings.ProviderSystemPrompt[pid]) != "" {
			if g.settings.ProviderSystemPromptConcat == nil {
				g.settings.ProviderSystemPromptConcat = map[string]bool{}
			}
			g.settings.ProviderSystemPromptConcat[pid] = true
		}
	}
	return g.saveSettings()
}

func cloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		if sub, ok := v.(map[string]any); ok {
			out[k] = cloneAnyMap(sub)
		} else {
			out[k] = v
		}
	}
	return out
}

func maskedRequestPresets(list []requestPreset) []requestPreset {
	if len(list) == 0 {
		return nil
	}
	out := make([]requestPreset, len(list))
	for i, p := range list {
		p.Headers = maskedPresetHeaders(p.Headers)
		out[i] = p
	}
	return out
}

// sanitizeImportedPresets 导入备份时丢弃打码头、非法头和非法 Body，
// 避免把占位符当成真实密钥写回。
func sanitizeImportedPresets(list []requestPreset) []requestPreset {
	out := make([]requestPreset, 0, len(list))
	seen := map[string]bool{}
	for _, p := range list {
		p.ID = strings.TrimSpace(p.ID)
		if p.ID == "" || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			p.Name = p.ID
		}
		p.SystemPrompt = strings.TrimSpace(p.SystemPrompt)
		if len(p.SystemPrompt) > 32<<10 {
			p.SystemPrompt = ""
		}
		if p.SystemPrompt == "" {
			p.ConcatSystem = false
		}
		if p.Body != nil {
			if err := validatePresetBody(p.Body); err != nil {
				p.Body = nil
			} else {
				p.Body = cloneAnyMap(p.Body)
			}
		}
		headers := make(map[string]string, len(p.Headers))
		for k, v := range p.Headers {
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if k == "" || v == "" || isMaskedHeader(v) || validateHeaderName(k) != nil || strings.ContainsAny(v, "\r\n") {
				continue
			}
			headers[k] = v
		}
		if len(headers) == 0 {
			p.Headers = nil
		} else {
			p.Headers = headers
		}
		out = append(out, p)
	}
	return out
}

func filterPresetAttachments(att map[string][]string, known map[string]bool) map[string][]string {
	if len(att) == 0 {
		return nil
	}
	out := make(map[string][]string, len(att))
	for key, ids := range att {
		clean := make([]string, 0, len(ids))
		seen := map[string]bool{}
		for _, id := range ids {
			if known[id] && !seen[id] {
				seen[id] = true
				clean = append(clean, id)
			}
		}
		if len(clean) > 0 {
			out[key] = clean
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func maskedPresetHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if sensitiveHeader(k) {
			out[k] = maskHeaderValue(v)
		} else {
			out[k] = v
		}
	}
	return out
}

// maskRequestPresetSecrets 让 GET /api/settings 也不回显预设里的敏感 Header。
func maskRequestPresetSecrets(s runtimeSettings) runtimeSettings {
	if len(s.RequestPresets) == 0 {
		return s
	}
	cp := make([]requestPreset, len(s.RequestPresets))
	for i, p := range s.RequestPresets {
		p.Headers = maskedPresetHeaders(p.Headers)
		cp[i] = p
	}
	s.RequestPresets = cp
	return s
}

func (g *Gateway) presetsView() map[string]any {
	g.settingsMu.Lock()
	presets := make([]requestPreset, len(g.settings.RequestPresets))
	for i, p := range g.settings.RequestPresets {
		p.Headers = maskedPresetHeaders(p.Headers)
		presets[i] = p
	}
	providerPresets := make(map[string][]string, len(g.settings.ProviderPresets))
	for k, v := range g.settings.ProviderPresets {
		providerPresets[k] = append([]string(nil), v...)
	}
	routePresets := make(map[string][]string, len(g.settings.RoutePresets))
	for k, v := range g.settings.RoutePresets {
		routePresets[k] = append([]string(nil), v...)
	}
	g.settingsMu.Unlock()
	return map[string]any{
		"presets":          presets,
		"provider_presets": providerPresets,
		"route_presets":    routePresets,
	}
}

// handlePresets GET /api/presets
func (g *Gateway) handlePresets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.presetsView())
}

type presetSaveInput struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Description  string             `json:"description"`
	Enabled      *bool              `json:"enabled"`
	Body         *map[string]any    `json:"body"`
	Headers      *map[string]string `json:"headers"`
	SystemPrompt *string            `json:"system_prompt"`
	ConcatSystem *bool              `json:"concat_system"`
}

// handlePresetSave POST /api/presets/save
func (g *Gateway) handlePresetSave(w http.ResponseWriter, r *http.Request) {
	var in presetSaveInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "预设名称不能为空"})
		return
	}

	g.settingsMu.Lock()
	idx := -1
	for i := range g.settings.RequestPresets {
		if g.settings.RequestPresets[i].ID == strings.TrimSpace(in.ID) {
			idx = i
			break
		}
	}
	old := requestPreset{}
	if idx >= 0 {
		old = g.settings.RequestPresets[idx]
	}

	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = g.nextPresetIDLocked(name)
	} else if slugify(id) != id {
		g.settingsMu.Unlock()
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "预设 ID 只能包含小写字母、数字和连字符"})
		return
	}

	body := old.Body
	if in.Body != nil {
		body = cloneAnyMap(*in.Body)
		if err := validatePresetBody(body); err != nil {
			g.settingsMu.Unlock()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}

	headers := old.Headers
	if in.Headers != nil {
		next, err := g.cleanPresetHeadersLocked(*in.Headers, old.Headers)
		if err != nil {
			g.settingsMu.Unlock()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		headers = next
	}

	enabled := true
	if idx >= 0 {
		enabled = old.Enabled
	}
	if in.Enabled != nil {
		enabled = *in.Enabled
	}

	systemPrompt := old.SystemPrompt
	if in.SystemPrompt != nil {
		systemPrompt = strings.TrimSpace(*in.SystemPrompt)
		if len(systemPrompt) > 32<<10 {
			g.settingsMu.Unlock()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "系统提示词过长（上限 32KB）"})
			return
		}
	}
	concatSystem := old.ConcatSystem
	if in.ConcatSystem != nil {
		concatSystem = *in.ConcatSystem
	}
	if systemPrompt == "" {
		concatSystem = false
	}

	preset := requestPreset{
		ID: id, Name: name, Description: strings.TrimSpace(in.Description),
		Enabled: enabled, Body: body, Headers: headers,
		SystemPrompt: systemPrompt, ConcatSystem: concatSystem,
		UpdatedAt: time.Now().Unix(),
	}
	if idx >= 0 {
		g.settings.RequestPresets[idx] = preset
	} else {
		g.settings.RequestPresets = append(g.settings.RequestPresets, preset)
	}
	err := g.saveSettings()
	g.settingsMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存预设失败: " + err.Error()})
		return
	}
	g.applyProviderTransportAll()
	writeJSON(w, http.StatusOK, g.presetsView())
}

// nextPresetIDLocked 生成不冲突的预设 ID；调用方必须持有 settingsMu。
func (g *Gateway) nextPresetIDLocked(name string) string {
	used := make(map[string]bool, len(g.settings.RequestPresets))
	for _, p := range g.settings.RequestPresets {
		used[p.ID] = true
	}
	base := slugify(name)
	if base == "" {
		base = "preset"
	}
	id := base
	for n := 2; used[id]; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	return id
}

func validatePresetBody(body map[string]any) error {
	if len(body) == 0 {
		return nil
	}
	for k := range body {
		name := strings.TrimSpace(strings.TrimPrefix(k, "!"))
		if name == "" {
			return fmt.Errorf("Body 里有空字段名")
		}
		if extraBodyReserved[strings.ToLower(name)] {
			return fmt.Errorf("字段 %s 由网关自己设置，不能通过预设覆盖", name)
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("Body 无法序列化: %w", err)
	}
	if len(raw) > 64<<10 {
		return fmt.Errorf("Body 过大（上限 64KB）")
	}
	return nil
}

// cleanPresetHeadersLocked 校验并清洗预设头；掩码回传时保留旧值。
// 调用方必须持有 settingsMu。
func (g *Gateway) cleanPresetHeadersLocked(in, old map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(in))
	for name, value := range in {
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" {
			continue
		}
		if err := validateHeaderName(name); err != nil {
			return nil, err
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("请求头 %s 的值不能包含换行", name)
		}
		if value == "" {
			continue
		}
		if isMaskedHeader(value) {
			if prev, ok := old[name]; ok {
				out[name] = prev
			}
			continue
		}
		out[name] = value
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

type presetDeleteInput struct {
	ID string `json:"id"`
}

// handlePresetDelete POST /api/presets/delete
func (g *Gateway) handlePresetDelete(w http.ResponseWriter, r *http.Request) {
	var in presetDeleteInput
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in)
	id := strings.TrimSpace(in.ID)
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少预设 ID"})
		return
	}

	g.settingsMu.Lock()
	presets := g.settings.RequestPresets[:0]
	for _, p := range g.settings.RequestPresets {
		if p.ID != id {
			presets = append(presets, p)
		}
	}
	g.settings.RequestPresets = presets
	for key, ids := range g.settings.ProviderPresets {
		g.settings.ProviderPresets[key] = removeString(ids, id)
		if len(g.settings.ProviderPresets[key]) == 0 {
			delete(g.settings.ProviderPresets, key)
		}
	}
	for key, ids := range g.settings.RoutePresets {
		g.settings.RoutePresets[key] = removeString(ids, id)
		if len(g.settings.RoutePresets[key]) == 0 {
			delete(g.settings.RoutePresets, key)
		}
	}
	err := g.saveSettings()
	g.settingsMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "删除预设失败: " + err.Error()})
		return
	}
	g.applyProviderTransportAll()
	writeJSON(w, http.StatusOK, g.presetsView())
}

type presetAttachInput struct {
	Scope string   `json:"scope"` // provider / route
	Key   string   `json:"key"`
	IDs   []string `json:"ids"`
}

// handlePresetAttach POST /api/presets/attach
func (g *Gateway) handlePresetAttach(w http.ResponseWriter, r *http.Request) {
	var in presetAttachInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	scope := strings.TrimSpace(in.Scope)
	key := strings.TrimSpace(in.Key)
	if key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少挂载目标"})
		return
	}
	switch scope {
	case "provider":
		if _, ok := provider.Get(key); !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "未知渠道: " + key})
			return
		}
	case "route":
		if !g.models.HasGroup(key) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "未知路由组: " + key})
			return
		}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "scope 必须是 provider 或 route"})
		return
	}

	g.settingsMu.Lock()
	known := make(map[string]bool, len(g.settings.RequestPresets))
	for _, p := range g.settings.RequestPresets {
		known[p.ID] = true
	}
	clean := make([]string, 0, len(in.IDs))
	seen := map[string]bool{}
	for _, id := range in.IDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] || !known[id] {
			continue
		}
		seen[id] = true
		clean = append(clean, id)
	}
	if scope == "provider" {
		if len(clean) == 0 {
			delete(g.settings.ProviderPresets, key)
		} else {
			if g.settings.ProviderPresets == nil {
				g.settings.ProviderPresets = map[string][]string{}
			}
			g.settings.ProviderPresets[key] = clean
		}
	} else {
		if len(clean) == 0 {
			delete(g.settings.RoutePresets, key)
		} else {
			if g.settings.RoutePresets == nil {
				g.settings.RoutePresets = map[string][]string{}
			}
			g.settings.RoutePresets[key] = clean
		}
	}
	err := g.saveSettings()
	g.settingsMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存预设挂载失败: " + err.Error()})
		return
	}
	g.applyProviderTransportAll()
	writeJSON(w, http.StatusOK, g.presetsView())
}
