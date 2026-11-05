package gateway

// 意图路由（对标 aether internal/gateway/classify.go，设计依据
// docs/aether_DEEP_ANALYSIS.md 第 2 节）。
//
// 虚拟组里的渠道往往"强弱分明"——一个代码能力强、一个便宜但弱。priority
// 只能按静态顺序 fallback；smart 策略在每次请求前用一个小分类模型读一遍
// 用户提问，把"最擅长这类问题"的渠道提到链首，其余渠道保持相对顺序继续
// 充当 fallback 链。
//
// 分类请求经网关自身的 loopback HTTP（/v1/chat/completions）发出，完整
// 复用路由 / 渠道 / 用量 / 日志链路：分类模型用了多少 token，在用量面板里
// 与普通请求一样可见，不需要单独记账。
//
// 防抖设计全部学自 aether classify.go：
//   - SHA-256 缓存（TTL 10 分钟）——同一提问短时间内不重复分类；
//   - inflight 去重——并发相同提问共享一次分类调用；
//   - 失败 30 秒冷却——分类模型抖动时不让每个请求都白等一次超时；
//   - 短追问继承——"继续"这类追问单独分类没有信息量，直接沿用上一轮意图；
//   - 防注入剥离 + 头 3000 / 尾 1000 rune 截断——用户上下文里常带有
//     <system-reminder> 之类的注入块，会把分类器带偏。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/ir"
)

const (
	// intentHeader 标记"本次请求来自网关自身的意图分类调用"。
	//
	// 防递归是意图路由的生命线：分类模型本身往往就配在某个虚拟组里，
	// 它的请求同样会走 dispatch。如果不加标记，"分类请求 → smart 策略 →
	// 再发起一次分类"会无限套娃，直到把网关自己打挂。dispatch 收到带
	// 该头的请求时 RouteContext.Intent 传 nil，smart 退化为 priority。
	intentHeader = "X-Aether-Intent"

	// intentCacheTTL 分类结果缓存时长（aether 同款：10 分钟）。
	// 同一会话里反复出现相同提问很常见（客户端重试、多窗口同问），缓存
	// 既省一次分类调用的延迟，也省分类模型的用量。
	intentCacheTTL = 10 * time.Minute

	// intentCacheLimit 缓存条目上限。超限先清过期，仍超则随机丢一条。
	// 随机丢而不是 LRU：分类缓存是纯优化，丢谁都不影响正确性，
	// 没必要为它维护访问序。
	intentCacheLimit = 4096

	// intentCooldown 分类失败后该组的冷却时长。冷却的是"尝试"而不是
	// "结果"：30 秒内该组直接按原序路由，绝不为了分类多等一次超时。
	intentCooldown = 30 * time.Second

	// intentDefaultTimeout 单次分类的默认超时（cfg.IntentTimeoutSeconds
	// 的 0 值兜底）。分类发生在首字节之前，超时必须远小于上游本身的
	// 首字节预算，否则一次分类抖动会拖垮整条请求。
	intentDefaultTimeout = 8 * time.Second

	// intentShortRunes 短追问阈值（rune 数）。少于该长度且本会话已有
	// 上一轮意图时直接继承——"继续""换个说法再讲一遍"这类追问脱离
	// 上下文毫无分类价值，硬分类只会得到噪声序号。
	intentShortRunes = 24

	// intentPromptHead / intentPromptTail 防注入后的文本截断
	// （aether 同款：头 3000 + 尾 1000 rune）。分类只需要知道"这段话
	// 在问什么"，不是全文精读；同时限制了分类请求的 token 成本上限。
	intentPromptHead = 3000
	intentPromptTail = 1000

	// intentTruncateMark 截断处的省略标记。
	intentTruncateMark = "\n…（中间内容已省略）…\n"
)

// IntentClassifyFunc 意图分类函数，由网关注入 ModelRouter（与 QuotaLookup
// 同一套 atomic.Pointer 模式）。输入虚拟组名、跨轮稳定的会话指纹、候选链
// 与用户文本，返回应提前到链首的候选下标：
//
//	0  = 维持原序（无意图 / 分类认为首位最合适）
//	>0 = 把 seats[i] 移到最前，其余保持相对顺序
//	-1 = 分类失败（维持原序）
type IntentClassifyFunc func(group, sessionKey string, seats []modelBinding, text string) int

// intentHTTPClient 分类自调用专用 HTTP 客户端。
//
// 刻意不用网关的 transport.Factory（那里带全局代理）：目标是网关自身的
// 127.0.0.1 回环，走代理既绕出本机又多一跳故障点。超时由每次调用的
// ctx 控制，这里不设 Client.Timeout。
var intentHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 nil, // 回环自调用绝不经过代理
		MaxIdleConns:          4,
		IdleConnTimeout:       90 * time.Second,
		DisableCompression:    true,
		ResponseHeaderTimeout: 0, // 由 ctx 超时统一约束
	},
}

// intentClassifier 意图分类器。所有状态都挂在 Gateway 上、随网关存活；
// 分类结果对正确性无影响（失败=原序），因此不做持久化。
type intentClassifier struct {
	g *Gateway

	// endpoint 分类端点完整 URL。为空时按网关监听地址推导本机回环端点；
	// 测试用 httptest 覆盖。
	endpoint string

	mu       sync.Mutex
	cache    map[string]intentCacheEntry // key = sha256(classifyModel|group|text)
	inflight map[string]*intentCall      // key 同 cache，进行中的分类调用
	lastHit  map[string]intentMemory     // key = group|sessionKey，上一轮命中的渠道
	cooldown map[string]time.Time        // group -> 冷却截止时刻
}

// intentCacheEntry 缓存的分类结果。
type intentCacheEntry struct {
	idx       int
	expiresAt time.Time
}

// intentCall 一次进行中的分类调用。并发同 key 的请求共享同一个 done。
type intentCall struct {
	done chan struct{}
	idx  int
}

// intentMemory 某会话上一轮意图命中的渠道（按渠道 ID 记忆而不是下标，
// 因为两次 resolve 之间的候选链可能已经变化）。
type intentMemory struct {
	providerID string
	expiresAt  time.Time
}

// newIntentClassifier 创建分类器。未配置 IntentClassifyModel 时它仍然
// 存在，只是 classify 恒返回 0——配置与否不影响网关其它行为。
func newIntentClassifier(g *Gateway) *intentClassifier {
	return &intentClassifier{
		g:        g,
		cache:    make(map[string]intentCacheEntry, 64),
		inflight: make(map[string]*intentCall, 8),
		lastHit:  make(map[string]intentMemory, 64),
		cooldown: make(map[string]time.Time, 4),
	}
}

// classify 实现 IntentClassifyFunc。所有失败路径都收敛到"维持原序"：
// 分类是锦上添花的优化，绝不能成为请求的可用性风险。
func (c *intentClassifier) classify(group, sessionKey string, seats []modelBinding, text string) int {
	classifyModel := strings.TrimSpace(c.g.cfg.IntentClassifyModel)
	if classifyModel == "" || len(seats) < 2 {
		// 未配置分类模型（smart 退化为 priority）或组内只有一个渠道，
		// 没有可重排的东西。
		return 0
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}

	// 短追问继承：极短的追问单独分类没有信息量，直接沿用本会话上一轮
	// 意图。继承失败（新会话 / 渠道下线）时继续走正常分类，不会更糟。
	runes := []rune(trimmed)
	if len(runes) < intentShortRunes && sessionKey != "" {
		if idx, ok := c.rememberedIndex(group, sessionKey, seats); ok {
			return idx
		}
	}

	clean := truncateIntentPrompt(sanitizeIntentText(trimmed))
	key := intentCacheKey(classifyModel, group, clean)

	if idx, ok := c.cachedIndex(key, len(seats)); ok {
		// 缓存命中同样算"本轮意图"，让后续短追问能继承。
		c.remember(group, sessionKey, seats, idx)
		return idx
	}

	// 失败冷却期内不再尝试：返回 -1（维持原序），避免每个请求都
	// 白等一次注定失败的超时。
	if c.inCooldown(group) {
		return -1
	}

	timeout := c.classifyTimeout()
	call, leader := c.beginCall(key)
	if !leader {
		// 并发同 key：共享 leader 那一次调用，不重复发 HTTP。
		return c.waitCall(call, timeout)
	}

	idx, err := c.doClassify(classifyModel, group, seats, clean, timeout)
	if err != nil {
		log.Printf("[WARN] 意图分类失败（组 %s）：%v，%v 内该组不再尝试分类", group, err, intentCooldown)
		c.markCooldown(group)
		idx = -1
	} else if idx < 0 || idx >= len(seats) {
		// 模型回复了越界序号或无法解析的内容，与请求失败同样处理。
		log.Printf("[WARN] 意图分类输出无效（组 %s）：%q", group, strconv.Itoa(idx))
		c.markCooldown(group)
		idx = -1
	} else {
		c.storeCache(key, idx)
		c.remember(group, sessionKey, seats, idx)
		c.clearCooldown(group)
	}
	c.finishCall(key, idx)
	return idx
}

// classifyTimeout 单次分类超时。配置为 0 或负数时用默认 8 秒。
func (c *intentClassifier) classifyTimeout() time.Duration {
	if s := c.g.cfg.IntentTimeoutSeconds; s > 0 {
		return time.Duration(s) * time.Second
	}
	return intentDefaultTimeout
}

// doClassify 经网关自身 loopback 发起一次分类请求。
//
// 请求带 Authorization（复用下游客户端的鉴权）与 X-Aether-Intent 防递归
// 头，走完整的 /v1/chat/completions 链路——路由、渠道、用量统计一视同仁。
func (c *intentClassifier) doClassify(classifyModel, group string, seats []modelBinding, clean string, timeout time.Duration) (int, error) {
	url := c.endpoint
	if url == "" {
		url = intentLoopbackURL(c.g.PublicAddr())
		if url == "" {
			return -1, fmt.Errorf("无法从监听地址 %q 推导自调用端点", c.g.PublicAddr())
		}
	}

	// 组内渠道清单：每行 "序号. 渠道ID - 描述"，与候选链下标严格一致。
	var sb strings.Builder
	for i, b := range seats {
		desc := b.DisplayName
		if desc == "" {
			desc = b.UpstreamModel
		}
		fmt.Fprintf(&sb, "%d. %s - %s\n", i, b.ProviderID, desc)
	}

	payload := map[string]any{
		"model": classifyModel,
		"messages": []map[string]string{
			{"role": roleSystem, "content": "你是意图分类器。只回复一个整数序号。"},
			{"role": roleUser, "content": sb.String() + "\n用户提问：" + clean},
		},
		"max_tokens":  8,
		"temperature": 0,
		"stream":      false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return -1, err
	}

	token := c.g.cfg.APIKey
	if token == "" {
		token = "aether-local"
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return -1, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	// 防递归标记：dispatch 看到该头就不会对这条请求再做分类。
	req.Header.Set(intentHeader, "1")

	resp, err := intentHTTPClient.Do(req)
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return -1, fmt.Errorf("分类端点返回 %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return -1, err
	}
	var wire struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return -1, err
	}
	if len(wire.Choices) == 0 {
		return -1, errors.New("分类端点没有返回候选")
	}
	return parseIntentIndex(wire.Choices[0].Message.Content), nil
}

const (
	roleSystem = "system"
	roleUser   = "user"
)

// parseIntentIndex 从模型回复里抠出第一个整数。容错 "1"、"#2"、"3. 因为"
// 这类拼法；带负号视为无效；解析不出返回 -1（调用方按失败处理）。
func parseIntentIndex(s string) int {
	runes := []rune(s)
	start := -1
	for i, r := range runes {
		if r >= '0' && r <= '9' {
			start = i
			break
		}
	}
	if start < 0 {
		return -1
	}
	// 紧贴数字的负号说明模型在表达"无效/拒绝"，尊重它。
	if start > 0 && runes[start-1] == '-' {
		return -1
	}
	end := start
	for end < len(runes) && runes[end] >= '0' && runes[end] <= '9' {
		end++
	}
	n, err := strconv.Atoi(string(runes[start:end]))
	if err != nil {
		return -1
	}
	return n
}

// intentLoopbackURL 从网关监听地址推导自调用端点。host 强制 127.0.0.1：
// 这是进程内的自调用，即使网关对外监听 0.0.0.0 也不该绕道外部网卡。
// 推导不出端口（配置残缺）时返回空串，由调用方按失败处理。
func intentLoopbackURL(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return ""
	}
	return "http://127.0.0.1:" + port + "/v1/chat/completions"
}

// sanitizeIntentText 剥离注入块：按行扫描，丢弃以 <system-reminder> 开始、
// 到 </system-reminder> 结束的行段。用户上下文（尤其来自 Claude Code 的
// 请求）里大量出现此类块，内容与"用户在问什么"无关，却可能诱导分类器
// 输出错误序号——剥离后分类只看真实提问。
func sanitizeIntentText(text string) string {
	if !strings.Contains(text, "<system-reminder>") {
		return text
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	dropping := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !dropping && strings.HasPrefix(trimmed, "<system-reminder>") {
			dropping = true
			continue
		}
		if dropping {
			// 结束标记允许出现在行中任何位置（宽松匹配，防止
			// 结束行带了前后缀导致整段剥不到头）。
			if strings.Contains(trimmed, "</system-reminder>") {
				dropping = false
			}
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// truncateIntentPrompt 截断到头 3000 + 尾 1000 rune。只保留头尾是因为
// "在问什么"几乎总在开头或结尾（中间多是粘贴的长文档/代码）。
func truncateIntentPrompt(text string) string {
	runes := []rune(text)
	if len(runes) <= intentPromptHead+intentPromptTail {
		return text
	}
	return string(runes[:intentPromptHead]) + intentTruncateMark + string(runes[len(runes)-intentPromptTail:])
}

// intentCacheKey 缓存键：SHA-256(classifyModel|group|text)。
// 把分类模型名编进 key：换了分类模型，旧缓存不适用。
func intentCacheKey(model, group, clean string) string {
	sum := sha256.Sum256([]byte(model + "|" + group + "|" + clean))
	return hex.EncodeToString(sum[:])
}

// cachedIndex 查缓存。命中但下标越界（渠道数变化）视为未命中。
func (c *intentClassifier) cachedIndex(key string, n int) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[key]
	if !ok {
		return 0, false
	}
	if time.Now().After(e.expiresAt) {
		delete(c.cache, key)
		return 0, false
	}
	if e.idx < 0 || e.idx >= n {
		return 0, false
	}
	return e.idx, true
}

// storeCache 写缓存。超限时先清过期条目，仍超则随机丢一条：
// map 遍历序随机，取第一条删除即是随机丢——缓存是纯优化，不值得为它
// 维护 LRU 链表。
func (c *intentClassifier) storeCache(key string, idx int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cache) >= intentCacheLimit {
		now := time.Now()
		for k, e := range c.cache {
			if now.After(e.expiresAt) {
				delete(c.cache, k)
			}
		}
		for len(c.cache) >= intentCacheLimit {
			for k := range c.cache {
				delete(c.cache, k)
				break
			}
		}
	}
	c.cache[key] = intentCacheEntry{idx: idx, expiresAt: time.Now().Add(intentCacheTTL)}
}

// beginCall 登记一次分类调用。返回 (call, false) 表示已有并发调用
// 在进行中，应等待它的结果；(nil, true) 表示本 goroutine 赢得发起权。
func (c *intentClassifier) beginCall(key string) (*intentCall, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if call, ok := c.inflight[key]; ok {
		return call, false
	}
	c.inflight[key] = &intentCall{done: make(chan struct{})}
	return nil, true
}

// finishCall 落盘结果并唤醒所有等待者。必须在 leader 的所有结果
// 写入之后调用；idx 为 -1 时等待者同样按失败处理。
func (c *intentClassifier) finishCall(key string, idx int) {
	c.mu.Lock()
	call, ok := c.inflight[key]
	if ok {
		call.idx = idx
		delete(c.inflight, key)
	}
	c.mu.Unlock()
	if ok {
		close(call.done)
	}
}

// waitCall 等待并发的同 key 分类调用。leader 自带超时，这里再加一层
// 同样的兜底：万一 leader goroutine 异常消亡，等待者不至于永久挂死。
func (c *intentClassifier) waitCall(call *intentCall, timeout time.Duration) int {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-call.done:
		return call.idx
	case <-timer.C:
		return -1
	}
}

// remember 记录某会话本轮意图命中的渠道，供后续短追问继承。
// key = group|sessionKey；sessionKey 为空（拿不到会话指纹）时不记录。
func (c *intentClassifier) remember(group, sessionKey string, seats []modelBinding, idx int) {
	if sessionKey == "" || idx < 0 || idx >= len(seats) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.lastHit) >= intentCacheLimit {
		now := time.Now()
		for k, m := range c.lastHit {
			if now.After(m.expiresAt) {
				delete(c.lastHit, k)
			}
		}
	}
	c.lastHit[group+"|"+sessionKey] = intentMemory{
		providerID: seats[idx].ProviderID,
		expiresAt:  time.Now().Add(intentCacheTTL),
	}
}

// rememberedIndex 短追问继承：查该会话上一轮命中的渠道是否还在候选链里。
// 按渠道 ID 而不是下标匹配，兼容两次 resolve 之间候选链的变化。
// 未命中（新会话 / 已过期 / 渠道下线）返回 false，调用方退回正常分类。
func (c *intentClassifier) rememberedIndex(group, sessionKey string, seats []modelBinding) (int, bool) {
	c.mu.Lock()
	m, ok := c.lastHit[group+"|"+sessionKey]
	c.mu.Unlock()
	if !ok || time.Now().After(m.expiresAt) {
		return 0, false
	}
	for i, b := range seats {
		if b.ProviderID == m.providerID {
			return i, true
		}
	}
	return 0, false
}

func (c *intentClassifier) inCooldown(group string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	until, ok := c.cooldown[group]
	return ok && time.Now().Before(until)
}

func (c *intentClassifier) markCooldown(group string) {
	c.mu.Lock()
	c.cooldown[group] = time.Now().Add(intentCooldown)
	c.mu.Unlock()
}

func (c *intentClassifier) clearCooldown(group string) {
	c.mu.Lock()
	delete(c.cooldown, group)
	c.mu.Unlock()
}

// conversationFingerprint 计算跨轮稳定的会话指纹：首条用户文本的哈希。
//
// 刻意不用 pool.SessionFingerprint——它把最后一轮文本也混进了哈希，
// 每一轮提问都会变，做不了"同一会话"的继承键。这里只取首条用户文本：
// 客户端带全量历史的协议（OpenAI Chat / Anthropic Messages）里它跨轮稳定。
// Responses 协议只带增量输入时指纹每轮变化，继承自然落空、退回正常分类，
// 属于可接受的 fail-open。
func conversationFingerprint(req *ir.UnifiedChatRequest) string {
	for _, it := range req.Items {
		if it.IsUserTurn() && strings.TrimSpace(it.Text) != "" {
			sum := sha256.Sum256([]byte(it.Text))
			return hex.EncodeToString(sum[:])[:16]
		}
	}
	return ""
}
