package gateway

import (
	"log"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

// RequestLog 是一条请求审计记录。
//
// 存在的意义：当 Claude Code 报"连接失败"时，用户需要能立刻分清
// 是客户端配错了、网关没收到、渠道超时、还是上游 401。
// 没有这个面板，排查只能靠翻控制台日志。
type RequestLog struct {
	Seq        int64  `json:"seq"`
	Time       int64  `json:"time"`
	Protocol   string `json:"protocol"`
	Model      string `json:"model"`
	ProviderID string `json:"provider_id,omitempty"`
	AccountID  string `json:"account_id,omitempty"`
	Stream     bool   `json:"stream"`
	Status     string `json:"status"` // ok | error
	ErrCode    string `json:"err_code,omitempty"`
	ErrMsg     string `json:"err_msg,omitempty"`
	// DurationMS 端到端耗时（流式为整个流的时长）。
	DurationMS int64 `json:"duration_ms"`
	// FirstByteMS 首字节耗时，流式体验的关键指标。
	FirstByteMS int64 `json:"first_byte_ms,omitempty"`
	// FinishReason 是上游给出的结束原因（stop / length / tool_calls …）。
	// 排查"响应被截断"时最关键的一个字段：length 表示撞上了输出上限。
	FinishReason string `json:"finish_reason,omitempty"`
	// Truncated 表示上游因输出长度上限被截断（FinishReason == length）。
	//
	// 这类响应如果含工具调用，参数 JSON 一定是残缺的，客户端会报
	// "Unterminated string in JSON"。标出来才知道问题在上游额度，
	// 而不是网关把内容弄丢了。
	Truncated bool `json:"truncated,omitempty"`
	// HeaderMS 是"到上游开始响应"的耗时（响应头/首个上游事件）。
	// 与 FirstByteMS（首个内容增量）分开：HeaderMS 约等于网络+排队，
	// FirstByteMS-HeaderMS 约等于模型思考，排查时一眼能分清楚。
	HeaderMS int64 `json:"header_ms,omitempty"`
	// Items 本次请求的上下文项数，便于发现"历史没回填"这类问题。
	Items int `json:"items,omitempty"`
	// PreviousID 是否续接了上一轮。
	PreviousID bool `json:"previous_id,omitempty"`
	// FastPath 是否走了零损直通（未经过 IR 转换）。
	FastPath bool `json:"fast_path,omitempty"`
	// AutoContinued 本轮是否因上下文超限而裁剪重试过一次。
	AutoContinued bool `json:"auto_continued,omitempty"`
	// TrimmedItems 自动续接时丢弃的条目数。
	TrimmedItems int `json:"trimmed_items,omitempty"`
	// Token 与费用：由账本在请求结束时回填，供日志列表和详情抽屉展示。
	InputTokens     int64   `json:"input_tokens,omitempty"`
	OutputTokens    int64   `json:"output_tokens,omitempty"`
	CacheRead       int64   `json:"cache_read,omitempty"`
	CacheWrite      int64   `json:"cache_write,omitempty"`
	ReasoningTokens int64   `json:"reasoning_tokens,omitempty"`
	Cost            float64 `json:"cost,omitempty"`
	Priced          bool    `json:"priced,omitempty"`
	PricingModel    string  `json:"pricing_model,omitempty"`
}

// LogBuffer 是固定容量的环形缓冲。
type LogBuffer struct {
	mu      sync.Mutex
	entries []RequestLog
	cap     int
	seq     int64
}

// NewLogBuffer 创建日志缓冲。
func NewLogBuffer(capacity int) *LogBuffer {
	if capacity <= 0 {
		capacity = 500
	}
	return &LogBuffer{
		entries: make([]RequestLog, 0, capacity),
		cap:     capacity,
	}
}

// Push 追加一条记录，超出容量时丢弃最旧的。
func (b *LogBuffer) Push(e RequestLog) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.seq++
	e.Seq = b.seq

	if len(b.entries) >= b.cap {
		// 简单的前移：容量 500 时每次 O(n)，实测量级完全可接受，
		// 换 ring buffer 的取模下标会牺牲"按时间倒序取最近 N 条"的直观性。
		copy(b.entries, b.entries[1:])
		b.entries[len(b.entries)-1] = e
		return
	}
	b.entries = append(b.entries, e)
}

// Recent 返回最近 limit 条（按时间倒序）。
func (b *LogBuffer) Recent(limit int) []RequestLog {
	b.mu.Lock()
	defer b.mu.Unlock()

	if limit <= 0 || limit > len(b.entries) {
		limit = len(b.entries)
	}
	out := make([]RequestLog, 0, limit)
	for i := len(b.entries) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, b.entries[i])
	}
	return out
}

// Clear 清空全部请求记录。
//
// 只保留容量与自增序号（seq 继续往上走）：seq 是"第几条请求"的身份，
// 清空后复用旧号会让前端同一 key 下的缓存错位。返回被清掉的条数。
func (b *LogBuffer) Clear() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	n := len(b.entries)
	b.entries = b.entries[:0]
	return n
}

// Stats 汇总统计。
type LogStats struct {
	Total      int            `json:"total"`
	OK         int            `json:"ok"`
	Errors     int            `json:"errors"`
	AvgMS      int64          `json:"avg_ms"`
	P95MS      int64          `json:"p95_ms"`
	ByProvider map[string]int `json:"by_provider,omitempty"`
}

// Stats 计算汇总。
func (b *LogBuffer) Stats() LogStats {
	b.mu.Lock()
	defer b.mu.Unlock()

	st := LogStats{
		Total:      len(b.entries),
		ByProvider: make(map[string]int, 4),
	}
	if len(b.entries) == 0 {
		return st
	}

	durations := make([]int64, 0, len(b.entries))
	var sum int64
	for _, e := range b.entries {
		if e.Status == "ok" {
			st.OK++
		} else {
			st.Errors++
		}
		if e.ProviderID != "" {
			st.ByProvider[e.ProviderID]++
		}
		durations = append(durations, e.DurationMS)
		sum += e.DurationMS
	}
	st.AvgMS = sum / int64(len(b.entries))

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	idx := int(float64(len(durations)) * 0.95)
	if idx >= len(durations) {
		idx = len(durations) - 1
	}
	st.P95MS = durations[idx]
	return st
}

// handleLogs 返回最近请求日志。
func (g *Gateway) handleLogs(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"logs":  g.logs.Recent(limit),
		"stats": g.logs.Stats(),
	})
}

// handleClearLogs DELETE /api/logs
//
// 清空请求记录（内存环缓冲）。用量聚合与账本是另外两套数据，各自有独立入口，
// 这里不动它们——否则用户"只想清空请求列表"会把用量统计一起清掉。
func (g *Gateway) handleClearLogs(w http.ResponseWriter, r *http.Request) {
	if g.logs == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared": 0})
		return
	}
	cleared := g.logs.Clear()
	log.Printf("[LOGS] 按用户请求清空请求记录：%d 条", cleared)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared": cleared})
}

// requestTimer 采集一次请求的耗时。
type requestTimer struct {
	start time.Time
	// header 是"拿到响应头/开始有上游响应"的时刻（不等于有正文）。
	header time.Time
	// first 是首个**内容**增量（正文/思考/工具参数）的时刻。
	//
	// 这两个分开记：不同路径（IR / HTTP 直通 / WS 直通）过去都在各自的时机
	// 调用同一个 markFirstByte()，导致同一栏数字在不同渠道间不可比——
	// IR 把 EventStart（空握手事件）算进去、直通在读 body 前标记、
	// WS 甚至可能流结束才标记。拆开之后，UI 的"首字节"始终是内容时间，
	// header 时间另行保留用于诊断。
	first time.Time
}

func newRequestTimer() *requestTimer {
	return &requestTimer{start: time.Now()}
}

// markFirstByte 记录"首个内容增量"的时刻（仅第一次有效）。
//
// 只应在真正有内容要发给客户端时调用；纯握手事件（EventStart）不算，
// 否则慢模型会在"还没产出任何字"的情况下显示成低 TTFT。
func (t *requestTimer) markFirstByte() {
	if t.first.IsZero() {
		t.first = time.Now()
	}
	if t.header.IsZero() {
		t.header = t.first
	}
}

// markHeader 记录"上游开始响应"（拿到响应头/首个上游事件）的时刻。
// 用于诊断"慢在建连还是慢在模型"，与面向用户的首字节分开。
func (t *requestTimer) markHeader() {
	if t.header.IsZero() {
		t.header = time.Now()
	}
}

func (t *requestTimer) totalMS() int64 {
	return time.Since(t.start).Milliseconds()
}

func (t *requestTimer) firstByteMS() int64 {
	if t.first.IsZero() {
		return 0
	}
	return t.first.Sub(t.start).Milliseconds()
}

// hasHeader / hasFirstByte 报告对应时刻是否已记录。
//
// 为什么需要：耗时字段是毫秒取整，极快路径下"已标记"和"未标记"都可能是 0。
// 断言时序时必须看这两个布尔值，而不是看毫秒数。
func (t *requestTimer) hasHeader() bool    { return !t.header.IsZero() }
func (t *requestTimer) hasFirstByte() bool { return !t.first.IsZero() }

// headerMS 是"到响应头"的耗时，0 表示还没拿到。
func (t *requestTimer) headerMS() int64 {
	if t.header.IsZero() {
		return 0
	}
	return t.header.Sub(t.start).Milliseconds()
}
