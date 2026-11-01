package gateway

import (
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

// requestTimer 采集一次请求的耗时。
type requestTimer struct {
	start time.Time
	first time.Time
}

func newRequestTimer() *requestTimer {
	return &requestTimer{start: time.Now()}
}

// markFirstByte 记录首字节时刻（仅第一次有效）。
func (t *requestTimer) markFirstByte() {
	if t.first.IsZero() {
		t.first = time.Now()
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
