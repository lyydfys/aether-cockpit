package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
)

// ---- 测试基础设施 ----

// intentStub 模拟网关自身的 /v1/chat/completions 端点，
// 记录收到的请求并按预置内容回复。
type intentStub struct {
	*httptest.Server

	mu       sync.Mutex
	calls    int
	lastAuth string
	lastMark string // X-Aether-Intent 头
	lastBody []byte
	content  string // 回复的 OpenAI Chat JSON
	status   int
	hold     chan struct{} // 非 nil 时挂住请求直到关闭（测超时 / inflight）
}

func newIntentStub(t *testing.T, content string) *intentStub {
	t.Helper()
	st := &intentStub{content: content, status: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		st.mu.Lock()
		st.calls++
		st.lastAuth = r.Header.Get("Authorization")
		st.lastMark = r.Header.Get(intentHeader)
		st.lastBody = body
		status, content, hold := st.status, st.content, st.hold
		st.mu.Unlock()
		if hold != nil {
			<-hold
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(content))
	})
	st.Server = httptest.NewServer(mux)
	// 先注册 Server.Close，后注册的 hold 清理先执行（LIFO），
	// 保证挂住的 handler 在 Server.Close 之前被释放。
	t.Cleanup(st.Close)
	return st
}

func (st *intentStub) count() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.calls
}

func (st *intentStub) reply(content string) {
	st.mu.Lock()
	st.content = content
	st.mu.Unlock()
}

func (st *intentStub) fail(status int) {
	st.mu.Lock()
	st.status = status
	st.mu.Unlock()
}

// newTestClassifier 构造只依赖 cfg 的分类器（不经过 New() 的完整装配），
// endpoint 指向 httptest 桩。
func newTestClassifier(t *testing.T, cfg Config, st *intentStub) *intentClassifier {
	t.Helper()
	c := newIntentClassifier(&Gateway{cfg: cfg})
	c.endpoint = st.URL + "/v1/chat/completions"
	return c
}

var intentSeats = []modelBinding{
	{ProviderID: "p-strong", DisplayName: "强渠道"},
	{ProviderID: "p-cheap", DisplayName: "便宜渠道"},
	{ProviderID: "p-fallback", DisplayName: "兜底渠道"},
}

const longQuestion = "请详细解释一下 Go 语言里 channel 的关闭语义，以及用 for range 遍历时需要注意什么"

func chatReply(content string) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{
			{"message": map[string]string{"role": "assistant", "content": content}},
		},
	})
	return string(b)
}

// ---- 核心行为 ----

// TestIntentClassifyBasic 命中 "1" 时返回下标 1，且请求形态符合契约：
// 带防递归标记头与鉴权头，body 里是组内渠道清单与提问文本。
func TestIntentClassifyBasic(t *testing.T) {
	st := newIntentStub(t, chatReply("1"))
	c := newTestClassifier(t, Config{IntentClassifyModel: "test/clf"}, st)

	got := c.classify("auto/free", "sess-1", intentSeats, longQuestion)
	if got != 1 {
		t.Fatalf("classify = %d, want 1", got)
	}
	if st.count() != 1 {
		t.Fatalf("HTTP 调用次数 = %d, want 1", st.count())
	}
	if st.lastMark != "1" {
		t.Errorf("X-Aether-Intent 头 = %q, want \"1\"（防递归标记缺失会死循环）", st.lastMark)
	}
	if st.lastAuth != "Bearer aether-local" {
		t.Errorf("未配置 APIKey 时应带 aether-local 兜底鉴权，实际 %q", st.lastAuth)
	}
	var wire struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		MaxTokens int     `json:"max_tokens"`
		Temp      float64 `json:"temperature"`
		Stream    bool    `json:"stream"`
	}
	if err := json.Unmarshal(st.lastBody, &wire); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v", err)
	}
	if wire.Model != "test/clf" {
		t.Errorf("model = %q, want test/clf", wire.Model)
	}
	if len(wire.Messages) != 2 || wire.Messages[0].Role != "system" || wire.Messages[1].Role != "user" {
		t.Fatalf("messages 形态错误: %+v", wire.Messages)
	}
	if !strings.Contains(wire.Messages[1].Content, "0. p-strong - 强渠道") ||
		!strings.Contains(wire.Messages[1].Content, "1. p-cheap - 便宜渠道") {
		t.Errorf("渠道清单缺失: %q", wire.Messages[1].Content)
	}
	if !strings.Contains(wire.Messages[1].Content, longQuestion) {
		t.Errorf("提问文本缺失: %q", wire.Messages[1].Content)
	}
}

func TestIntentClassifyUsesConfiguredAPIKey(t *testing.T) {
	st := newIntentStub(t, chatReply("0"))
	c := newTestClassifier(t, Config{IntentClassifyModel: "test/clf", APIKey: "sk-real"}, st)
	if got := c.classify("auto/free", "", intentSeats, longQuestion); got != 0 {
		t.Fatalf("classify = %d, want 0", got)
	}
	if st.lastAuth != "Bearer sk-real" {
		t.Errorf("Authorization = %q, want Bearer sk-real", st.lastAuth)
	}
}

// 未配置分类模型 / 组内单渠道 / 空文本：直接原序返回，一个 HTTP 都不发。
func TestIntentClassifyDegrades(t *testing.T) {
	st := newIntentStub(t, chatReply("1"))

	c := newTestClassifier(t, Config{}, st) // 未配置模型
	if got := c.classify("auto/free", "s", intentSeats, longQuestion); got != 0 {
		t.Errorf("未配置模型应返回 0，实际 %d", got)
	}
	if st.count() != 0 {
		t.Errorf("未配置模型不应发 HTTP")
	}

	c2 := newTestClassifier(t, Config{IntentClassifyModel: "test/clf"}, st)
	if got := c2.classify("auto/free", "s", intentSeats[:1], longQuestion); got != 0 {
		t.Errorf("单渠道应返回 0，实际 %d", got)
	}
	if got := c2.classify("auto/free", "s", intentSeats, "   "); got != 0 {
		t.Errorf("空文本应返回 0，实际 %d", got)
	}
	if st.count() != 0 {
		t.Errorf("降级路径不应发 HTTP，实际 %d 次", st.count())
	}
}

// TestIntentClassifyReplies "0" 是合法回复（保持原序）；垃圾与越界是失败（-1）。
func TestIntentClassifyReplies(t *testing.T) {
	cases := []struct {
		reply string
		want  int
	}{
		{"0", 0},
		{" 2 ", 2},
		{"2. 因为兜底渠道最便宜", 2},
		{"#1", 1},
		{"-1", -1},
		{"看不出来", -1},
		{"9", -1}, // 越界
	}
	for _, tc := range cases {
		st := newIntentStub(t, chatReply(tc.reply))
		c := newTestClassifier(t, Config{IntentClassifyModel: "test/clf"}, st)
		got := c.classify("auto/free", "", intentSeats, longQuestion)
		if got != tc.want {
			t.Errorf("reply %q: classify = %d, want %d", tc.reply, got, tc.want)
		}
	}
}

// TestIntentClassifyCache 同一文本第二次不再发 HTTP。
func TestIntentClassifyCache(t *testing.T) {
	st := newIntentStub(t, chatReply("1"))
	c := newTestClassifier(t, Config{IntentClassifyModel: "test/clf"}, st)

	if got := c.classify("auto/free", "s1", intentSeats, longQuestion); got != 1 {
		t.Fatalf("第一次分类 = %d", got)
	}
	if got := c.classify("auto/free", "s2", intentSeats, longQuestion); got != 1 {
		t.Fatalf("第二次分类 = %d", got)
	}
	if st.count() != 1 {
		t.Fatalf("缓存未生效：HTTP 调用 %d 次, want 1", st.count())
	}
	// 换文本要重新分类。
	st.reply(chatReply("2"))
	if got := c.classify("auto/free", "s1", intentSeats, longQuestion+"，请举例"); got != 2 {
		t.Fatalf("新文本分类 = %d, want 2", got)
	}
	if st.count() != 2 {
		t.Fatalf("新文本应重新发 HTTP，实际 %d 次", st.count())
	}
}

// TestIntentClassifyInflightDedup 并发同 key 只发一次 HTTP。
func TestIntentClassifyInflightDedup(t *testing.T) {
	hold := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(hold) }) }
	st := newIntentStub(t, chatReply("1"))
	st.mu.Lock()
	st.hold = hold
	st.mu.Unlock()
	t.Cleanup(release)

	c := newTestClassifier(t, Config{IntentClassifyModel: "test/clf"}, st)

	const n = 4
	results := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = c.classify("auto/free", "", intentSeats, longQuestion)
		}(i)
	}
	// 等 leader 进入挂住的 handler，其余 goroutine 到达等待点。
	deadline := time.Now().Add(2 * time.Second)
	for st.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	release()
	wg.Wait()

	for i, r := range results {
		if r != 1 {
			t.Fatalf("结果[%d] = %d, want 1", i, r)
		}
	}
	if got := st.count(); got != 1 {
		t.Fatalf("并发去重失效：HTTP 调用 %d 次, want 1", got)
	}
}

// TestIntentClassifyCooldown 失败后该组 30 秒内不再尝试（换文本也不发）。
func TestIntentClassifyCooldown(t *testing.T) {
	st := newIntentStub(t, chatReply("1"))
	st.fail(http.StatusInternalServerError)
	c := newTestClassifier(t, Config{IntentClassifyModel: "test/clf"}, st)

	if got := c.classify("auto/free", "", intentSeats, longQuestion); got != -1 {
		t.Fatalf("失败应返回 -1，实际 %d", got)
	}
	if got := c.classify("auto/free", "", intentSeats, longQuestion+"，换个问法"); got != -1 {
		t.Fatalf("冷却期应返回 -1，实际 %d", got)
	}
	if got := st.count(); got != 1 {
		t.Fatalf("冷却期内不应再发 HTTP，实际 %d 次", got)
	}

	// 直接把冷却时间拨到过去，恢复后可再次分类。
	c.mu.Lock()
	c.cooldown["auto/free"] = time.Now().Add(-time.Second)
	c.mu.Unlock()
	st.fail(http.StatusOK)
	st.reply(chatReply("1"))
	if got := c.classify("auto/free", "", intentSeats, longQuestion); got != 1 {
		t.Fatalf("冷却结束后应恢复分类，实际 %d", got)
	}
	if got := st.count(); got != 2 {
		t.Fatalf("恢复后应发 HTTP，实际 %d 次", got)
	}
}

// TestIntentClassifyTimeout 分类端点挂死时按配置超时失败并进入冷却。
func TestIntentClassifyTimeout(t *testing.T) {
	hold := make(chan struct{})
	st := newIntentStub(t, chatReply("1"))
	st.mu.Lock()
	st.hold = hold
	st.mu.Unlock()
	t.Cleanup(func() { close(hold) })

	c := newTestClassifier(t, Config{IntentClassifyModel: "test/clf", IntentTimeoutSeconds: 1}, st)

	start := time.Now()
	if got := c.classify("auto/free", "", intentSeats, longQuestion); got != -1 {
		t.Fatalf("超时应返回 -1，实际 %d", got)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Fatalf("超时太早返回（%v），配置的 1 秒没生效", elapsed)
	}
	// 超时算失败：进入冷却。
	if got := c.classify("auto/free", "", intentSeats, longQuestion+"，再来"); got != -1 {
		t.Fatalf("超时后冷却期内应返回 -1，实际 %d", got)
	}
	if got := st.count(); got != 1 {
		t.Fatalf("冷却期内不应再发 HTTP，实际 %d 次", got)
	}
}

// TestIntentShortQuestionInheritance 短追问直接继承上一轮意图，不发 HTTP。
func TestIntentShortQuestionInheritance(t *testing.T) {
	st := newIntentStub(t, chatReply("1"))
	c := newTestClassifier(t, Config{IntentClassifyModel: "test/clf"}, st)

	// 第一轮：长问题，正常分类。
	if got := c.classify("auto/free", "sessA", intentSeats, longQuestion); got != 1 {
		t.Fatalf("首轮分类 = %d, want 1", got)
	}
	if got := st.count(); got != 1 {
		t.Fatalf("首轮应发 HTTP，实际 %d 次", got)
	}

	// 短追问：同会话直接继承（<24 rune）。
	if got := c.classify("auto/free", "sessA", intentSeats, "继续"); got != 1 {
		t.Fatalf("短追问应继承上一轮意图 1，实际 %d", got)
	}
	// 换个渠道被选中的场景：短追问继承新结果。
	st.reply(chatReply("2"))
	if got := c.classify("auto/free", "sessA", intentSeats, longQuestion+"，要说人话"); got != 2 {
		t.Fatalf("第二轮分类 = %d, want 2", got)
	}
	if got := c.classify("auto/free", "sessA", intentSeats, "换个说法再讲一遍"); got != 2 {
		t.Fatalf("短追问应继承最新意图 2，实际 %d", got)
	}
	if got := st.count(); got != 2 {
		t.Fatalf("短追问不应发 HTTP，实际 %d 次", got)
	}

	// 不同会话的短追问没有可继承的上一轮 → 正常分类。
	if got := c.classify("auto/free", "sessB", intentSeats, "继续"); got != 2 {
		t.Fatalf("新会话短追问应走分类（当前回复 2），实际 %d", got)
	}
	if got := st.count(); got != 3 {
		t.Fatalf("新会话应发 HTTP，实际 %d 次", got)
	}

	// 记忆的渠道已不在候选链里 → 继承落空，退回正常分类。
	shrunk := []modelBinding{{ProviderID: "p-strong", DisplayName: "强渠道"}, {ProviderID: "p-cheap", DisplayName: "便宜渠道"}}
	st.reply(chatReply("0"))
	if got := c.classify("auto/free", "sessA", shrunk, "继续"); got != 0 {
		t.Fatalf("记忆渠道下线后应重新分类（回复 0），实际 %d", got)
	}
	if got := st.count(); got != 4 {
		t.Fatalf("继承落空应发 HTTP，实际 %d 次", got)
	}
}

// TestIntentShortQuestionStillClassifiesFirstTurn 新会话的短问题
// 也可能是第一问：继承落空时要正常分类，而不是拒绝服务。
func TestIntentShortQuestionStillClassifiesFirstTurn(t *testing.T) {
	st := newIntentStub(t, chatReply("1"))
	c := newTestClassifier(t, Config{IntentClassifyModel: "test/clf"}, st)
	if got := c.classify("auto/free", "fresh", intentSeats, "在吗"); got != 1 {
		t.Fatalf("新会话短问题应正常分类，实际 %d", got)
	}
	if st.count() != 1 {
		t.Fatalf("应发 HTTP，实际 %d 次", st.count())
	}
}

// ---- 文本清洗 ----

func TestSanitizeIntentText(t *testing.T) {
	in := "前半句。\n<system-reminder>\n这是注入块\n</system-reminder>\n后半句。"
	if got := sanitizeIntentText(in); got != "前半句。\n后半句。" {
		t.Errorf("剥离结果 = %q", got)
	}
	// 没有注入块时原样返回。
	if got := sanitizeIntentText("普通文本"); got != "普通文本" {
		t.Errorf("无注入块应原样返回，实际 %q", got)
	}
	// 结束标记前后有杂字符也能收尾。
	in = "<system-reminder>头</system-reminder> 正文"
	if got := sanitizeIntentText(in); got != "" {
		t.Errorf("单行整块应被剥掉，实际 %q", got)
	}
	// 未闭合的块：剥到结尾（fail-safe，分类输入变短但不出错）。
	in = "开头\n<system-reminder>\n永不结束"
	if got := sanitizeIntentText(in); got != "开头" {
		t.Errorf("未闭合块应剥到结尾，实际 %q", got)
	}
}

func TestTruncateIntentPrompt(t *testing.T) {
	long := strings.Repeat("字", intentPromptHead+intentPromptTail+500)
	got := truncateIntentPrompt(long)
	want := intentPromptHead + intentPromptTail + len([]rune(intentTruncateMark))
	if n := len([]rune(got)); n != want {
		t.Fatalf("截断后 rune 数 = %d, want %d", n, want)
	}
	if !strings.HasPrefix(got, strings.Repeat("字", 100)) {
		t.Error("头部内容丢失")
	}
	if !strings.HasSuffix(got, strings.Repeat("字", 100)) {
		t.Error("尾部内容丢失")
	}
	short := "短文本"
	if got := truncateIntentPrompt(short); got != short {
		t.Errorf("短文本不应截断，实际 %q", got)
	}
}

// ---- 小函数 ----

func TestParseIntentIndex(t *testing.T) {
	cases := map[string]int{
		"1":    1,
		" 2 ":  2,
		"0":    0,
		"#3":   3,
		"12":   12,
		"序号：1": 1,
		"":     -1,
		"abc":  -1,
		"-1":   -1,
		"x-2y": -1, // 数字前的负号视为负数
	}
	for in, want := range cases {
		if got := parseIntentIndex(in); got != want {
			t.Errorf("parseIntentIndex(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestIntentLoopbackURL(t *testing.T) {
	cases := map[string]string{
		":8787":          "http://127.0.0.1:8787/v1/chat/completions",
		"0.0.0.0:9000":   "http://127.0.0.1:9000/v1/chat/completions",
		"127.0.0.1:1234": "http://127.0.0.1:1234/v1/chat/completions",
		"no-port":        "",
		":":              "",
	}
	for in, want := range cases {
		if got := intentLoopbackURL(in); got != want {
			t.Errorf("intentLoopbackURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIntentTimeoutDefault(t *testing.T) {
	c := newIntentClassifier(&Gateway{cfg: Config{}})
	if got := c.classifyTimeout(); got != intentDefaultTimeout {
		t.Errorf("默认超时 = %v, want %v", got, intentDefaultTimeout)
	}
	c.g.cfg.IntentTimeoutSeconds = 3
	if got := c.classifyTimeout(); got != 3*time.Second {
		t.Errorf("配置超时 = %v, want 3s", got)
	}
}

// TestConversationFingerprint 同一会话（首条用户文本不变）跨轮稳定。
func TestConversationFingerprint(t *testing.T) {
	req := &ir.UnifiedChatRequest{
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "第一轮的问题"},
			{Type: ir.ItemTypeMessage, Role: "assistant", Text: "回答"},
			{Type: ir.ItemTypeMessage, Role: "user", Text: "第二轮的问题"},
		},
	}
	fp1 := conversationFingerprint(req)
	// 第二轮：历史在前，新增一轮。
	req.Items = append(req.Items, ir.UnifiedItem{Type: ir.ItemTypeMessage, Role: "user", Text: "第三轮的问题"})
	fp2 := conversationFingerprint(req)
	if fp1 == "" || fp1 != fp2 {
		t.Fatalf("会话指纹应跨轮稳定：%q vs %q", fp1, fp2)
	}
	// 首条文本不同 → 指纹不同（不同会话不互相继承）。
	other := &ir.UnifiedChatRequest{Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "另一个会话"}}}
	if conversationFingerprint(other) == fp1 {
		t.Fatal("不同会话的指纹不应相同")
	}
	// 没有用户文本 → 空指纹。
	empty := &ir.UnifiedChatRequest{Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "assistant", Text: "hi"}}}
	if conversationFingerprint(empty) != "" {
		t.Fatal("无用户文本应返回空指纹")
	}
}
