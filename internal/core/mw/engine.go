// Package mw 是声明式的请求改写中间件。
//
// 上游对应的是 aether 的 JS 插件钩子（onRequest / onEvent / onResponse），
// 那套设计要跑一个 Bun 运行时。这里换成配置驱动的 Go 实现：
//
//	好处：零运行时依赖、可审计、单二进制、出错不会拖垮网关
//	代价：不能写任意 JS 逻辑（但对 90% 的改写需求，声明式已经够用）
//
// 四类中间件：
//   - param_override  统一改写生成参数（temperature / max_tokens / top_p）
//   - model_map       把请求里的模型名重写后再发往上游
//   - word_guard      命中关键词时拦截，返回可读的错误
//   - system_prompt   给每个请求注入一段系统提示
package mw

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"sync"
)

// Kind 是中间件类型。
type Kind string

const (
	KindParamOverride Kind = "param_override"
	KindModelMap      Kind = "model_map"
	KindWordGuard     Kind = "word_guard"
	KindSystemPrompt  Kind = "system_prompt"
)

// Rule 是一条中间件配置。
//
// 所有类型共用一个结构：用哪几个字段取决于 Kind，
// 好处是配置与 UI 都只有一种形状，不需要为每类中间件写一套表单。
type Rule struct {
	ID      string `json:"id"`
	Kind    Kind   `json:"kind"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// Note 给人看的说明。
	Note string `json:"note,omitempty"`

	// --- 适用范围 ---
	// Providers 为空表示全部渠道；否则只对这些渠道生效。
	Providers []string `json:"providers,omitempty"`
	// Models 为模型名的匹配模式（支持 * 通配），为空表示全部模型。
	Models []string `json:"models,omitempty"`

	// --- param_override ---
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`

	// --- model_map ---
	// From 是匹配的模型名（支持 * 通配），To 是替换后的名字。
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`

	// --- word_guard ---
	Words    []string `json:"words,omitempty"`
	Patterns []string `json:"patterns,omitempty"`
	// Message 是拦截时返回给调用方的说明。
	Message string `json:"message,omitempty"`

	// --- system_prompt ---
	Prompt string `json:"prompt,omitempty"`
	// Position 是注入位置：prepend（默认）| append
	Position string `json:"position,omitempty"`
}

// Engine 持有全部规则并持久化。
type Engine struct {
	mu    sync.RWMutex
	rules []Rule
	path  string

	// 编译后的匹配器，避免每请求重复编译
	modelRes map[string]*regexp.Regexp
	wordRes  map[string]*regexp.Regexp
}

// New 创建引擎并从 path 载入配置。path 为空则只在内存里。
func New(path string) *Engine {
	e := &Engine{path: path, modelRes: map[string]*regexp.Regexp{}, wordRes: map[string]*regexp.Regexp{}}
	e.load()
	return e
}

// knownKinds 是 Apply 真正会执行的规则类型。
//
// 单独列一张表而不是在 switch 里数 default：Enabled 要回答的问题是
// "直通会不会让某条本来会生效的规则失效"，而未知 Kind 在 Apply 里本来就被跳过，
// 把它算进来只会白白牺牲直通性能。
var knownKinds = map[Kind]bool{
	KindParamOverride: true,
	KindModelMap:      true,
	KindWordGuard:     true,
	KindSystemPrompt:  true,
}

// Enabled 报告是否至少有一条会生效的规则。
//
// 存在的理由是 Fast-Path：直通路径不解析请求体，所有改写规则都没有机会执行。
// 所以只要有一条启用规则，调用方就必须放弃直通、退回 IR 路径；
// 这里刻意按"启用 + 已知类型"从严判断，不看规则的字段是否填满 ——
// 空白规则也可能在运行期被热更新填上内容，少挡一次就是静默丢掉用户配置。
func (e *Engine) Enabled() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, r := range e.rules {
		if r.Enabled && knownKinds[r.Kind] {
			return true
		}
	}
	return false
}

// Rules 返回规则快照。
func (e *Engine) Rules() []Rule {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Rule, len(e.rules))
	copy(out, e.rules)
	return out
}

// SetRules 整体替换规则并落盘。
func (e *Engine) SetRules(rules []Rule) error {
	e.mu.Lock()
	e.rules = rules
	e.compileLocked()
	e.mu.Unlock()
	return e.save()
}

// Upsert 新增或更新一条规则。
func (e *Engine) Upsert(r Rule) error {
	e.mu.Lock()
	found := false
	for i := range e.rules {
		if e.rules[i].ID == r.ID {
			e.rules[i] = r
			found = true
			break
		}
	}
	if !found {
		e.rules = append(e.rules, r)
	}
	e.compileLocked()
	e.mu.Unlock()
	return e.save()
}

// Delete 删除一条规则。
func (e *Engine) Delete(id string) error {
	e.mu.Lock()
	out := e.rules[:0]
	for _, r := range e.rules {
		if r.ID != id {
			out = append(out, r)
		}
	}
	e.rules = out
	e.compileLocked()
	e.mu.Unlock()
	return e.save()
}

/* ---------------- 执行 ---------------- */

// Context 是一次请求的上下文，供规则判断适用范围。
type Context struct {
	Provider string
	Model    string
}

// Apply 依次执行所有启用规则，返回可能的拦截原因。
//
// 参数改写直接作用在指针上；拦截则立刻返回，不再继续执行。
func (e *Engine) Apply(ctx *Context, temperature *float64, topP *float64, maxTokens *int, instructions *string) (blocked string) {
	e.mu.RLock()
	rules := make([]Rule, len(e.rules))
	copy(rules, e.rules)
	e.mu.RUnlock()

	for _, r := range rules {
		if !r.Enabled || !r.applies(ctx, e) {
			continue
		}
		switch r.Kind {
		case KindParamOverride:
			// 只在请求没显式指定（零值）时才覆盖：用户写了就尊重用户。
			// 注意必须先判指针非 nil 再解引用，否则零值请求会 panic。
			if temperature != nil && *temperature == 0 && r.Temperature != nil {
				*temperature = *r.Temperature
			}
			if topP != nil && *topP == 0 && r.TopP != nil {
				*topP = *r.TopP
			}
			if maxTokens != nil && *maxTokens == 0 && r.MaxTokens != nil {
				*maxTokens = *r.MaxTokens
			}

		case KindModelMap:
			if r.From != "" && r.To != "" && ctx.Model != "" {
				if e.matchModel(r.From, ctx.Model) {
					ctx.Model = r.To
				}
			}

		case KindWordGuard:
			if msg := r.guard(ctx, instructions, e); msg != "" {
				return msg
			}

		case KindSystemPrompt:
			if r.Prompt != "" {
				if instructions == nil {
					continue
				}
				if r.Position == "append" {
					*instructions = strings.TrimSpace(*instructions + "\n\n" + r.Prompt)
				} else {
					*instructions = strings.TrimSpace(r.Prompt + "\n\n" + *instructions)
				}
			}
		}
	}
	return ""
}

// applies 判断规则是否作用于这次请求。
func (r Rule) applies(ctx *Context, e *Engine) bool {
	if len(r.Providers) > 0 {
		hit := false
		for _, p := range r.Providers {
			if p == ctx.Provider {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if len(r.Models) > 0 {
		hit := false
		for _, m := range r.Models {
			if e.matchModel(m, ctx.Model) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// guard 检查关键词与正则，命中返回拦截说明。
func (r Rule) guard(ctx *Context, instructions *string, e *Engine) string {
	text := ""
	if instructions != nil {
		text = *instructions
	}
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)

	for _, w := range r.Words {
		if w == "" {
			continue
		}
		if strings.Contains(lower, strings.ToLower(w)) {
			if r.Message != "" {
				return r.Message
			}
			return "请求包含被拦截的内容（规则：" + r.Name + "）"
		}
	}

	for _, p := range r.Patterns {
		if re, ok := e.wordRes[p]; ok && re != nil && re.MatchString(text) {
			if r.Message != "" {
				return r.Message
			}
			return "请求包含被拦截的内容（规则：" + r.Name + "）"
		}
	}
	return ""
}

// matchModel 用 * 通配匹配模型名。
func (e *Engine) matchModel(pattern, model string) bool {
	if pattern == model {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return false
	}
	re, ok := e.modelRes[pattern]
	if !ok || re == nil {
		return false
	}
	return re.MatchString(model)
}

/* ---------------- 内部 ---------------- */

// compileLocked 预编译通配与正则。调用方必须持锁。
func (e *Engine) compileLocked() {
	e.modelRes = make(map[string]*regexp.Regexp, 16)
	e.wordRes = make(map[string]*regexp.Regexp, 16)

	for _, r := range e.rules {
		if r.From != "" && strings.Contains(r.From, "*") {
			if re, err := wildcard(r.From); err == nil {
				e.modelRes[r.From] = re
			}
		}
		for _, p := range r.Patterns {
			if p == "" {
				continue
			}
			if re, err := regexp.Compile(p); err == nil {
				e.wordRes[p] = re
			}
		}
	}
}

// wildcard 把 * 通配转成锚定正则。
func wildcard(pat string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for _, part := range strings.Split(pat, "*") {
		b.WriteString(regexp.QuoteMeta(part))
		b.WriteString(".*")
	}
	s := strings.TrimSuffix(b.String(), ".*")
	s += "$"
	return regexp.Compile(s)
}

func (e *Engine) load() {
	if e.path == "" {
		return
	}
	b, err := os.ReadFile(e.path)
	if err != nil {
		return
	}
	var rules []Rule
	if json.Unmarshal(b, &rules) == nil {
		e.mu.Lock()
		e.rules = rules
		e.compileLocked()
		e.mu.Unlock()
	}
}

func (e *Engine) save() error {
	if e.path == "" {
		return nil
	}
	e.mu.RLock()
	b, err := json.MarshalIndent(e.rules, "", "  ")
	e.mu.RUnlock()
	if err != nil {
		return err
	}

	dir := e.path
	if i := strings.LastIndexAny(dir, `/\`); i > 0 {
		dir = dir[:i]
	}
	_ = os.MkdirAll(dir, 0o755)

	tmp := e.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, e.path)
}
