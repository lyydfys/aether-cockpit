// Package redact 在请求离开本机之前，把敏感信息替换成占位符。
//
// 为什么需要它：用户让 AI 改代码时，常把配置片段、日志、报错整段贴进对话。
// 这些内容里往往夹着 API Key、邮箱、身份证号。请求一旦发给上游，
// 这些东西就离开了本机，而且会留在对方的日志里。
//
// 设计要点：
//
//  1. **同一值永远是同一个占位符**。这不是为了好看：上游按 prompt 内容做缓存，
//     同一段文本前后两次出现不同的占位符会让缓存全部失效；思考签名也可能因此对不上。
//  2. **占位符可逆**。上游返回的文本里带着占位符，网关在回程路上换回原值，
//     用户看到的仍是完整内容。
//  3. **值只在内存**。落盘的是「有几个值被替换」的计数，不是值本身。
//  4. **失败就拦**。引擎出错时返回 503，而不是把明文发出去——
//     脱敏的意义就在于它不能"静默失效"。
package redact

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Action 是命中一条规则后的处置方式。
type Action string

const (
	// ActionMask 替换成占位符（默认）。
	ActionMask Action = "mask"
	// ActionWarn 只记录，不改写。
	ActionWarn Action = "warn"
	// ActionBlock 直接拦截整个请求。
	ActionBlock Action = "block"
)

// Rule 是一条脱敏规则。
type Rule struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Action  Action `json:"action"`
	Enabled bool   `json:"enabled"`

	re *regexp.Regexp
	// group >0 时只替换该捕获组（"passport: AB1234567" 里的号码），
	// 关键词本身留在原地。
	group int
	// validate 可选：再校验一次命中（IBAN 校验和、序列号必须含数字）。
	validate func(string) bool
}

// span 是一次命中在文本里的位置（有捕获组时是组内位置）。
type span struct {
	start, end int
	text       string
}

// findSpans 返回要替换的片段与位置；有捕获组时只取组内文本。
//
// 用位置而不是"再按文本全局替换"：同一段文本出现在命中之外
// （Nightjars 里的 Nightjar）时，只有真正命中的那处被替换。
func (r Rule) findSpans(s string) []span {
	if r.group <= 0 && r.validate == nil {
		idx := r.re.FindAllStringIndex(s, -1)
		out := make([]span, 0, len(idx))
		for _, m := range idx {
			out = append(out, span{start: m[0], end: m[1], text: s[m[0]:m[1]]})
		}
		return out
	}
	idx := r.re.FindAllStringSubmatchIndex(s, -1)
	out := make([]span, 0, len(idx))
	for _, m := range idx {
		a, b := m[0], m[1]
		if r.group > 0 {
			if 2*r.group+1 >= len(m) || m[2*r.group] < 0 {
				continue
			}
			a, b = m[2*r.group], m[2*r.group+1]
		}
		v := s[a:b]
		if r.validate != nil && !r.validate(v) {
			continue
		}
		out = append(out, span{start: a, end: b, text: v})
	}
	return out
}

// CustomRule 是用户在设置页添加的规则：
// 以 prefix 开头的一段 token，或一条正则表达式；kind 决定占位符的类型前缀。
type CustomRule struct {
	Kind   string `json:"kind"`
	Prefix string `json:"prefix,omitempty"`
	Regex  string `json:"regex,omitempty"`
}

// NormalizeKind 把用户填的类型名规范成占位符里的形式：
// 大写、空白与 . - 换成 _、丢掉其他符号；空则 CUSTOM。
// 与前端 maskRuleKind 保持一致，否则动作表按 kind 覆盖时对不上。
func NormalizeKind(k string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(k)) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '.' || r == '-':
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "CUSTOM"
	}
	return out
}

// prefixPattern 把"以它开头"的规则编成正则：前缀本身不可元字符解释，
// 后面同一 token 的字符一并吃掉（API Key 的后半段），整段替换。
func prefixPattern(prefix string) string {
	pat := regexp.QuoteMeta(prefix)
	if rs := []rune(prefix); len(rs) > 0 {
		switch r := rs[0]; {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			pat = `\b` + pat
		}
	}
	return pat + `[A-Za-z0-9_.+/=\-]*`
}

// Hit 是一次命中。
type Hit struct {
	RuleID string `json:"rule_id"`
	Rule   string `json:"rule"`
	Kind   string `json:"kind"`
	Action Action `json:"action"`
	// Placeholder 是替换后的占位符（ActionMask 或 ActionBlock 时非空）。
	Placeholder string `json:"placeholder,omitempty"`
	// Blocked 标记这条命中把整个请求判死了（ActionBlock）。
	// 中间件据此拒发；即便调用方漏判，正文里也已经是占位符而不是原文。
	Blocked bool `json:"blocked,omitempty"`
	// By 是发现者：rule（正则规则）/ model（本地模型，暂未接入）。
	By string `json:"by,omitempty"`
	// Head / Len 是原值的首几个字符与长度：脱敏页只展示形状，
	// 原文永远只在内存里按需取用。
	Head string `json:"head,omitempty"`
	Len  int    `json:"len,omitempty"`
	// Count 是该命中在这一处出现的次数。
	Count int `json:"n,omitempty"`
}

// Record 是一条可审计的记录。只存元信息，不存原值。
type Record struct {
	Key       string    `json:"key"`
	At        time.Time `json:"at"`
	Provider  string    `json:"provider,omitempty"`
	Model     string    `json:"model,omitempty"`
	Hits      []Hit     `json:"hits"`
	Chars     int       `json:"chars"`
	Masked    int       `json:"masked"`
	Blocked   bool      `json:"blocked,omitempty"`
	Direction string    `json:"direction,omitempty"`
	// Agent 是发起请求的客户端（能识别时），What 是请求类型（chat 等）。
	Agent string `json:"agent,omitempty"`
	What  string `json:"what,omitempty"`
}

// SentPart 是一次请求实际发出去的文本（已脱敏），按角色记录。
// 只存在内存，重启即失：脱敏页的 "What the vendor saw" 用它。
type SentPart struct {
	Role string `json:"role,omitempty"`
	Text string `json:"text"`
}

// Engine 是脱敏引擎。并发安全。
type Engine struct {
	mu sync.RWMutex

	rules []Rule

	// salt 是进程级随机密钥，参与占位符派生。
	//
	// 为什么必须加盐：不加盐时占位符是 sha256(kind|value) 的纯函数，谁拿到
	// 占位符就能离线穷举原值（手机号、身份证、邮箱的候选空间都很小）。
	// 加盐后"同值同符"仍然成立（上游缓存与思考签名不受影响），但离开本进程
	// 就无法反推。只存在内存里，重启即换新盐——与"值只在内存"的既有约束一致。
	salt []byte

	// forward: 原值 -> 占位符；reverse: 占位符 -> 原值。
	// 两张表都在内存，进程退出即消失。
	forward map[string]string
	reverse map[string]string
	// valueOrder 记录插入顺序，超限时淘汰最旧的值（近似 FIFO，见 evictValuesLocked）。
	valueOrder []string

	records    []Record
	maxRecords int

	// actions 是按 kind 覆盖的处置方式（脱敏页 "Mask it instead"）。
	actions map[string]Action
	// custom 是用户自己的规则（已编译），customSrc 是它的原始形态。
	custom    []Rule
	customSrc []CustomRule
	// words 是用户手填的"脱敏词"（kind TERM）。
	words []Rule
	// allow 是"永不脱敏"的白名单（整值比较，忽略大小写）。
	allow    []string
	allowSet map[string]bool
	// kindEnabled 是按类型开关（个人信息细项，如 MAC/SERIAL）。
	// 缺省表示按规则自带默认（大部分开、主目录与桶关）。
	kindEnabled map[string]bool
	// sent 是每个请求实际发出去的文本，只保留最新的若干条。
	sent      map[string][]SentPart
	sentOrder []string
	// backs 是每个请求的占位符在回复里被换回的次数（仅内存）。
	backs  map[string]map[string]int
	keySeq atomic.Uint64

	enabled  bool
	personal bool
	// shape 打开时，密钥类值以"同形假值"发出（sk-… 仍是 sk-…），
	// 记录里仍用占位符；回程两种都还原。
	shape bool
}

// maxValues 是 forward/reverse 两张映射的容量上限。
//
// 为什么要有上限：进程长时间运行时，每遇到一个新的敏感值就多一条记录，
// 之前是完全无界的（只增不减）。这既是内存泄漏，也会让回程的 map 遍历变慢。
// 20000 条按每条约百字节估算约 2MB，够覆盖一次长会话的全部历史。
const maxValues = 20000

// New 创建引擎并装载内置规则。
func New() *Engine {
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		// 极端情况下拿不到熵：退回时间派生，仍然比不加盐强。
		binary.BigEndian.PutUint64(salt, uint64(time.Now().UnixNano()))
	}
	e := &Engine{
		salt:       salt,
		forward:    make(map[string]string, 64),
		reverse:    make(map[string]string, 64),
		actions:    make(map[string]Action, 4),
		sent:       make(map[string][]SentPart, 32),
		backs:      make(map[string]map[string]int, 32),
		maxRecords: 500,
		personal:   true,
	}
	e.rules = builtinRules()
	return e
}

// SetEnabled 开关脱敏。
func (e *Engine) SetEnabled(on bool) {
	e.mu.Lock()
	e.enabled = on
	e.mu.Unlock()
}

// Enabled 返回是否开启。
func (e *Engine) Enabled() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.enabled
}

// SetPersonal 控制是否脱敏个人身份类（邮箱/手机号/证件/银行卡）。
func (e *Engine) SetPersonal(on bool) {
	e.mu.Lock()
	e.personal = on
	e.mu.Unlock()
}

// SetShape 控制"保留密钥格式"：打开后密钥类值以同形假值发出，
// 不再是 {{API_KEY_…}} 占位符（回程仍可还原）。
func (e *Engine) SetShape(on bool) {
	e.mu.Lock()
	e.shape = on
	e.mu.Unlock()
}

// Shape 返回是否保留密钥格式。
func (e *Engine) Shape() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.shape
}

// shapeKind 是"保留格式"适用的类型：真正的密钥/令牌/私钥/口令。
func shapeKind(kind string) bool {
	switch kind {
	case "API_KEY", "TOKEN", "PRIVATE_KEY", "SECRET", "PASSWORD":
		return true
	}
	return false
}

// shapeValue 生成与原值同形、同长度的假值（确定性，同值同形）。
// 字母→字母（保留大小写）、数字→数字、其余字符原样保留；
// 开头到第一个分隔符（- _ .）的短前缀原样保留："sk-" 仍是 "sk-"。
//
// salt 与占位符共用同一份进程级盐：不加盐时"假值"是原值的确定性函数，
// 拿到假值就能对候选空间离线比对（例如猜一个手机号）。加盐后同一次运行内
// 仍然"同值同形"（上游缓存与回程还原都依赖这一点），跨进程就无法关联。
func shapeValue(salt []byte, value, kind string) string {
	mac := hmac.New(sha256.New, salt)
	mac.Write([]byte("shape"))
	mac.Write([]byte{0})
	mac.Write([]byte(kind))
	mac.Write([]byte{0})
	mac.Write([]byte(value))
	sum := mac.Sum(nil)
	rs := []rune(value)
	keep := 0
	for i, r := range rs {
		if r == '-' || r == '_' || r == '.' {
			if i+1 <= 10 {
				keep = i + 1
			}
			break
		}
	}
	var b strings.Builder
	b.WriteString(string(rs[:keep]))
	bs := 0
	next := func() byte {
		v := sum[bs%len(sum)]
		bs++
		return v
	}
	for _, r := range rs[keep:] {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteByte('a' + next()%26)
		case r >= 'A' && r <= 'Z':
			b.WriteByte('A' + next()%26)
		case r >= '0' && r <= '9':
			b.WriteByte('0' + next()%10)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// shapedFor 在"保留格式"打开且类型适用时返回同形假值；否则返回 ""。
func (e *Engine) shapedFor(value, kind string) string {
	if !e.shape || !shapeKind(kind) || len([]rune(value)) < 8 {
		return ""
	}
	return shapeValue(e.salt, value, kind)
}

// Personal 返回是否脱敏个人身份类。
func (e *Engine) Personal() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.personal
}

// personalKind 是"个人信息"类规则：受 redactPersonal 开关控制。
func personalKind(kind string) bool {
	switch kind {
	case "EMAIL", "PHONE", "ID", "CARD",
		"SSN", "PASSPORT", "IBAN", "BIRTHDAY", "MAC", "SERIAL", "HOME", "BUCKET",
		"USERHOST", "IP_ADDR":
		return true
	}
	return false
}

// kindDefaultOff 是默认关闭的个人信息细项（aether 的 REDACT_KINDS 默认值）：
// 主目录里的用户名几乎每个请求都有，桶名多为公开配置；
// 提示符里的 user@host 与公网 IP 在日志/配置里出现太频繁。
var kindDefaultOff = map[string]bool{"HOME": true, "BUCKET": true, "USERHOST": true, "IP_ADDR": true}

// SetKindEnabled 按类型开关个人信息细项；缺省用 kindDefaultOff。
func (e *Engine) SetKindEnabled(kinds map[string]bool) {
	out := make(map[string]bool, len(kinds))
	for k, v := range kinds {
		out[strings.ToUpper(strings.TrimSpace(k))] = v
	}
	e.mu.Lock()
	e.kindEnabled = out
	e.mu.Unlock()
}

// KindEnabled 返回某类型此刻是否开启。
func (e *Engine) KindEnabled(kind string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if v, ok := e.kindEnabled[kind]; ok {
		return v
	}
	return !kindDefaultOff[kind]
}

// kindOnLocked 判断某类型在个人信息开关与细项开关下是否生效（调用方持有锁）。
func (e *Engine) kindOnLocked(kind string) bool {
	if !personalKind(kind) {
		return true
	}
	if !e.personal {
		return false
	}
	if v, ok := e.kindEnabled[kind]; ok {
		return v
	}
	return !kindDefaultOff[kind]
}

// SetWords 编译用户手填的脱敏词（kind TERM）。
//
// anyCase=false 时按原样匹配（大小写敏感），true 时不分大小写。
// 词按整词边界匹配：Nightjar 不会命中 Nightjars 里的片段以外的巧合，
// 也不会命中别的单词内部。
func (e *Engine) SetWords(words []string, anyCase bool) {
	out := make([]Rule, 0, len(words))
	seen := make(map[string]bool, len(words))
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w == "" || len([]rune(w)) > 100 {
			continue
		}
		key := w
		if anyCase {
			key = strings.ToLower(w)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		pat := regexp.QuoteMeta(w)
		if rs := []rune(w); len(rs) > 0 {
			wordChar := func(r rune) bool {
				return r == '_' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r > 127
			}
			if wordChar(rs[0]) {
				pat = `\b` + pat
			}
			if wordChar(rs[len(rs)-1]) {
				pat += `\b`
			}
		}
		if anyCase {
			pat = `(?i)` + pat
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			continue
		}
		out = append(out, Rule{
			ID: "word-" + strconv.Itoa(len(out)+1), Name: "Word: " + w,
			Kind: "TERM", Action: ActionMask, Enabled: true, re: re,
		})
		if len(out) >= maxAllowValues {
			break
		}
	}
	e.mu.Lock()
	e.words = out
	e.mu.Unlock()
}

// Words 返回当前的脱敏词（供测试与回显）。
func (e *Engine) Words() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, 0, len(e.words))
	for _, r := range e.words {
		out = append(out, strings.TrimPrefix(r.Name, "Word: "))
	}
	return out
}

// Rules 返回规则快照（供设置页展示）。
func (e *Engine) Rules() []Rule {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Rule, len(e.rules))
	copy(out, e.rules)
	return out
}

// Result 是一次脱敏的结果。
type Result struct {
	Text    string
	Hits    []Hit
	Blocked bool
}

// SetActions 按 kind 覆盖处置方式（mask / warn / block）。
func (e *Engine) SetActions(actions map[string]Action) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.actions = make(map[string]Action, len(actions))
	for k, v := range actions {
		switch v {
		case ActionMask, ActionWarn, ActionBlock:
			e.actions[k] = v
		}
	}
}

// Actions 返回当前覆盖表。
func (e *Engine) Actions() map[string]Action {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make(map[string]Action, len(e.actions))
	for k, v := range e.actions {
		out[k] = v
	}
	return out
}

// 用户规则的上限：设置页手填的东西，不需要无限大。
const (
	maxCustomRules = 100
	maxAllowValues = 200
)

// SetCustomRules 整体替换用户规则。
//
// 任何一条编不过（正则写错、prefix 和 regex 都空）就整次拒绝：
// 设置页把"哪条不行"显示在原处，其余规则保持原样，而不是一半生效。
func (e *Engine) SetCustomRules(rules []CustomRule) error {
	if len(rules) > maxCustomRules {
		return fmt.Errorf("规则最多 %d 条", maxCustomRules)
	}
	compiled := make([]Rule, 0, len(rules))
	src := make([]CustomRule, 0, len(rules))
	for i, ru := range rules {
		kind := NormalizeKind(ru.Kind)
		prefix := strings.TrimSpace(ru.Prefix)
		regex := strings.TrimSpace(ru.Regex)
		if kind == "" || len([]rune(kind)) > 40 {
			return fmt.Errorf("第 %d 条规则的类型名太长", i+1)
		}
		if prefix == "" && regex == "" {
			return fmt.Errorf("第 %d 条规则缺少要匹配的内容", i+1)
		}
		if prefix != "" && len([]rune(prefix)) > 100 {
			return fmt.Errorf("第 %d 条规则的前缀太长", i+1)
		}
		pat, name, clean := "", "", CustomRule{Kind: kind}
		if prefix != "" {
			pat = prefixPattern(prefix)
			name = "Starts with " + prefix
			clean.Prefix = prefix
		} else {
			if len([]rune(regex)) > 500 {
				return fmt.Errorf("第 %d 条规则的正则太长", i+1)
			}
			pat = regex
			name = "Matches " + regex
			clean.Regex = regex
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return fmt.Errorf("第 %d 条规则无法编译: %v", i+1, err)
		}
		compiled = append(compiled, Rule{
			ID: "custom-" + strconv.Itoa(i+1), Name: name, Kind: kind,
			Action: ActionMask, Enabled: true, re: re,
		})
		src = append(src, clean)
	}
	e.mu.Lock()
	e.custom = compiled
	e.customSrc = src
	e.mu.Unlock()
	return nil
}

// CustomRules 返回用户规则快照。
func (e *Engine) CustomRules() []CustomRule {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]CustomRule, len(e.customSrc))
	copy(out, e.customSrc)
	return out
}

// SetAllow 设置"永不脱敏"的整值白名单（忽略大小写）。
func (e *Engine) SetAllow(values []string) {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || len([]rune(v)) > 200 {
			continue
		}
		key := strings.ToLower(v)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
		if len(out) >= maxAllowValues {
			break
		}
	}
	e.mu.Lock()
	e.allow = out
	e.allowSet = seen
	e.mu.Unlock()
}

// Allow 返回白名单快照。
func (e *Engine) Allow() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, len(e.allow))
	copy(out, e.allow)
	return out
}

// allowedLocked 判断某个命中的整值是否在白名单里（调用方持有锁）。
func (e *Engine) allowedLocked(match string) bool {
	if len(e.allowSet) == 0 {
		return false
	}
	return e.allowSet[strings.ToLower(match)]
}

// ValueOf 返回占位符对应的原值（仅在内存里）。
func (e *Engine) ValueOf(placeholder string) (string, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	v, ok := e.reverse[placeholder]
	return v, ok
}

// SentParts 返回某次请求实际发送的文本片段（已脱敏）。
func (e *Engine) SentParts(key string) []SentPart {
	e.mu.RLock()
	defer e.mu.RUnlock()
	parts := e.sent[key]
	out := make([]SentPart, len(parts))
	copy(out, parts)
	return out
}

// NoteBack 记录某次回复里出现的占位符（用于"换回了几处"）。
func (e *Engine) NoteBack(key, text string) {
	if key == "" || text == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for {
		i := strings.Index(text, "{{")
		if i < 0 {
			return
		}
		j := strings.Index(text[i:], "}}")
		if j < 0 {
			return
		}
		p := text[i : i+j+2]
		text = text[i+j+2:]
		if _, ok := e.reverse[p]; !ok {
			continue
		}
		m := e.backs[key]
		if m == nil {
			m = map[string]int{}
			e.backs[key] = m
		}
		m[p]++
	}
}

// Backs 返回某次请求的占位符换回次数。
func (e *Engine) Backs(key string) map[string]int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	m := e.backs[key]
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// NewRequestKey 生成一个递增的十进制请求键：前端用 BigInt 比较新旧，
// 所以必须是纯数字且随时间单调。
func (e *Engine) NewRequestKey() string {
	n := e.keySeq.Add(1)
	return strconv.FormatInt(time.Now().UnixNano(), 10) + fmt.Sprintf("%04d", n%10000)
}

// Mask 替换文本里的敏感信息。
//
// meta 用于记录审计（provider / model），可为空。
func (e *Engine) Mask(text, provider, model string) Result {
	return e.mask(text, provider, model, "", "", "", true)
}

// MaskWithMeta 是 Mask 的完整形态：key 归组同一次请求的多段文本，
// agent / role 供脱敏页展示；只记录，不影响返回。
func (e *Engine) MaskWithMeta(text, provider, model, key, agent, role string) Result {
	return e.mask(text, provider, model, key, agent, role, true)
}

func (e *Engine) mask(text, provider, model, key, agent, role string, keep bool) Result {
	if text == "" {
		return Result{Text: text}
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.enabled {
		return Result{Text: text}
	}

	var hits []Hit
	out := text
	blocked := false
	masked := 0

	for _, list := range [3][]Rule{e.rules, e.custom, e.words} {
		for _, r := range list {
			if !r.Enabled || r.re == nil || !e.kindOnLocked(r.Kind) {
				continue
			}
			action := r.Action
			if a, ok := e.actions[r.Kind]; ok {
				action = a
			}
			spans := r.findSpans(out)
			if len(spans) == 0 {
				continue
			}
			var b strings.Builder
			last := 0

			for _, sp := range spans {
				m := sp.text
				if m == "" || e.allowedLocked(m) {
					continue
				}
				h := Hit{RuleID: r.ID, Rule: r.Name, Kind: r.Kind, Action: action, By: "rule", Head: headOf(m), Len: len([]rune(m)), Count: 1}
				b.WriteString(out[last:sp.start])
				switch action {
				case ActionBlock:
					// 拦截：仍然把原值换成占位符（绝不把原文写回），
					// 否则下游一旦没检查 Blocked，就等于"设了拦截却明文发出"。
					blocked = true
					ph := e.placeholderFor(m, r.Kind)
					b.WriteString(ph)
					h.Placeholder = ph
					h.Blocked = true
				case ActionWarn:
					b.WriteString(m)
				default:
					ph := e.placeholderFor(m, r.Kind)
					sent := ph
					if shaped := e.shapedFor(m, r.Kind); shaped != "" {
						sent = shaped
						e.reverse[shaped] = m
					}
					b.WriteString(sent)
					h.Placeholder = ph
					masked++

				}
				last = sp.end
				hits = append(hits, h)
			}
			if last > 0 {
				b.WriteString(out[last:])
				out = b.String()
			}
		}
	}

	if len(hits) > 0 && keep {
		if key == "" {
			key = e.newKeyLocked()
		}
		e.record(Record{
			Key:       key,
			At:        time.Now(),
			Provider:  provider,
			Model:     model,
			Hits:      hits,
			Chars:     len(text),
			Masked:    masked,
			Blocked:   blocked,
			Direction: "request",
			Agent:     agent,
			What:      "chat",
		})
		e.addSentLocked(key, role, out)
	}

	return Result{Text: out, Hits: hits, Blocked: blocked}
}

func (e *Engine) newKeyLocked() string {
	n := e.keySeq.Add(1)
	return strconv.FormatInt(time.Now().UnixNano(), 10) + fmt.Sprintf("%04d", n%10000)
}

// Preview 在不记录、不写入内存映射的前提下演示脱敏结果。
// 开关关闭时也演示（off=true），供设置页 "试一试"。
func (e *Engine) Preview(text string) (Result, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	off := !e.enabled
	var hits []Hit
	out := text
	blocked := false
	for _, list := range [3][]Rule{e.rules, e.custom, e.words} {
		for _, r := range list {
			if !r.Enabled || r.re == nil || !e.kindOnLocked(r.Kind) {
				continue
			}
			action := r.Action
			if a, ok := e.actions[r.Kind]; ok {
				action = a
			}
			spans := r.findSpans(out)
			if len(spans) == 0 {
				continue
			}
			var b strings.Builder
			last := 0
			for _, sp := range spans {
				m := sp.text
				if m == "" || e.allowedLocked(m) {
					continue
				}
				h := Hit{RuleID: r.ID, Rule: r.Name, Kind: r.Kind, Action: action, By: "rule", Head: headOf(m), Len: len([]rune(m)), Count: 1}
				b.WriteString(out[last:sp.start])
				switch action {
				case ActionBlock:
					// 预览与真跑同一语义：拦截照样换成占位符，页面上不出现原文。
					blocked = true
					ph := e.placeholderName(m, r.Kind)
					b.WriteString(ph)
					h.Placeholder = ph
					h.Blocked = true
				case ActionWarn:
					b.WriteString(m)
				default:
					ph := e.placeholderName(m, r.Kind)
					sent := ph
					if shaped := e.shapedFor(m, r.Kind); shaped != "" {
						sent = shaped
					}
					b.WriteString(sent)
					h.Placeholder = ph
				}
				last = sp.end
				hits = append(hits, h)
			}
			if last > 0 {
				b.WriteString(out[last:])
				out = b.String()
			}
		}
	}
	return Result{Text: out, Hits: hits, Blocked: blocked}, off
}

// headOf 返回原值的首 4 个字符（按 rune），用于展示 "形状"。
func headOf(v string) string {
	rs := []rune(v)
	if len(rs) > 4 {
		rs = rs[:4]
	}
	return string(rs)
}

// placeholderName 是不带盐的派生，只作为"没有引擎实例"时的兜底与测试辅助。
// 生产路径一律走 Engine.placeholderName（带进程级盐）。
func placeholderName(value, kind string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + value))
	return fmt.Sprintf("{{%s_%s}}", strings.ToUpper(kind), hex.EncodeToString(sum[:8]))
}

// addSentLocked 记录一次请求实际发出的文本（调用方持有写锁）。
func (e *Engine) addSentLocked(key, role, text string) {
	if key == "" {
		return
	}
	if _, ok := e.sent[key]; !ok {
		e.sentOrder = append(e.sentOrder, key)
		if len(e.sentOrder) > 300 {
			old := e.sentOrder[0]
			e.sentOrder = e.sentOrder[1:]
			delete(e.sent, old)
			delete(e.backs, old)
		}
	}
	e.sent[key] = append(e.sent[key], SentPart{Role: role, Text: text})
}

// Unmask 把上游返回文本里的占位符换回原值。
//
// 只走 reverse 表：不认识的占位符原样保留，不做猜测。
// Unmask 把文本里的占位符换回原值。
func (e *Engine) Unmask(text string) string {
	if text == "" {
		return text
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	if len(e.reverse) == 0 {
		return text
	}

	out := text
	for ph, raw := range e.reverse {
		if strings.Contains(out, ph) {
			out = strings.ReplaceAll(out, ph, raw)
		}
	}
	return out
}

// UnmaskJSON 与 Unmask 相同，但替换进去的原值会做 JSON 字符串转义。
//
// 为什么需要单独一份：工具调用的参数是 JSON 文本，占位符出现在字符串字面量里。
// 若原值含 `"`、`\`、换行，直接 ReplaceAll 会拼出非法 JSON，客户端解析失败。
// 只用于 JSON 语境；普通正文必须走 Unmask（否则会把引号错误地转义）。
func (e *Engine) UnmaskJSON(text string) string {
	if text == "" {
		return text
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	if len(e.reverse) == 0 {
		return text
	}

	out := text
	for ph, raw := range e.reverse {
		if !strings.Contains(out, ph) {
			continue
		}
		esc, err := json.Marshal(raw)
		if err != nil {
			out = strings.ReplaceAll(out, ph, raw)
			continue
		}
		// json.Marshal 产出带外层引号的字符串，这里只要转义后的内容。
		body := string(esc)
		if len(body) >= 2 {
			body = body[1 : len(body)-1]
		}
		out = strings.ReplaceAll(out, ph, body)
	}
	return out
}

// Records 返回最近的审计记录（新的在前）。
func (e *Engine) Records() []Record {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Record, len(e.records))
	copy(out, e.records)
	return out
}

// Summary 汇总最近 n 天的脱敏情况。
func (e *Engine) Summary(days int) map[string]any {
	e.mu.RLock()
	defer e.mu.RUnlock()

	since := time.Now().AddDate(0, 0, -days)
	var requests, masked int
	byKind := make(map[string]int, 8)
	byProvider := make(map[string]int, 8)
	seen := make(map[string]bool, 64)

	for _, r := range e.records {
		if r.At.Before(since) {
			continue
		}
		if !seen[r.Key] {
			seen[r.Key] = true
			requests++
		}
		for _, h := range r.Hits {
			if h.Action == ActionMask {
				masked++
				byKind[h.Kind]++
			}
		}
		if r.Provider != "" {
			byProvider[r.Provider]++
		}
	}

	return map[string]any{
		"days":       days,
		"requests":   requests,
		"masked":     masked,
		"values":     len(e.reverse),
		"byKind":     byKind,
		"byProvider": byProvider,
		"enabled":    e.enabled,
	}
}

// Clear 清空记录与占位符表。
func (e *Engine) Clear() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.records = nil
	e.forward = make(map[string]string, 64)
	e.reverse = make(map[string]string, 64)
	e.sent = make(map[string][]SentPart, 32)
	e.sentOrder = nil
	e.backs = make(map[string]map[string]int, 32)
}

/* ---------------- 内部 ---------------- */

// placeholderFor 返回某个值对应的占位符，必要时新建。
//
// 占位符形如 {{API_KEY_3f9a2b7c1d4e5f60}}：类型前缀让人一眼看出是什么，
// 64 位加盐哈希保证同值同符、不同值几乎不可能撞符（旧实现的 24 位在
// 4096 个值上就有约 50% 撞车概率，且可离线反推原值）。
func (e *Engine) placeholderFor(value, kind string) string {
	if ph, ok := e.forward[value]; ok {
		return ph
	}
	ph := e.placeholderName(value, kind)
	e.forward[value] = ph
	e.reverse[ph] = value
	e.valueOrder = append(e.valueOrder, value)
	e.evictValuesLocked()
	return ph
}

// evictValuesLocked 在映射超限时按插入顺序淘汰最旧的值（调用方持有写锁）。
//
// 淘汰只影响"回程还能不能换回原文"，不影响"已经发出去的占位符"。
// 这让内存有界，代价是极老的值在回复里出现时换不回来（用户会看到占位符）。
func (e *Engine) evictValuesLocked() {
	if len(e.valueOrder) <= maxValues {
		return
	}
	drop := len(e.valueOrder) - maxValues
	for _, v := range e.valueOrder[:drop] {
		if ph, ok := e.forward[v]; ok {
			delete(e.reverse, ph)
			delete(e.forward, v)
		}
	}
	// 从头切走，避免 slice 无界增长。
	e.valueOrder = append([]string(nil), e.valueOrder[drop:]...)
}

// placeholderName 用进程级盐派生占位符。
//
// 用 HMAC 而不是 sha256(salt|value)：后者有长度扩展攻击面，而 HMAC 零成本。
func (e *Engine) placeholderName(value, kind string) string {
	return placeholderNameWithSalt(e.salt, value, kind)
}

func placeholderNameWithSalt(salt []byte, value, kind string) string {
	mac := hmac.New(sha256.New, salt)
	mac.Write([]byte(kind))
	mac.Write([]byte{0})
	mac.Write([]byte(value))
	sum := mac.Sum(nil)
	return fmt.Sprintf("{{%s_%s}}", strings.ToUpper(kind), hex.EncodeToString(sum[:8]))
}

func (e *Engine) record(r Record) {
	e.records = append(e.records, r)
	if len(e.records) > e.maxRecords {
		e.records = e.records[len(e.records)-e.maxRecords:]
	}
}

// recordKey 给一次请求生成稳定标识：哈希的前 8 字节转十进制，
// 前端用 BigInt 比较新旧，必须是纯数字。
func recordKey(provider, model, text string) string {
	sum := sha256.Sum256([]byte(provider + "\x00" + model + "\x00" + text))
	return strconv.FormatUint(binary.BigEndian.Uint64(sum[:8]), 10)
}

/* ---------------- 内置规则 ---------------- */

func builtinRules() []Rule {
	def := func(id, name, kind string, re string, act Action) Rule {
		return Rule{
			ID: id, Name: name, Kind: kind, Action: act, Enabled: true,
			re: regexp.MustCompile(re),
		}
	}

	return []Rule{
		// 各家云与服务的 API Key
		def("openai_key", "OpenAI / 兼容服务 API Key", "API_KEY", `\bsk-[A-Za-z0-9_\-]{20,}\b`, ActionMask),
		def("openai_proj_key", "OpenAI 项目 Key", "API_KEY", `\bsk-proj-[A-Za-z0-9_\-]{20,}\b`, ActionMask),
		def("anthropic_key", "Anthropic API Key", "API_KEY", `\bsk-ant-[A-Za-z0-9_\-]{20,}\b`, ActionMask),
		def("aws_key", "AWS Access Key ID", "API_KEY", `\b(AKIA|ASIA)[0-9A-Z]{16}\b`, ActionMask),
		def("github_token", "GitHub Token", "API_KEY", `\b(gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,})\b`, ActionMask),
		def("google_key", "Google API Key", "API_KEY", `\bAIza[0-9A-Za-z_\-]{35}\b`, ActionMask),
		def("slack_token", "Slack Token", "API_KEY", `\bxox[baprs]-[0-9A-Za-z\-]{10,}\b`, ActionMask),
		def("jwt", "JWT", "TOKEN", `\beyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\b`, ActionMask),
		// 以下前缀是实际排查中最常漏的几家：它们的 Key 形状与 sk- 家族不重叠，
		// 单靠一条 sk- 正则覆盖不到。
		def("volc_ark_key", "火山方舟 Key", "API_KEY", `\bark-[0-9a-zA-Z]{8,}-[0-9a-zA-Z\-]{8,}\b`, ActionMask),
		def("openrouter_key", "OpenRouter Key", "API_KEY", `\bsk-or-v1-[A-Za-z0-9]{32,}\b`, ActionMask),
		def("stripe_key", "Stripe Key", "API_KEY", `\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,}\b`, ActionMask),
		def("huggingface_key", "Hugging Face Token", "API_KEY", `\bhf_[A-Za-z0-9]{30,}\b`, ActionMask),
		def("groq_key", "Groq API Key", "API_KEY", `\bgsk_[A-Za-z0-9]{40,}\b`, ActionMask),
		def("xai_key", "xAI API Key", "API_KEY", `\bxai-[A-Za-z0-9]{40,}\b`, ActionMask),
		def("replicate_key", "Replicate Token", "API_KEY", `\br8_[A-Za-z0-9]{30,}\b`, ActionMask),

		// 私钥整块
		def("private_key", "私钥文件内容", "PRIVATE_KEY",
			`-----BEGIN (RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY-----[\s\S]{32,}?-----END (RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY-----`,
			ActionMask),

		// 口令类：URL 里内嵌的密码、鉴权头、环境变量 / 键值对、中文写法。
		// 单独成类是因为它们的"值"没有固定形状，必须靠上下文定位；
		// 也正因为靠上下文，误判比密钥类更容易，所以每条都带边界约束。
		grouped(def("url_password", "URL 内嵌密码", "PASSWORD",
			`\b[a-zA-Z][a-zA-Z0-9+.\-]{1,15}://[^\s/:@]{1,64}:([^\s/@]{1,128})@`, ActionMask), 1),
		grouped(def("auth_header", "Authorization 头", "SECRET",
			`(?i:authorization["']?\s*[:=]\s*["']?(?:bearer|basic|token|digest)\s+)([A-Za-z0-9._\-+/=~]{8,})`, ActionMask), 1),
		grouped(def("api_key_header", "API Key 请求头", "SECRET",
			`(?i:(?:x-api-key|x-goog-api-key|api-key|apikey)["']?\s*[:=]\s*["']?)([A-Za-z0-9._\-]{8,})`, ActionMask), 1),
		grouped(def("cookie_header", "Cookie 头", "SECRET",
			`(?i:cookie["']?\s*[:=]\s*["']?)([^\n\r"']{12,512})`, ActionMask), 1),
		// 大写的 *PASSWORD / *SECRET / *TOKEN 赋值。限定"全大写变量名"是为了避开
		// sort_key / cacheToken 这类代码标识符——它们不该被当成密钥脱敏。
		grouped(validated(def("env_secret", "环境变量里的口令", "SECRET",
			`(?m)^\s*(?:export\s+)?([A-Z][A-Z0-9_]*(?:PASSWORD|PASSWD|SECRET|TOKEN|API_?KEY|PRIVATE_KEY))\s*[:=]\s*["']?([^\s"'\\]{6,256})`,
			ActionMask), hasDigit), 2),
		grouped(def("json_secret", "JSON 里的口令字段", "SECRET",
			`(?i)"(?:password|passwd|secret|client_secret|access_token|refresh_token|api_?key)"\s*:\s*"([^"\n]{6,256})"`, ActionMask), 1),
		// 中文写法：密码：xxx / 口令是 xxx / 密钥为 xxx。
		// 值限定"不含空白与中文标点"，避免把整句都吃掉。
		grouped(def("cjk_password", "中文写的口令", "PASSWORD",
			`(?:密码|口令|密钥|秘钥|令牌)\s*(?:是|为|:|：|=)?\s*([A-Za-z0-9!@#$%^&*_\-+=.]{4,64})`, ActionMask), 1),

		// 个人身份
		def("email", "邮箱地址", "EMAIL",
			`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`, ActionMask),
		def("cn_id", "身份证号", "ID",
			`\b[1-9]\d{5}(19|20)\d{2}(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])\d{3}[\dXx]\b`, ActionMask),
		def("cn_phone", "手机号", "PHONE",
			`\b1[3-9]\d{9}\b`, ActionMask),
		def("bank_card", "银行卡号", "CARD",
			`\b(?:4\d{12}(?:\d{3})?|5[1-5]\d{14}|62\d{14,17})\b`, ActionMask),

		// 内网地址：不脱敏，但值得提醒（会暴露内网结构）
		def("private_ip", "内网 IP", "IP",
			`\b(?:10\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}|192\.168\.\d{1,3})\.\d{1,3}\b`,
			ActionWarn),

		// 个人信息细项（设置页逐项开关；默认值见 kindDefaultOff）
		def("us_ssn", "美国社会安全号", "SSN", `\b\d{3}-\d{2}-\d{4}\b`, ActionMask),
		grouped(def("passport", "护照号", "PASSPORT",
			`(?i:(?:passport|护照|パスポート))[^\n\r]{0,16}?\b([A-Z0-9]{6,9})\b`, ActionMask), 1),
		validated(def("iban", "IBAN", "IBAN", `\b[A-Z]{2}\d{2}[A-Z0-9]{11,30}\b`, ActionMask), validIBAN),
		grouped(def("birthday", "生日", "BIRTHDAY",
			`(?i:(?:birthday|dob|born|生日|生年月日))[^\n\r]{0,16}?((?:19|20)\d{2}[-/.]\d{1,2}[-/.]\d{1,2}|\d{1,2}[-/.]\d{1,2}[-/.]\d{4})`, ActionMask), 1),
		def("mac_addr", "MAC 地址", "MAC", `\b[0-9A-Fa-f]{2}(?:[:-][0-9A-Fa-f]{2}){5}\b`, ActionMask),
		grouped(validated(def("serial", "序列号", "SERIAL",
			`(?i:(?:s/n|serial|序列号))[^\n\r]{0,12}?([A-Za-z0-9][A-Za-z0-9\-]{5,19})\b`, ActionMask), hasDigit), 1),
		grouped(def("home_name", "主目录里的用户名", "HOME",
			`(?:/Users/|/home/|[A-Za-z]:\\Users\\)([A-Za-z0-9._\-]+)`, ActionMask), 1),
		grouped(def("bucket", "云存储桶", "BUCKET",
			`\b(?:s3|gs|oss|cos)://([a-z0-9][a-z0-9.\-]{1,61}[a-z0-9])`, ActionMask), 1),
		// user@host（shell 提示符、ssh 命令），以及公网 IPv4。
		// 邮箱在更早、更具体的 EMAIL 规则里已经换掉，这里不会再撞上。
		validated(def("userhost", "用户名和主机名", "USERHOST",
			`\b[A-Za-z0-9._\-]{1,32}@[A-Za-z0-9][A-Za-z0-9._\-]{0,62}\b`, ActionMask), notEmailDomain),
		validated(def("public_ip", "公网 IP 地址", "IP_ADDR",
			`\b\d{1,3}(?:\.\d{1,3}){3}\b`, ActionMask), publicIPv4),
	}
}

// notEmailDomain 让 userhost 规则跳过像邮箱的写法（交给 EMAIL 规则）。
func notEmailDomain(s string) bool {
	at := strings.LastIndexByte(s, '@')
	if at < 0 {
		return false
	}
	return !strings.Contains(s[at+1:], ".")
}

// publicIPv4 判断一个点分四段是不是"公网"地址：
// 私网、回环、链路本地、文档段、CGNAT 与组播一律不算。
func publicIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	oct := make([]int, 4)
	for i, p := range parts {
		if len(p) > 3 {
			return false
		}
		n := 0
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
			n = n*10 + int(r-'0')
		}
		if n > 255 {
			return false
		}
		oct[i] = n
	}
	switch {
	case oct[0] == 0, oct[0] == 10, oct[0] == 127, oct[0] >= 224:
		return false
	case oct[0] == 172 && oct[1] >= 16 && oct[1] <= 31:
		return false
	case oct[0] == 192 && oct[1] == 168:
		return false
	case oct[0] == 169 && oct[1] == 254:
		return false
	case oct[0] == 100 && oct[1] >= 64 && oct[1] <= 127:
		return false
	case oct[0] == 192 && oct[1] == 0 && oct[2] == 2:
		return false
	case oct[0] == 198 && oct[1] == 51 && oct[2] == 100:
		return false
	case oct[0] == 203 && oct[1] == 0 && oct[2] == 113:
		return false
	}
	return true
}

// grouped 让规则只替换第 n 个捕获组。
func grouped(r Rule, n int) Rule { r.group = n; return r }

// validated 给规则加一道命中校验。
func validated(r Rule, fn func(string) bool) Rule { r.validate = fn; return r }

// validIBAN 按 ISO 13616 校验和：转成数字后 mod 97 必须为 1。
func validIBAN(s string) bool {
	s = strings.ToUpper(strings.Join(strings.Fields(s), ""))
	if len(s) < 15 || len(s) > 34 {
		return false
	}
	rearranged := s[4:] + s[:4]
	var sum int
	for _, ch := range rearranged {
		switch {
		case ch >= '0' && ch <= '9':
			sum = (sum*10 + int(ch-'0')) % 97
		case ch >= 'A' && ch <= 'Z':
			v := int(ch-'A') + 10
			sum = (sum*100 + v) % 97
		default:
			return false
		}
	}
	return sum == 1
}

// hasDigit 要求命中里至少有一个数字（"serial number" 这类词不是序列号）。
func hasDigit(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}
