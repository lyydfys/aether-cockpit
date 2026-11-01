package gateway

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/redact"
)

// 本文件提供脱敏页（Insights › Masking）与设置页所需的端点。
//
// 契约对齐 magpie 的 plugins/redact/page.js：
//   - GET  /api/masking/summary?days=7|30   卡片（天/类型/来源/值排行）
//   - GET  /api/masking/records?limit&before&kind&by&blocked&agent&provider
//   - GET  /api/masking/record?key=         单条请求
//   - POST /api/masking/values {ps}         占位符 → 原值（仅内存）
//   - GET  /api/masking/sent?key=           实际发出去的文本（仅内存）
//   - GET  /api/masking/value?p=            单个占位符的原值
//   - POST /api/masking/preview {text}      试一试
//   - POST /api/masking/clear               清空记录
//
// 原文绝不落盘：记录里只有类型、长度、首字符与占位符。

// maskValueView 是记录里的一个值（脱敏页 values[]）。
type maskValueView struct {
	P string `json:"p"`
	K string `json:"k"`
	A string `json:"a"`
	B string `json:"b"`
	H string `json:"h"`
	L int    `json:"l,omitempty"`
	N int    `json:"n,omitempty"`
}

// maskAgg 把同一请求键的多段记录合并成一条。
type maskAgg struct {
	key      string
	at       time.Time
	provider string
	model    string
	agent    string
	blocked  bool
	hits     []redact.Hit
}

func (g *Gateway) maskAggs() []*maskAgg {
	recs := g.redactEngine().Records()
	byKey := make(map[string]*maskAgg, len(recs))
	order := make([]*maskAgg, 0, len(recs))
	for _, r := range recs {
		a := byKey[r.Key]
		if a == nil {
			a = &maskAgg{key: r.Key}
			byKey[r.Key] = a
			order = append(order, a)
		}
		if r.At.After(a.at) {
			a.at = r.At
		}
		if a.provider == "" {
			a.provider = r.Provider
		}
		if a.model == "" {
			a.model = r.Model
		}
		if a.agent == "" {
			a.agent = r.Agent
		}
		a.blocked = a.blocked || r.Blocked
		a.hits = append(a.hits, r.Hits...)
	}
	sort.SliceStable(order, func(i, j int) bool {
		if !order[i].at.Equal(order[j].at) {
			return order[i].at.After(order[j].at)
		}
		return order[i].key > order[j].key
	})
	return order
}

func (a *maskAgg) places() int {
	n := 0
	for _, h := range a.hits {
		if h.Action == redact.ActionMask {
			n += max(1, h.Count)
		}
	}
	return n
}

func (a *maskAgg) valueViews() []maskValueView {
	merged := make(map[string]*maskValueView, len(a.hits))
	order := make([]string, 0, len(a.hits))
	for _, h := range a.hits {
		k := h.Placeholder
		if k == "" {
			k = "\x00" + h.Kind + "|" + string(h.Action)
		}
		v := merged[k]
		if v == nil {
			v = &maskValueView{P: h.Placeholder, K: h.Kind, A: string(h.Action), B: "rule", H: h.Head, L: h.Len}
			merged[k] = v
			order = append(order, k)
		}
		v.N += max(1, h.Count)
	}
	sort.SliceStable(order, func(i, j int) bool {
		if merged[order[i]].N != merged[order[j]].N {
			return merged[order[i]].N > merged[order[j]].N
		}
		return merged[order[i]].K < merged[order[j]].K
	})
	out := make([]maskValueView, 0, len(order))
	for _, k := range order {
		out = append(out, *merged[k])
	}
	return out
}

func (a *maskAgg) recordView() map[string]any {
	return map[string]any{
		"key":      a.key,
		"t":        a.at.UnixMilli(),
		"agent":    a.agent,
		"provider": a.provider,
		"model":    a.model,
		"what":     "chat",
		"n":        a.places(),
		"blocked":  a.blocked,
		"values":   a.valueViews(),
	}
}

// handleMaskingSummary GET /api/masking/summary?days=7
func (g *Gateway) handleMaskingSummary(w http.ResponseWriter, r *http.Request) {
	days := 7
	if v := r.URL.Query().Get("days"); v == "30" {
		days = 30
	}
	aggs := g.maskAggs()

	type dayRow struct {
		Day      string `json:"day"`
		N        int    `json:"n"`
		Requests int    `json:"requests"`
	}
	start := time.Now().AddDate(0, 0, -(days - 1))
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
	dayIdx := make(map[string]int, days)
	dayRows := make([]dayRow, days)
	for i := 0; i < days; i++ {
		key := start.AddDate(0, 0, i).Format("2006-01-02")
		dayIdx[key] = i
		dayRows[i].Day = key
	}

	kinds := map[string]int{}
	providers := map[string]int{}
	agents := map[string]int{}
	type valSum struct {
		P        string `json:"p"`
		Kind     string `json:"kind"`
		By       string `json:"by"`
		A        string `json:"a"`
		Head     string `json:"head"`
		Len      int    `json:"len"`
		N        int    `json:"n"`
		Requests int    `json:"requests"`
		Last     int64  `json:"last"`
	}
	values := map[string]*valSum{}
	requests, total, blocked := 0, 0, 0

	for _, a := range aggs {
		if a.at.Before(start) {
			continue
		}
		requests++
		if a.blocked {
			blocked++
		}
		places := a.places()
		total += places
		if i, ok := dayIdx[a.at.Format("2006-01-02")]; ok {
			dayRows[i].N += places
			dayRows[i].Requests++
		}
		if a.provider != "" {
			providers[a.provider]++
		}
		if a.agent != "" {
			agents[a.agent]++
		}
		for _, h := range a.hits {
			kinds[h.Kind] += max(1, h.Count)
			if h.Placeholder == "" {
				continue
			}
			v := values[h.Placeholder]
			if v == nil {
				v = &valSum{P: h.Placeholder, Kind: h.Kind, By: "rule", A: string(h.Action), Head: h.Head, Len: h.Len, Last: a.at.UnixMilli()}
				values[h.Placeholder] = v
			}
			v.N += max(1, h.Count)
			v.Requests++
			if a.at.UnixMilli() > v.Last {
				v.Last = a.at.UnixMilli()
			}
		}
	}

	type pair struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}
	rank := func(m map[string]int) []pair {
		out := make([]pair, 0, len(m))
		for k, n := range m {
			out = append(out, pair{Name: k, N: n})
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].N != out[j].N {
				return out[i].N > out[j].N
			}
			return out[i].Name < out[j].Name
		})
		if len(out) > 8 {
			out = out[:8]
		}
		return out
	}
	valList := make([]*valSum, 0, len(values))
	for _, v := range values {
		valList = append(valList, v)
	}
	sort.SliceStable(valList, func(i, j int) bool {
		if valList[i].N != valList[j].N {
			return valList[i].N > valList[j].N
		}
		return valList[i].Last > valList[j].Last
	})
	if len(valList) > 60 {
		valList = valList[:60]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"n":         total,
		"distinct":  len(values),
		"requests":  requests,
		"blocked":   blocked,
		"days":      dayRows,
		"kinds":     rank(kinds),
		"providers": rank(providers),
		"agents":    rank(agents),
		"values":    valList,
		"by":        map[string]int{"rule": total, "model": 0},
		"enabled":   g.redactEngine().Enabled(),
	})
}

// handleMaskingRecords GET /api/masking/records
func (g *Gateway) handleMaskingRecords(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 40
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 && n <= 200 {
		limit = n
	}
	kinds := map[string]bool{}
	for _, k := range strings.Split(q.Get("kind"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			kinds[k] = true
		}
	}
	by := strings.TrimSpace(q.Get("by"))
	blockedOnly := q.Get("blocked") == "1"
	agent := strings.TrimSpace(q.Get("agent"))
	provider := strings.TrimSpace(q.Get("provider"))
	before := strings.TrimSpace(q.Get("before"))

	all := g.maskAggs()
	start := 0
	if before != "" {
		for i, a := range all {
			if a.key == before {
				start = i + 1
				break
			}
		}
	}

	out := make([]map[string]any, 0, limit+1)
	more := false
	for _, a := range all[start:] {
		if blockedOnly && !a.blocked {
			continue
		}
		if agent != "" && a.agent != agent {
			continue
		}
		if provider != "" && a.provider != provider {
			continue
		}
		if by == "model" {
			continue // 目前只有规则命中，没有本地模型
		}
		if len(kinds) > 0 {
			hit := false
			for _, h := range a.hits {
				if kinds[h.Kind] {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		if len(out) == limit {
			more = true
			break
		}
		view := a.recordView()
		if back := g.redactEngine().Backs(a.key); len(back) > 0 {
			view["back"] = back
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": out, "more": more})
}

// handleMaskingRecord GET /api/masking/record?key=
func (g *Gateway) handleMaskingRecord(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 key"})
		return
	}
	for _, a := range g.maskAggs() {
		if a.key == key {
			view := a.recordView()
			if back := g.redactEngine().Backs(a.key); len(back) > 0 {
				view["back"] = back
			}
			writeJSON(w, http.StatusOK, view)
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "记录已不存在（只保留 30 天或最近 500 条）"})
}

// handleMaskingValues 占位符表。
//
//	GET  → 最近记录里的占位符与类型（不含原值）
//	POST {ps:[...]} → {values:{占位符:原值}}（仅内存）
func (g *Gateway) handleMaskingValues(w http.ResponseWriter, r *http.Request) {
	eng := g.redactEngine()
	if r.Method == http.MethodPost {
		var body struct {
			Ps []string `json:"ps"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		vals := make(map[string]string, len(body.Ps))
		for _, p := range body.Ps {
			if v, ok := eng.ValueOf(p); ok {
				vals[p] = v
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"values": vals})
		return
	}

	recs := eng.Records()
	values := make([]map[string]any, 0, 16)
	seen := make(map[string]bool, 16)
	for _, rec := range recs {
		for _, h := range rec.Hits {
			if h.Placeholder == "" || seen[h.Placeholder] {
				continue
			}
			seen[h.Placeholder] = true
			values = append(values, map[string]any{
				"placeholder": h.Placeholder,
				"kind":        h.Kind,
				"rule":        h.Rule,
				"head":        h.Head,
				"len":         h.Len,
				"provider":    rec.Provider,
				"at":          rec.At.Unix(),
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"values": values, "total": len(values)})
}

// handleMaskingValue GET /api/masking/value?p=
func (g *Gateway) handleMaskingValue(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("p")
	if v, ok := g.redactEngine().ValueOf(p); ok {
		writeJSON(w, http.StatusOK, map[string]any{"value": v})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// handleMaskingSent GET /api/masking/sent?key=
// 返回该请求实际发出去的文本（已脱敏）；进程重启后不再保留。
func (g *Gateway) handleMaskingSent(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	parts := g.redactEngine().SentParts(key)
	if len(parts) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	type msg struct {
		Role    string `json:"role,omitempty"`
		Content string `json:"content"`
	}
	msgs := make([]msg, 0, len(parts))
	for _, p := range parts {
		role := p.Role
		if role == "" {
			role = "user"
		}
		msgs = append(msgs, msg{Role: role, Content: p.Text})
	}
	body, _ := json.Marshal(map[string]any{"messages": msgs})
	writeJSON(w, http.StatusOK, map[string]any{"body": string(body)})
}

// handleMaskingPreview POST /api/masking/preview {text}
func (g *Gateway) handleMaskingPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败"})
		return
	}
	res, off := g.redactEngine().Preview(body.Text)
	hits := make([]map[string]any, 0, len(res.Hits))
	for _, h := range res.Hits {
		hits = append(hits, map[string]any{
			"placeholder": h.Placeholder, "kind": h.Kind, "by": "rule",
			"action": string(h.Action), "head": h.Head, "len": h.Len, "n": max(1, h.Count),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": res.Text, "hits": hits, "off": off, "blocked": res.Blocked})
}

// handleMaskingClear POST /api/masking/clear
func (g *Gateway) handleMaskingClear(w http.ResponseWriter, r *http.Request) {
	g.redactEngine().Clear()
	writeJSON(w, http.StatusOK, map[string]any{"cleared": true})
}

// syncRedactPrefs 把 UI 偏好（redact / redactPersonal / redactActions /
// redactRules / redactAllow）落进脱敏引擎。
// 供启动与每次保存设置后调用；自己加锁，调用方不得持有 settingsMu。
func (g *Gateway) syncRedactPrefs() {
	g.settingsMu.Lock()
	var on bool
	if v, ok := g.settings.UI["redact"]; ok {
		on = settingTruthy(v)
	}
	personal := true
	if v, ok := g.settings.UI["redactPersonal"]; ok {
		personal = settingTruthy(v)
	}
	actions := map[string]redact.Action{}
	if v, ok := g.settings.UI["redactActions"].(map[string]any); ok {
		for k, raw := range v {
			if s, ok := raw.(string); ok {
				actions[k] = redact.Action(s)
			}
		}
	}
	custom := decodeCustomRules(g.settings.UI["redactRules"])
	allow := decodeAllow(g.settings.UI["redactAllow"])
	words := decodeAllow(g.settings.UI["redactWords"])
	anyCase := settingTruthy(g.settings.UI["redactWordsAnyCase"])
	shape := settingTruthy(g.settings.UI["redactShape"])
	kinds := map[string]bool{}
	if v, ok := g.settings.UI["redactKinds"].(map[string]any); ok {
		for id, raw := range v {
			if kind, known := redactKindIDs[id]; known {
				kinds[kind] = settingTruthy(raw)
			}
		}
	}
	g.settingsMu.Unlock()
	eng := g.redactEngine()
	eng.SetEnabled(on)
	eng.SetPersonal(personal)
	eng.SetActions(actions)
	eng.SetAllow(allow)
	eng.SetWords(words, anyCase)
	eng.SetKindEnabled(kinds)
	eng.SetShape(shape)
	if err := eng.SetCustomRules(custom); err != nil {
		// 手改过 settings.json 导致某条正则编不过：跳过坏的，保留好的。
		good := make([]redact.CustomRule, 0, len(custom))
		for _, ru := range custom {
			if eng.SetCustomRules(append(append([]redact.CustomRule(nil), good...), ru)) == nil {
				good = append(good, ru)
			}
		}
		_ = eng.SetCustomRules(good)
		log.Printf("[WARN] 自定义脱敏规则有 %d 条无法编译，已跳过: %v", len(custom)-len(good), err)
	}
}

// redactKindIDs 把设置页的细项 id 映射到引擎的规则类型。
// 设置页里 userhost/ip 我们的规则集没有对应检测，映射不到就忽略。
var redactKindIDs = map[string]string{
	"ssn":      "SSN",
	"passport": "PASSPORT",
	"iban":     "IBAN",
	"birthday": "BIRTHDAY",
	"mac":      "MAC",
	"serial":   "SERIAL",
	"home":     "HOME",
	"userhost": "USERHOST",
	"ip":       "IP_ADDR",
	"bucket":   "BUCKET",
}

// decodeCustomRules 把设置里的规则（可能是 []any / []map[string]any）解出来。
func decodeCustomRules(v any) []redact.CustomRule {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out []redact.CustomRule
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

// decodeAllow 把设置里的白名单解出来（[]any / []string 均可）。
func decodeAllow(v any) []string {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out []string
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

// customRulesView 把用户规则摊成前端要的形状：{kind, prefix} 或 {kind, regex}。
func customRulesView(eng *redact.Engine) []map[string]any {
	rules := eng.CustomRules()
	out := make([]map[string]any, 0, len(rules))
	for _, ru := range rules {
		m := map[string]any{"kind": ru.Kind}
		if ru.Prefix != "" {
			m["prefix"] = ru.Prefix
		} else {
			m["regex"] = ru.Regex
		}
		out = append(out, m)
	}
	return out
}

// saveRedactPrefsLocked 落盘一个 UI 偏好键（调用方持有 settingsMu）。
func (g *Gateway) saveRedactPrefsLocked(key string, value any) {
	if g.settings.UI == nil {
		g.settings.UI = map[string]any{}
	}
	g.settings.UI[key] = value
	_ = g.saveSettings()
}

// handleRedactRules POST /api/settings/redact-rules {rules:[{kind,prefix|regex}]}
//
// 设置页每次都把全部规则发回（整体替换）：任何一条编不过就整次拒绝，
// 错误显示在添加行，已保存的规则保持原样。
func (g *Gateway) handleRedactRules(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Rules []redact.CustomRule `json:"rules"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败"})
		return
	}
	eng := g.redactEngine()
	if err := eng.SetCustomRules(body.Rules); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	g.settingsMu.Lock()
	g.saveRedactPrefsLocked("redactRules", customRulesView(eng))
	g.settingsMu.Unlock()
	writeJSON(w, http.StatusOK, g.settingsView())
}

// handleRedactRule POST /api/settings/redact-rule {at, was, rule}
//
// 只改第 at 条：was 是前端打开编辑时那条的样子，对不上说明规则已被
// 别处改过（或删了），整次拒绝让前端重画，而不是改错人。
func (g *Gateway) handleRedactRule(w http.ResponseWriter, r *http.Request) {
	var body struct {
		At   *int              `json:"at"`
		Was  redact.CustomRule `json:"was"`
		Rule redact.CustomRule `json:"rule"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败"})
		return
	}
	eng := g.redactEngine()
	cur := eng.CustomRules()
	stale := body.At == nil || *body.At < 0 || *body.At >= len(cur) || !sameCustomRule(cur[*body.At], body.Was)
	if stale {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "规则已被改动，请刷新后重试"})
		return
	}
	next := make([]redact.CustomRule, len(cur))
	copy(next, cur)
	next[*body.At] = body.Rule
	if err := eng.SetCustomRules(next); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	g.settingsMu.Lock()
	g.saveRedactPrefsLocked("redactRules", customRulesView(eng))
	g.settingsMu.Unlock()
	writeJSON(w, http.StatusOK, g.settingsView())
}

func sameCustomRule(a, b redact.CustomRule) bool {
	norm := func(x redact.CustomRule) redact.CustomRule {
		return redact.CustomRule{
			Kind:   redact.NormalizeKind(x.Kind),
			Prefix: strings.TrimSpace(x.Prefix),
			Regex:  strings.TrimSpace(x.Regex),
		}
	}
	return norm(a) == norm(b)
}

// handleRedactAllow POST /api/settings/redact-allow {allow:[...]}
func (g *Gateway) handleRedactAllow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Allow []string `json:"allow"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败"})
		return
	}
	eng := g.redactEngine()
	eng.SetAllow(body.Allow)
	g.settingsMu.Lock()
	g.saveRedactPrefsLocked("redactAllow", eng.Allow())
	g.settingsMu.Unlock()
	writeJSON(w, http.StatusOK, g.settingsView())
}

// handleRedactSettings 读写脱敏开关、规则动作。
//
//	GET  /api/settings/redact-actions → {enabled, rules, actions}
//	POST /api/settings/redact-actions {enabled} 或 {actions:{kind:"mask"}}
//	     → 扁平 prefs（设置页的 writingPrefs 直接消费）
func (g *Gateway) handleRedactSettings(w http.ResponseWriter, r *http.Request) {
	eng := g.redactEngine()

	if r.Method == http.MethodPost {
		var body struct {
			Enabled *bool              `json:"enabled"`
			Actions *map[string]string `json:"actions"`
		}
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		var changed bool
		if len(raw) > 0 && json.Unmarshal(raw, &body) == nil {
			g.settingsMu.Lock()
			if g.settings.UI == nil {
				g.settings.UI = map[string]any{}
			}
			if body.Enabled != nil {
				eng.SetEnabled(*body.Enabled)
				g.settings.UI["redact"] = *body.Enabled
				changed = true
			}
			if body.Actions != nil {
				acts := make(map[string]any, len(*body.Actions))
				for k, v := range *body.Actions {
					acts[k] = v
				}
				eng.SetActions(decodeRedactActions(*body.Actions))
				g.settings.UI["redactActions"] = acts
				changed = true
			}
			if changed {
				_ = g.saveSettings()
			}
			g.settingsMu.Unlock()
		}
		if changed {
			view := g.settingsView()
			view["enabled"] = eng.Enabled()
			view["rules"] = redactRulesView(eng)
			view["actions"] = eng.Actions()
			writeJSON(w, http.StatusOK, view)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": eng.Enabled(),
		"rules":   redactRulesView(eng),
		"actions": eng.Actions(),
	})
}

func decodeRedactActions(in map[string]string) map[string]redact.Action {
	out := make(map[string]redact.Action, len(in))
	for k, v := range in {
		switch redact.Action(v) {
		case redact.ActionMask, redact.ActionWarn, redact.ActionBlock:
			out[k] = redact.Action(v)
		}
	}
	return out
}

func redactRulesView(eng *redact.Engine) []map[string]any {
	rules := eng.Rules()
	out := make([]map[string]any, 0, len(rules))
	for _, ru := range rules {
		out = append(out, map[string]any{
			"id": ru.ID, "name": ru.Name, "kind": ru.Kind,
			"action": string(ru.Action), "enabled": ru.Enabled,
		})
	}
	return out
}

// redactEngine 返回脱敏引擎（懒初始化）。
func (g *Gateway) redactEngine() *redact.Engine {
	g.redactOnce.Do(func() {
		g.redactE = redact.New()
	})
	return g.redactE
}
