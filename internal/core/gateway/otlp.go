package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 手写 OTLP-over-HTTP 导出器 —— 对标 aether internal/otlp 的做法：
// 不引官方 SDK，纯标准库把请求审计记录（RequestLog）导出为 OTLP/JSON traces，
// 直接兼容 OpenTelemetry Collector / Grafana Tempo / Jaeger 的 OTLP HTTP 入口。
//
// 设计要点（docs/AETHER_DEEP_ANALYSIS.md 第 7/8 节）：
//   - 队列满则丢最旧，绝不阻塞请求路径；丢弃计数进日志
//   - 攒批 BatchSize（默认 32）条或 FlushIntervalS（默认 5）秒触发一次 POST
//   - 重试最多 3 次（1s→2s→4s），只对网络错误 / 429 / 502 / 503 / 504，
//     尊重 Retry-After 头（有则按其秒数）；其余状态码整批丢弃
//   - 配置变更（设置页保存 OTLP 字段）作废全部积压并停 worker，
//     绝不把旧批次发给新目的地或新凭据；下次入队用新配置快照重启
//
// 懒启动：New() 之后无需任何接线，首次 enqueue 且配置启用时才起 worker
// goroutine；未配置 endpoint 时 enqueue 是"读一次配置 + 判空"的零副作用路径。

const (
	otlpTracePathSuffix = "v1/traces"
	otlpHTTPTimeout     = 30 * time.Second
	// otlpMaxRetries 单批最多重试次数（不含首次发送），退避 1s→2s→4s。
	otlpMaxRetries = 3
	// otlpMaxRetryAfter 是 Retry-After 的上限，防御异常远端把 worker 挂死一天。
	otlpMaxRetryAfter = 30 * time.Second
	// OTLP span 语义常量：kind 1 = SERVER；status code 2 = ERROR。
	otlpSpanKindServer  = 1
	otlpStatusCodeError = 2
	// 资源与服务范围标识。
	otlpServiceName = "aether-cockpit"
	otlpScopeName   = "aether-cockpit/gateway"
)

// otlpRetryBackoff 是第 1..3 次重试前的等待时长（Retry-After 优先于它）。
var otlpRetryBackoff = [otlpMaxRetries]time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// otlpHTTPClient 共享出站客户端（与 webdav.go 一样纯标准库）。
var otlpHTTPClient = &http.Client{Timeout: otlpHTTPTimeout}

// otlpExporters 是 Gateway → invalidate 钩子 的实例注册表。
// Gateway 结构体在 gateway.go（该文件维护纪律上禁改），无法加钩子字段，
// 故用实例键注册表解耦：设置页保存 OTLP 字段时经 Gateway.invalidateOTLP 调用。
var otlpExporters sync.Map // map[*Gateway]func()

// otlpExporter 把请求审计记录批量导出为 OTLP/JSON traces。
type otlpExporter struct {
	// getCfg 读取当前生效配置（实现须持 settingsMu 取快照）。
	getCfg func() OTLPConfig

	mu      sync.Mutex
	once    *sync.Once    // 每代一个懒启动 once；作废后换新，实现"下次 enqueue 重启"
	gen     *otlpGen      // 当前代 worker；nil = 未启动 / 已作废
	queue   []RequestLog  // 待导出积压（先进先出，满则丢最旧）
	dropped uint64        // 队列满丢弃的累计条数
	notify  chan struct{} // 积压达到 batchSize 时唤醒 worker（缓冲 1，非阻塞）
	// flushEvery 覆盖定时 flush 间隔（测试钩子；0 = 用配置值）。
	flushEvery time.Duration
}

// otlpGen 是一代 worker：绑定一份配置快照与生命周期。
// 配置变更作废旧代、下次入起新代，旧代带走的批次永远不会发给新目的地。
type otlpGen struct {
	cfg    OTLPConfig
	ctx    context.Context
	cancel context.CancelFunc
}

// newOTLPExporter 创建导出器。getCfg 在每次入队 / 启动前读取当前配置；
// registerInvalidate（可空）在构造时收到 invalidate 函数，供配置变更钩子注册。
func newOTLPExporter(getCfg func() OTLPConfig, registerInvalidate func(invalidate func())) *otlpExporter {
	e := &otlpExporter{
		getCfg: getCfg,
		once:   &sync.Once{},
		notify: make(chan struct{}, 1),
	}
	if registerInvalidate != nil {
		registerInvalidate(e.invalidate)
	}
	return e
}

// gatewayOTLPExporter 是 New() 接线用的便捷构造（一行接入）：
// 读配置经 settingsMu；invalidate 钩子注册进实例注册表，
// 设置页保存 OTLP 字段时由 handleUpdateSettings → invalidateOTLP 调用。
func gatewayOTLPExporter(g *Gateway) *otlpExporter {
	return newOTLPExporter(
		func() OTLPConfig {
			g.settingsMu.Lock()
			defer g.settingsMu.Unlock()
			if g.settings.OTLP == nil {
				return OTLPConfig{}
			}
			return *g.settings.OTLP
		},
		func(invalidate func()) { otlpExporters.Store(g, invalidate) },
	)
}

// invalidateOTLP 作废该网关的 OTLP 积压（配置变更钩子，settings.go 调用）。
// 未接线（New() 未创建导出器）时是空操作。
func (g *Gateway) invalidateOTLP() {
	if v, ok := otlpExporters.Load(g); ok {
		if inv, ok := v.(func()); ok {
			inv()
		}
	}
}

// enqueue 把一条审计记录放进导出队列。
// 未配置 / 端点非法时直接返回；队列满丢弃最旧，绝不阻塞请求路径。
func (e *otlpExporter) enqueue(entry RequestLog) {
	cfg := e.getCfg()
	if _, err := otlpTracesURL(cfg.Endpoint); err != nil {
		return // 未启用或端点非法：静默跳过（保存配置时已校验过一轮）
	}

	e.mu.Lock()
	max := cfg.queueMax()
	for max > 0 && len(e.queue) >= max {
		e.queue = e.queue[1:]
		e.dropped++
	}
	e.queue = append(e.queue, entry)
	full := len(e.queue) >= cfg.batchSize()
	// 懒启动：每代 once 只触发一次；作废后换了新 once，下次 enqueue 重启。
	e.once.Do(func() { e.startLocked(cfg) })
	e.mu.Unlock()

	if full {
		select {
		case e.notify <- struct{}{}:
		default:
		}
	}
}

// startLocked 启动一代 worker（绑定 cfg 快照）。调用方持有 e.mu。
func (e *otlpExporter) startLocked(cfg OTLPConfig) {
	gen := &otlpGen{cfg: cfg}
	gen.ctx, gen.cancel = context.WithCancel(context.Background())
	e.gen = gen
	interval := cfg.flushInterval()
	if e.flushEvery > 0 {
		interval = e.flushEvery
	}
	go gen.run(e, interval)
	log.Printf("[INFO] OTLP 导出已启动 → %s（batch=%d, interval=%s, queue=%d）",
		cfg.Endpoint, cfg.batchSize(), interval, cfg.queueMax())
}

// invalidate 作废积压并停掉当前 worker：清空队列 + cancel 旧代（连在途 POST
// 一起中止）。保证配置变更后绝不把旧批次发给新目的地或新凭据；
// 下次 enqueue 会用新配置快照重启。
func (e *otlpExporter) invalidate() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.gen != nil {
		e.gen.cancel()
		e.gen = nil
	}
	if n := len(e.queue); n > 0 {
		log.Printf("[INFO] OTLP 配置变更，作废 %d 条未导出积压", n)
	}
	e.queue = nil
	e.once = &sync.Once{}
}

// stats 返回当前积压条数与累计丢弃数（诊断用）。
func (e *otlpExporter) stats() (queued int, dropped uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.queue), e.dropped
}

// run 是 worker 主循环：攒批触发（notify）与定时触发（ticker）任一唤醒。
func (gen *otlpGen) run(e *otlpExporter, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-gen.ctx.Done():
			return
		case <-ticker.C:
			gen.flushBatch(e)
		case <-e.notify:
			gen.flushBatch(e)
		}
	}
}

// flushBatch 取出至多 batchSize 条并导出。
// 旧代 worker 的唤醒（作废后可能还有一次 ticker/notify 在途）直接返回，
// 保证被作废的批次不会从旧代漏出去。
func (gen *otlpGen) flushBatch(e *otlpExporter) {
	e.mu.Lock()
	if e.gen != gen {
		e.mu.Unlock()
		return
	}
	n := len(e.queue)
	if n == 0 {
		e.mu.Unlock()
		return
	}
	if n > gen.cfg.batchSize() {
		n = gen.cfg.batchSize()
	}
	batch := make([]RequestLog, n)
	copy(batch, e.queue[:n])
	e.queue = e.queue[n:]
	e.mu.Unlock()

	gen.export(batch)
}

// export 序列化并 POST 一批 span，带受限重试。最终失败仅记日志——
// 观测数据可以丢，不能反过来拖垮请求路径。
func (gen *otlpGen) export(batch []RequestLog) {
	u, err := otlpTracesURL(gen.cfg.Endpoint)
	if err != nil {
		log.Printf("[WARN] OTLP endpoint 非法，丢弃 %d 条: %v", len(batch), err)
		return
	}
	body, err := json.Marshal(buildExportTraceRequest(batch))
	if err != nil {
		log.Printf("[WARN] OTLP 序列化失败，丢弃 %d 条: %v", len(batch), err)
		return
	}

	var lastErr error
	var retryAfter time.Duration
	var retryAfterSet bool
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			delay := otlpRetryBackoff[attempt-1]
			if retryAfterSet {
				delay = retryAfter // 远端明确要求的等待优先于退避序列
			}
			select {
			case <-gen.ctx.Done():
				return
			case <-time.After(delay):
			}
		}
		code, ra, raSet, perr := gen.post(u, body)
		retryAfter, retryAfterSet = ra, raSet
		if perr == nil && code >= 200 && code <= 299 {
			return
		}
		if perr != nil {
			lastErr = perr
		} else {
			lastErr = fmt.Errorf("远端返回 %d", code)
		}
		retryable := perr != nil || code == http.StatusTooManyRequests ||
			code == http.StatusBadGateway || code == http.StatusServiceUnavailable ||
			code == http.StatusGatewayTimeout
		if !retryable || attempt >= otlpMaxRetries {
			break
		}
	}
	log.Printf("[WARN] OTLP 导出失败，丢弃 %d 条: %v", len(batch), lastErr)
}

// post 发送一次导出请求。返回状态码与 Retry-After（秒，是否出现）；
// 网络层错误走 err。
func (gen *otlpGen) post(u string, body []byte) (code int, retryAfter time.Duration, retryAfterSet bool, err error) {
	req, err := http.NewRequestWithContext(gen.ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return 0, 0, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range gen.cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := otlpHTTPClient.Do(req)
	if err != nil {
		return 0, 0, false, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		return resp.StatusCode, 0, false, nil
	}
	if ra := strings.TrimSpace(resp.Header.Get("Retry-After")); ra != "" {
		if secs, perr := strconv.Atoi(ra); perr == nil && secs >= 0 {
			d := time.Duration(secs) * time.Second
			if d > otlpMaxRetryAfter {
				d = otlpMaxRetryAfter
			}
			return resp.StatusCode, d, true, nil
		}
	}
	return resp.StatusCode, 0, false, nil
}

// --- OTLPConfig 取值辅助（默认值兜底）---

func (c OTLPConfig) batchSize() int {
	if c.BatchSize > 0 {
		return c.BatchSize
	}
	return defaultOTLPBatchSize
}

func (c OTLPConfig) flushInterval() time.Duration {
	if c.FlushIntervalS > 0 {
		return time.Duration(c.FlushIntervalS) * time.Second
	}
	return time.Duration(defaultOTLPFlushIntervalS) * time.Second
}

func (c OTLPConfig) queueMax() int {
	if c.QueueMax > 0 {
		return c.QueueMax
	}
	return defaultOTLPQueueMax
}

// otlpTracesURL 校验 endpoint（仅 http(s)、必须带主机名）并拼出
// <endpoint>/v1/traces。风格对齐 webdav.go 的 normalizeWebDAVURL。
func otlpTracesURL(endpoint string) (string, error) {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		return "", errors.New("OTLP endpoint 未配置")
	}
	if !strings.HasSuffix(ep, "/") {
		ep += "/"
	}
	u, err := url.Parse(ep)
	if err != nil {
		return "", fmt.Errorf("OTLP endpoint 无效: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("OTLP endpoint 仅支持 http(s)，收到 %q", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("OTLP endpoint 缺少主机名")
	}
	return ep + otlpTracePathSuffix, nil
}

// --- OTLP/JSON wire（结构对齐 opentelemetry-proto 的 trace JSON 编码）---
//
// proto3 JSON 映射里 64 位整数编码为字符串，故 startTimeUnixNano 与
// intValue 都是字符串；bool/数值按各自字段形态（boolValue / intValue）。

type otlpAnyValue struct {
	StringValue string `json:"stringValue,omitempty"`
	BoolValue   *bool  `json:"boolValue,omitempty"`
	// IntValue 按 proto3 JSON 惯例编码为十进制字符串。
	IntValue string `json:"intValue,omitempty"`
}

type otlpKeyValue struct {
	Key   string       `json:"key"`
	Value otlpAnyValue `json:"value"`
}

type otlpSpanStatus struct {
	Code    int    `json:"code"` // 0 UNSET / 1 OK / 2 ERROR
	Message string `json:"message,omitempty"`
}

type otlpSpan struct {
	TraceID           string          `json:"traceId"` // 16 字节 hex
	SpanID            string          `json:"spanId"`  // 8 字节 hex
	Name              string          `json:"name"`
	Kind              int             `json:"kind"` // 1 = SERVER
	StartTimeUnixNano string          `json:"startTimeUnixNano"`
	EndTimeUnixNano   string          `json:"endTimeUnixNano"`
	Attributes        []otlpKeyValue  `json:"attributes,omitempty"`
	Status            *otlpSpanStatus `json:"status,omitempty"`
}

type otlpScope struct {
	Name string `json:"name"`
}

type otlpScopeSpans struct {
	Scope *otlpScope `json:"scope,omitempty"`
	Spans []otlpSpan `json:"spans"`
}

type otlpResource struct {
	Attributes []otlpKeyValue `json:"attributes"`
}

type otlpResourceSpans struct {
	Resource   *otlpResource    `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpExportTraceRequest struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

func otlpStrAttr(k, v string) otlpKeyValue {
	return otlpKeyValue{Key: k, Value: otlpAnyValue{StringValue: v}}
}

func otlpIntAttr(k string, v int64) otlpKeyValue {
	return otlpKeyValue{Key: k, Value: otlpAnyValue{IntValue: strconv.FormatInt(v, 10)}}
}

func otlpBoolAttr(k string, v bool) otlpKeyValue {
	return otlpKeyValue{Key: k, Value: otlpAnyValue{BoolValue: &v}}
}

// buildSpan 把一条审计记录编码成一个 SERVER span。
func buildSpan(entry RequestLog) otlpSpan {
	start := entry.Time
	if start <= 0 {
		start = time.Now().Unix()
	}
	startNano := start * int64(time.Second)
	endNano := startNano + entry.DurationMS*int64(time.Millisecond)

	name := entry.Model
	if name == "" {
		name = entry.Protocol
	}
	if name == "" {
		name = "request"
	}

	attrs := []otlpKeyValue{otlpStrAttr("protocol", entry.Protocol)}
	if entry.ProviderID != "" {
		attrs = append(attrs, otlpStrAttr("provider_id", entry.ProviderID))
	}
	if entry.AccountID != "" {
		attrs = append(attrs, otlpStrAttr("account_id", entry.AccountID))
	}
	attrs = append(attrs, otlpStrAttr("status", entry.Status))
	if entry.ErrCode != "" {
		attrs = append(attrs, otlpStrAttr("err_code", entry.ErrCode))
	}
	attrs = append(attrs, otlpIntAttr("duration_ms", entry.DurationMS))
	if entry.FirstByteMS > 0 {
		attrs = append(attrs, otlpIntAttr("first_byte_ms", entry.FirstByteMS))
	}
	attrs = append(attrs, otlpBoolAttr("stream", entry.Stream))
	if entry.Items > 0 {
		attrs = append(attrs, otlpIntAttr("items", int64(entry.Items)))
	}
	attrs = append(attrs, otlpBoolAttr("fast_path", entry.FastPath))

	span := otlpSpan{
		TraceID:           otlpRandomHex(16),
		SpanID:            otlpRandomHex(8),
		Name:              name,
		Kind:              otlpSpanKindServer,
		StartTimeUnixNano: strconv.FormatInt(startNano, 10),
		EndTimeUnixNano:   strconv.FormatInt(endNano, 10),
		Attributes:        attrs,
	}
	if entry.Status == "error" {
		msg := entry.ErrMsg
		if msg == "" {
			msg = entry.ErrCode
		}
		span.Status = &otlpSpanStatus{Code: otlpStatusCodeError, Message: msg}
	}
	return span
}

// buildExportTraceRequest 把一批记录编码为 OTLP/JSON 导出请求体。
func buildExportTraceRequest(batch []RequestLog) otlpExportTraceRequest {
	spans := make([]otlpSpan, len(batch))
	for i, e := range batch {
		spans[i] = buildSpan(e)
	}
	return otlpExportTraceRequest{
		ResourceSpans: []otlpResourceSpans{{
			Resource: &otlpResource{
				Attributes: []otlpKeyValue{otlpStrAttr("service.name", otlpServiceName)},
			},
			ScopeSpans: []otlpScopeSpans{{
				Scope: &otlpScope{Name: otlpScopeName},
				Spans: spans,
			}},
		}},
	}
}

// otlpRandomHex 返回 n 字节的随机 hex（traceId=16 字节 / spanId=8 字节）。
func otlpRandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败在现实环境几乎不会发生；退化用时间熵填充，
		// 保证 ID 仍然非空、格式合法（接收端只按 hex 解码）。
		v := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(v)
			v = v*6364136223846793005 + 1442695040888963407
		}
	}
	return hex.EncodeToString(b)
}
