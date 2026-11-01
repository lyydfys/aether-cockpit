package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"regexp"
	"strings"

	"aether-cockpit/internal/core/ir"
)

// 隐私遮蔽（参考 aether 的 privacy filter，为本项目重新实现）。
//
// 工作方式：请求发往上游之前，把文本里的密钥 / 令牌 / 邮箱等敏感串替换成
// 占位符（__AE_MASKED_n__），上游回复里出现占位符时再原样还原。
// 映射表只存在于本次请求的内存里，不落盘、不进日志。
//
// 为什么放在网关层而不是渠道层：遮蔽必须对所有渠道一致生效，
// 而渠道管道是按 Manifest 缓存的，运行时开关需要即时生效，
// 所以在 serve() 的解析之后、调度之前就地处理。

// PrivacyCustomRule 是用户自定义规则：命名正则。
type PrivacyCustomRule struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
}

// PrivacyMaskConfig 是隐私遮蔽的配置（持久化在 data/settings.json）。
type PrivacyMaskConfig struct {
	Enabled bool `json:"enabled"`
	// Rules 内置规则名：keys / jwt / emails / phones / privatekey。
	Rules  []string            `json:"rules,omitempty"`
	Custom []PrivacyCustomRule `json:"custom,omitempty"`
	// Sidecar 可选：外部 PII 模型边车（自带模型，网关只负责驱动）。
	// 配置后边车不可用视为故障 → 请求 503（fail-closed），绝不退回明文。
	Sidecar *SidecarConfig `json:"sidecar,omitempty"`
}

// DefaultPrivacyRules 是 UI 里"恢复默认"用的规则集。
// keys 与 jwt 默认开启：它们一旦泄漏，损失是直接且不可逆的。
var DefaultPrivacyRules = []string{"keys", "jwt", "emails"}

// 内置规则表。顺序即处理顺序：越具体的越靠前，
// 避免 "-----BEGIN" 这类长块被短规则先咬掉一角。
var builtinPrivacyPatterns = []struct {
	name    string
	pattern string
}{
	{"privatekey", `-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`},
	{"jwt", `eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{8,}`},
	{"keys", `sk-ant-[A-Za-z0-9_-]{20,}`},
	{"keys", `sk-proj-[A-Za-z0-9_-]{20,}`},
	{"keys", `sk-[A-Za-z0-9_-]{20,}`},
	{"keys", `github_pat_[A-Za-z0-9_]{20,}`},
	{"keys", `gh[pousr]_[A-Za-z0-9]{36}`},
	{"keys", `glpat-[A-Za-z0-9_-]{20,}`},
	{"keys", `xox[bpars]-[A-Za-z0-9-]{10,}`},
	{"keys", `AKIA[0-9A-Z]{16}`},
	{"keys", `AIza[0-9A-Za-z_-]{35}`},
	{"keys", `gsk_[A-Za-z0-9]{20,}`},
	{"keys", `hf_[A-Za-z0-9]{30,}`},
	{"emails", `[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`},
	{"phones", `(?:\+?86[- ]?)1[3-9]\d{9}`},
}

// privacyEngine 是按配置编译好的规则集。
type privacyEngine struct {
	rules []privacyRule
	// key 让占位符由敏感串确定性派生（同一 secret 恒得同一占位符）。
	// 进程启动时随机生成：稳定性只需覆盖一次运行期，重启后换新值不影响正确性。
	key []byte
	// placeholderLen 是占位符的固定长度，流式还原时用它决定回退保留多少字节。
	placeholderLen int
	// sidecar 是可选的 PII 模型边车；nil 表示只用正则。
	sidecar *sidecarClient
}

type privacyRule struct {
	name string
	re   *regexp.Regexp
}

const placeholderPrefix = "__AE_MASKED_"
const placeholderSuffix = "__"

// placeholderTagLen 是派生标签的 base32 长度，决定占位符总长固定为 22。
const placeholderTagLen = 8

// NewPrivacyEngine 编译规则集。
//
// 失败即上游不发送：一旦开关打开，任何一条规则编译不出来都视为配置错误，
// 而不是"跳过这条继续跑"——静默跳过等于把用户以为已经遮蔽的内容明文发出去。
func newPrivacyEngine(cfg PrivacyMaskConfig) (*privacyEngine, error) {
	e := &privacyEngine{placeholderLen: len(placeholderPrefix) + placeholderTagLen + len(placeholderSuffix)}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	e.key = key

	want := make(map[string]bool, len(cfg.Rules))
	for _, r := range cfg.Rules {
		want[strings.TrimSpace(r)] = true
	}
	for _, b := range builtinPrivacyPatterns {
		if len(cfg.Rules) > 0 && !want[b.name] {
			continue
		}
		re, err := regexp.Compile(b.pattern)
		if err != nil {
			return nil, &privacyConfigError{msg: "内置规则 " + b.name + " 编译失败: " + err.Error()}
		}
		e.rules = append(e.rules, privacyRule{name: b.name, re: re})
	}
	for _, c := range cfg.Custom {
		if strings.TrimSpace(c.Pattern) == "" {
			continue
		}
		re, err := regexp.Compile(c.Pattern)
		if err != nil {
			return nil, &privacyConfigError{msg: "自定义规则 " + c.Name + " 不是合法正则: " + err.Error()}
		}
		e.rules = append(e.rules, privacyRule{name: "custom:" + c.Name, re: re})
	}
	if len(e.rules) == 0 {
		return nil, &privacyConfigError{msg: "已启用隐私遮蔽但没有任何可编译的规则"}
	}
	if cfg.Sidecar != nil && sidecarOn(cfg.Sidecar) {
		sc, err := newSidecarClient(*cfg.Sidecar)
		if err != nil {
			return nil, &privacyConfigError{msg: "PII 边车启动失败: " + err.Error()}
		}
		e.sidecar = sc
	}
	return e, nil
}

// privacyConfigError 标记配置错误，便于上层区分"配置写错"与"运行时故障"。
type privacyConfigError struct{ msg string }

func (e *privacyConfigError) Error() string { return e.msg }

// tagFor 由敏感串确定性派生固定长度标签。
//
// 确定性有两个实际收益：同一 secret 在多轮对话里恒得同一占位符（上游重新
// 计算 prompt cache 时能命中）；不同请求之间也保持稳定，不会每次换标签导致
// 前缀缓存整段失效。
func (e *privacyEngine) tagFor(secret string) string {
	mac := hmac.New(sha256.New, e.key)
	mac.Write([]byte(secret))
	sum := mac.Sum(nil)
	tag := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:5])
	return placeholderPrefix + tag + placeholderSuffix
}

// maskState 是单个请求内的双向映射：secret -> 占位符，占位符 -> secret。
// 双表让遮蔽侧 O(1)，还原侧按占位符直接查。
type maskState struct {
	byPH     map[string]string
	bySecret map[string]string
}

func newMaskState() *maskState {
	return &maskState{byPH: make(map[string]string, 8), bySecret: make(map[string]string, 8)}
}

// maskInto 对一段文本做遮蔽。state 由整个请求共享，
// 保证同一敏感串在任何位置都映射到同一个占位符。
func (e *privacyEngine) maskInto(text string, state *maskState) string {
	if text == "" || len(e.rules) == 0 {
		return text
	}
	for _, rule := range e.rules {
		if !rule.re.MatchString(text) {
			continue
		}
		text = rule.re.ReplaceAllStringFunc(text, func(match string) string {
			if ph, ok := state.bySecret[match]; ok {
				return ph
			}
			ph := e.tagFor(match)
			state.bySecret[match] = ph
			state.byPH[ph] = match
			return ph
		})
	}
	return text
}

// maskRequest 就地遮蔽请求里所有自由文本字段，返回流式还原器（无可遮蔽内容时返回 nil）。
//
// 覆盖范围：系统指令、消息正文、工具调用参数、工具执行结果、附件文件名。
// 不覆盖附件 URL 与 base64 正文——遮蔽它们会让上游取不到资源（URL 变成假地址、
// 图片二进制被改写），而这属于"结构化载荷"而非用户键入的文本；设置页对此有明示。
func (e *privacyEngine) maskRequest(req *ir.UnifiedChatRequest) (*privacyUnmasker, error) {
	state := newMaskState()
	mask := func(s string) string { return e.maskInto(s, state) }

	// 先过 PII 边车（语义实体），再跑正则（密钥/邮箱等确定性模式）：
	// 反过来会让边车看到一堆 __AE_MASKED_ 占位符，容易误判。
	if e.sidecar != nil {
		ctx, cancel := context.WithTimeout(context.Background(), e.sidecar.timeout())
		defer cancel()
		apply := func(s string) (string, error) {
			out, mapping, err := e.sidecar.Mask(ctx, s)
			if err != nil {
				return s, err
			}
			for ph, orig := range mapping {
				if ph == "" {
					continue
				}
				state.byPH[ph] = orig
			}
			return out, nil
		}
		var err error
		if req.Instructions, err = apply(req.Instructions); err != nil {
			return nil, err
		}
		for i := range req.Items {
			if req.Items[i].Text, err = apply(req.Items[i].Text); err != nil {
				return nil, err
			}
			if req.Items[i].Arguments, err = apply(req.Items[i].Arguments); err != nil {
				return nil, err
			}
			if req.Items[i].Output, err = apply(req.Items[i].Output); err != nil {
				return nil, err
			}
		}
	}

	req.Instructions = mask(req.Instructions)
	for i := range req.Items {
		req.Items[i].Text = mask(req.Items[i].Text)
		req.Items[i].Arguments = mask(req.Items[i].Arguments)
		req.Items[i].Output = mask(req.Items[i].Output)
		for j := range req.Items[i].Attachments {
			req.Items[i].Attachments[j].FileName = mask(req.Items[i].Attachments[j].FileName)
		}
	}

	if len(state.byPH) == 0 {
		return nil, nil
	}
	hold := e.placeholderLen
	for ph := range state.byPH {
		if len(ph) > hold {
			hold = len(ph)
		}
	}
	return newPrivacyUnmasker(state.byPH, hold), nil
}

// privacyUnmasker 把回复流里的占位符还原成原文。
//
// 难点是占位符可能被切成多个 chunk（例如 "__AE_MA" + "SKED_1__"）。
// 做法：保留缓冲区末尾 hold 字节不立即下发，凑齐或流结束时再处理。
// 代价是每个 delta 延迟 hold 字节的下发，对体感无影响。
type privacyUnmasker struct {
	mapping map[string]string
	hold    int
	buf     string
}

func newPrivacyUnmasker(mapping map[string]string, hold int) *privacyUnmasker {
	if hold < 1 {
		hold = 1
	}
	return &privacyUnmasker{mapping: mapping, hold: hold}
}

// push 处理一个增量，返回可安全下发的部分。
func (u *privacyUnmasker) push(delta string) string {
	u.buf += delta
	var out strings.Builder
	for {
		best := -1
		bestPh := ""
		for ph := range u.mapping {
			if i := strings.Index(u.buf, ph); i >= 0 && (best < 0 || i < best) {
				best = i
				bestPh = ph
			}
		}
		if best < 0 {
			break
		}
		out.WriteString(u.buf[:best])
		out.WriteString(u.mapping[bestPh])
		u.buf = u.buf[best+len(bestPh):]
	}
	if len(u.buf) > u.hold {
		cut := len(u.buf) - u.hold
		out.WriteString(u.buf[:cut])
		u.buf = u.buf[cut:]
	}
	return out.String()
}

// flush 流结束时调用：交还剩余缓冲，并还原其中完整的占位符。
// 残余的半截占位符按原样下发（它不包含秘密，只是那一次还原会失配）。
func (u *privacyUnmasker) flush() string {
	out := u.buf
	u.buf = ""
	for ph, sec := range u.mapping {
		out = strings.ReplaceAll(out, ph, sec)
	}
	return out
}

// wrapStreams 给上游事件流加还原层，直到流结束。
// 终止事件（done / error）之前先冲刷残余缓冲。
func (u *privacyUnmasker) wrapStreams(ctx context.Context, in <-chan ir.UnifiedStreamEvent) <-chan ir.UnifiedStreamEvent {
	out := make(chan ir.UnifiedStreamEvent, 8)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-in:
				if !ok {
					return
				}
				switch ev.Type {
				case ir.EventTextDelta, ir.EventReasoningDelta:
					ev.TextDelta = u.push(ev.TextDelta)
				case ir.EventToolCallStart, ir.EventToolCallDelta:
					ev.ArgsDelta = u.push(ev.ArgsDelta)
				case ir.EventDone, ir.EventError:
					if rest := u.flush(); rest != "" {
						tail := ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: rest}
						select {
						case out <- tail:
						case <-ctx.Done():
							return
						}
					}
				}
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}
