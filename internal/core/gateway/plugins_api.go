package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"aether-cockpit/internal/core/apppaths"
	"aether-cockpit/internal/core/mw"
)

// 本文件提供「插件 / 规则」页所需的数据端点。
//
// 上游的插件是 npm 包 + Bun 运行时。本项目换成声明式规则引擎：
// 同样是「改写请求」这件事，但零运行时依赖、可审计、单二进制。
//
// 页面上表现为一组可开关的「内置插件」：参数覆盖、模型映射、
// 关键词拦截、系统提示注入。

// rewriteEngine 返回规则引擎（懒初始化）。
func (g *Gateway) rewriteEngine() *mw.Engine {
	g.rewriteOnce.Do(func() {
		path := g.cfg.MiddlewarePath
		if path == "" {
			path = apppaths.Data("middleware.json")
		}
		g.rewriteE = mw.New(path)
	})
	return g.rewriteE
}

// builtinPlugins 是四类规则的中文说明，供页面展示。
var builtinPlugins = []map[string]any{
	{
		"id": "param_override", "name": "参数覆盖",
		"desc": "统一改写 temperature / top_p / max_tokens；请求里已显式指定的值不会被覆盖。",
		"on":   true,
	},
	{
		"id": "model_map", "name": "模型映射",
		"desc": "把请求里的模型名按通配规则重写后再发往上游，例如 claude-* → claude-sonnet-4-6。",
		"on":   true,
	},
	{
		"id": "word_guard", "name": "关键词拦截",
		"desc": "命中关键词或正则时直接拒绝请求，并返回可读的原因。",
		"on":   true,
	},
	{
		"id": "system_prompt", "name": "系统提示注入",
		"desc": "给每个请求前置或追加一段系统提示，可限定渠道与模型。",
		"on":   true,
	},
}

// handlePlugins 返回规则列表与内置插件说明。
//
//	GET /api/plugins
func (g *Gateway) handlePlugins(w http.ResponseWriter, r *http.Request) {
	rules := g.rewriteEngine().Rules()
	out := make([]map[string]any, 0, len(rules))
	for _, ru := range rules {
		out = append(out, ruleToMap(ru))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"plugins": out,
		"builtin": builtinPlugins,
		"total":   len(out),
	})
}

// handleBuiltinPlugins 只返回内置插件说明（页面侧栏用）。
func (g *Gateway) handleBuiltinPlugins(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"plugins": builtinPlugins})
}

// handlePluginSave 新增或更新一条规则。
//
//	POST /api/plugins  {id, kind, name, enabled, ...}
func (g *Gateway) handlePluginSave(w http.ResponseWriter, r *http.Request) {
	var rule mw.Rule
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err := json.Unmarshal(raw, &rule); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	if rule.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 id"})
		return
	}
	if rule.Kind == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 kind"})
		return
	}
	if rule.Name == "" {
		rule.Name = string(rule.Kind)
	}

	if err := g.rewriteEngine().Upsert(rule); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rule": ruleToMap(rule)})
}

// handlePluginDelete 删除一条规则。
func (g *Gateway) handlePluginDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/plugins/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 id"})
		return
	}
	if err := g.rewriteEngine().Delete(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

// ruleToMap 把规则转成前端友好的形状（只给非空字段）。
func ruleToMap(r mw.Rule) map[string]any {
	m := map[string]any{
		"id": r.ID, "kind": string(r.Kind), "name": r.Name, "enabled": r.Enabled,
	}
	if r.Note != "" {
		m["note"] = r.Note
	}
	if len(r.Providers) > 0 {
		m["providers"] = r.Providers
	}
	if len(r.Models) > 0 {
		m["models"] = r.Models
	}
	if r.Temperature != nil {
		m["temperature"] = *r.Temperature
	}
	if r.TopP != nil {
		m["top_p"] = *r.TopP
	}
	if r.MaxTokens != nil {
		m["max_tokens"] = *r.MaxTokens
	}
	if r.From != "" {
		m["from"] = r.From
	}
	if r.To != "" {
		m["to"] = r.To
	}
	if len(r.Words) > 0 {
		m["words"] = r.Words
	}
	if len(r.Patterns) > 0 {
		m["patterns"] = r.Patterns
	}
	if r.Message != "" {
		m["message"] = r.Message
	}
	if r.Prompt != "" {
		m["prompt"] = r.Prompt
	}
	if r.Position != "" {
		m["position"] = r.Position
	}
	return m
}

// handlePluginStub 覆盖插件页会请求但暂未实现的端点。
//
// 与 sessions 的处理一致：返回空对象而不是 404，
// 否则前端的 api() 会把 "404 page not found" 当成错误文案显示出来。
func (g *Gateway) handlePluginStub(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"pages":    []any{},
		"panels":   []any{},
		"plugins":  []any{},
		"listings": []any{},
		"options":  []any{},
		"items":    []any{},
		"ok":       true,
	})
}
