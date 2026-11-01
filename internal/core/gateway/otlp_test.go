package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- 测试基建：可编程的 /v1/traces 收集器 ---

// otlpCapture 收集 POST 到 /v1/traces 的请求体。
// failFirst>0 时前 N 次返回 503（带 Retry-After: 0），status 非 0 时固定返回该码。
type otlpCapture struct {
	mu        sync.Mutex
	bodies    []string
	requests  int
	failFirst int
	status    int
	ch        chan string
}

func newOTLPCapture() *otlpCapture { return &otlpCapture{ch: make(chan string, 16)} }

func (c *otlpCapture) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.requests++
		code := c.status
		if code == 0 {
			code = http.StatusOK
		}
		if c.failFirst > 0 {
			c.failFirst--
			code = http.StatusServiceUnavailable
		}
		c.bodies = append(c.bodies, string(b))
		c.mu.Unlock()

		if code != http.StatusOK {
			if c.failFirst == 0 && code == http.StatusServiceUnavailable {
				w.Header().Set("Retry-After", "0")
			}
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"message":"test failure"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"partialSuccess":{}}`))
		select {
		case c.ch <- string(b):
		default:
		}
	})
}

func (c *otlpCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests
}

func (c *otlpCapture) body(i int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bodies[i]
}

// collectOTLPSpans 从收集器读到累计 want 条 span 为止（容忍批次被拆分成多次
// POST），返回合并后的 span 数组。
func collectOTLPSpans(t *testing.T, c *otlpCapture, want int) []map[string]any {
	t.Helper()
	var spans []map[string]any
	deadline := time.After(5 * time.Second)
	for len(spans) < want {
		select {
		case body := <-c.ch:
			spans = append(spans, parseOTLPSpans(t, body)...)
		case <-deadline:
			t.Fatalf("等待 span 超时：已收 %d/%d", len(spans), want)
		}
	}
	return spans
}

// parseOTLPSpans 解析一帧导出请求体，返回其中的 span 数组（map 形态）。
func parseOTLPSpans(t *testing.T, body string) []map[string]any {
	t.Helper()
	var doc struct {
		ResourceSpans []struct {
			ScopeSpans []struct {
				Spans []map[string]any `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("导出请求体不是合法 JSON: %v\nbody=%s", err, body)
	}
	var spans []map[string]any
	for _, rs := range doc.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			spans = append(spans, ss.Spans...)
		}
	}
	return spans
}

// otlpSpanAttr 断言 span 含有指定属性并返回其 value 对象。
func otlpSpanAttr(t *testing.T, span map[string]any, key string) map[string]any {
	t.Helper()
	attrs, _ := span["attributes"].([]any)
	for _, a := range attrs {
		kv, ok := a.(map[string]any)
		if !ok {
			continue
		}
		if kv["key"] == key {
			v, _ := kv["value"].(map[string]any)
			return v
		}
	}
	t.Fatalf("span 缺少属性 %q: %v", key, span)
	return nil
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}

// newTestExporter 建一个绑定 httptest 收集器的导出器，返回导出器与收集器。
// cfg 由测试闭包持有（getCfg 返回其当前值）；endpoint 已指向收集器，
// 测试可随时改写 *cfg 模拟配置变更。
func newTestExporter(t *testing.T, cfg *OTLPConfig) (*otlpExporter, *otlpCapture) {
	t.Helper()
	cap := newOTLPCapture()
	srv := httptest.NewServer(cap.handler())
	t.Cleanup(srv.Close)
	cfg.Endpoint = srv.URL
	e := newOTLPExporter(func() OTLPConfig { return *cfg }, nil)
	t.Cleanup(e.invalidate)
	return e, cap
}

// --- 默认值与端点校验 ---

func TestOTLPConfigDefaults(t *testing.T) {
	var c OTLPConfig
	if c.batchSize() != 32 {
		t.Fatalf("batchSize 默认 = %d，期望 32", c.batchSize())
	}
	if c.flushInterval() != 5*time.Second {
		t.Fatalf("flushInterval 默认 = %s，期望 5s", c.flushInterval())
	}
	if c.queueMax() != 128 {
		t.Fatalf("queueMax 默认 = %d，期望 128", c.queueMax())
	}
}

func TestOTLPTracesURL(t *testing.T) {
	u, err := otlpTracesURL("http://127.0.0.1:4318")
	if err != nil || u != "http://127.0.0.1:4318/v1/traces" {
		t.Fatalf("URL = %q, err = %v", u, err)
	}
	u, err = otlpTracesURL("http://collector:4318/base/")
	if err != nil || u != "http://collector:4318/base/v1/traces" {
		t.Fatalf("带路径 URL = %q, err = %v", u, err)
	}
	for _, bad := range []string{"", "ftp://h", "127.0.0.1:4318", "http://"} {
		if _, err := otlpTracesURL(bad); err == nil {
			t.Fatalf("endpoint %q 应当被拒绝", bad)
		}
	}
}

// --- 单批 flush：定时触发（interval 调 10ms）---

func TestOTLPFlushOnInterval(t *testing.T) {
	cfg := &OTLPConfig{BatchSize: 8, QueueMax: 16}
	e, cap := newTestExporter(t, cfg)
	e.flushEvery = 10 * time.Millisecond

	e.enqueue(RequestLog{
		Time: time.Now().Unix(), Protocol: "anthropic", Model: "claude-x",
		ProviderID: "prov-1", AccountID: "acc-9", Status: "ok",
		DurationMS: 120, FirstByteMS: 30, Stream: true, Items: 4,
	})

	spans := collectOTLPSpans(t, cap, 1)
	if len(spans) != 1 {
		t.Fatalf("span 数 = %d，期望 1", len(spans))
	}
	span := spans[0]

	// wire 结构：resourceSpans[0].resource.attributes 含 service.name。
	var doc map[string]any
	if err := json.Unmarshal([]byte(cap.body(0)), &doc); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v", err)
	}
	rsArr, _ := doc["resourceSpans"].([]any)
	if len(rsArr) != 1 {
		t.Fatalf("resourceSpans 数 = %d，期望 1", len(rsArr))
	}
	rs, _ := rsArr[0].(map[string]any)
	res, _ := rs["resource"].(map[string]any)
	if res == nil {
		t.Fatal("缺少 resource")
	}
	resAttrs, _ := res["attributes"].([]any)
	var gotService string
	for _, a := range resAttrs {
		kv := a.(map[string]any)
		if kv["key"] == "service.name" {
			v, _ := kv["value"].(map[string]any)
			gotService, _ = v["stringValue"].(string)
		}
	}
	if gotService != "aether-cockpit" {
		t.Fatalf("service.name = %q，期望 aether-cockpit", gotService)
	}
	ssArr, _ := rs["scopeSpans"].([]any)
	if len(ssArr) != 1 {
		t.Fatalf("scopeSpans 数 = %d，期望 1", len(ssArr))
	}
	ss, _ := ssArr[0].(map[string]any)
	scope, _ := ss["scope"].(map[string]any)
	if scope == nil || scope["name"] != "aether-cockpit/gateway" {
		t.Fatalf("scope.name = %v，期望 aether-cockpit/gateway", scope)
	}

	// span 形态：SERVER kind、hex ID、nano 时间串、数值/布尔属性形态。
	if span["kind"].(float64) != 1 {
		t.Fatalf("kind = %v，期望 1（SERVER）", span["kind"])
	}
	if got := span["traceId"].(string); len(got) != 32 {
		t.Fatalf("traceId 长度 = %d，期望 32 hex", len(got))
	}
	if got := span["spanId"].(string); len(got) != 16 {
		t.Fatalf("spanId 长度 = %d，期望 16 hex", len(got))
	}
	if span["name"] != "claude-x" {
		t.Fatalf("span name = %v，期望模型名", span["name"])
	}
	sn, _ := span["startTimeUnixNano"].(string)
	en, _ := span["endTimeUnixNano"].(string)
	if sn == "" || en == "" {
		t.Fatalf("nano 时间戳缺失: start=%q end=%q", sn, en)
	}
	if v := otlpSpanAttr(t, span, "provider_id"); v["stringValue"] != "prov-1" {
		t.Fatalf("provider_id = %v", v)
	}
	if v := otlpSpanAttr(t, span, "account_id"); v["stringValue"] != "acc-9" {
		t.Fatalf("account_id = %v", v)
	}
	if v := otlpSpanAttr(t, span, "status"); v["stringValue"] != "ok" {
		t.Fatalf("status = %v", v)
	}
	if v := otlpSpanAttr(t, span, "duration_ms"); v["intValue"] != "120" {
		t.Fatalf("duration_ms 应为字符串形态 intValue，得到 %v", v)
	}
	if v := otlpSpanAttr(t, span, "first_byte_ms"); v["intValue"] != "30" {
		t.Fatalf("first_byte_ms = %v", v)
	}
	if v := otlpSpanAttr(t, span, "stream"); v["boolValue"] != true {
		t.Fatalf("stream 应为布尔形态 boolValue，得到 %v", v)
	}
	if _, has := span["status"]; has {
		t.Fatalf("成功请求不应带 span status")
	}
}

// --- 单批 flush：攒满触发（notify 路径），一次 POST 带全部 3 条 ---

func TestOTLPFlushOnBatchFull(t *testing.T) {
	cfg := &OTLPConfig{BatchSize: 3, QueueMax: 16}
	e, cap := newTestExporter(t, cfg)
	e.flushEvery = time.Hour // 本测试只走攒满触发

	e.enqueue(RequestLog{Time: time.Now().Unix(), Protocol: "anthropic", Model: "m1", ProviderID: "p1", Status: "ok", DurationMS: 10})
	e.enqueue(RequestLog{Time: time.Now().Unix(), Protocol: "anthropic", Model: "m2", Status: "ok", DurationMS: 20})
	e.enqueue(RequestLog{Time: time.Now().Unix(), Protocol: "openai", Model: "m3", Status: "error", ErrCode: "upstream_error", ErrMsg: "503 boom", DurationMS: 30, FastPath: true})

	spans := collectOTLPSpans(t, cap, 3)
	if len(spans) != 3 {
		t.Fatalf("span 数 = %d，期望 3", len(spans))
	}
	// 攒满触发：3 条应在同一个 POST 里（notify 在第 3 条入队时才置位）。
	if got := len(parseOTLPSpans(t, cap.body(0))); got != 3 {
		t.Fatalf("第一个 POST 含 %d 条 span，期望 3", got)
	}

	// 错误请求 → span status code=2 + message。
	var errSpan map[string]any
	for _, s := range spans {
		if s["name"] == "m3" {
			errSpan = s
		}
	}
	if errSpan == nil {
		t.Fatal("找不到错误 span m3")
	}
	st, _ := errSpan["status"].(map[string]any)
	if st == nil || st["code"].(float64) != 2 {
		t.Fatalf("错误 span status = %v，期望 code=2", errSpan["status"])
	}
	if msg, _ := st["message"].(string); !strings.Contains(msg, "503 boom") {
		t.Fatalf("status.message = %q，期望包含原始错误", msg)
	}
	if v := otlpSpanAttr(t, errSpan, "err_code"); v["stringValue"] != "upstream_error" {
		t.Fatalf("err_code = %v", v)
	}
	if v := otlpSpanAttr(t, errSpan, "fast_path"); v["boolValue"] != true {
		t.Fatalf("fast_path = %v", v)
	}

	queued, _ := e.stats()
	if queued != 0 {
		t.Fatalf("flush 后积压 = %d，期望 0", queued)
	}
}

// --- 满队列丢最旧 ---

func TestOTLPQueueFullDropsOldest(t *testing.T) {
	cfg := &OTLPConfig{BatchSize: 100, QueueMax: 4}
	e, cap := newTestExporter(t, cfg)
	e.flushEvery = time.Hour // 不让 worker 自动触发，手动 flush

	for i := 1; i <= 6; i++ {
		e.enqueue(RequestLog{Time: time.Now().Unix(), Protocol: "anthropic", Model: fmt.Sprintf("m%d", i), Status: "ok"})
	}
	queued, dropped := e.stats()
	if queued != 4 || dropped != 2 {
		t.Fatalf("stats = (queued=%d, dropped=%d)，期望 (4, 2)", queued, dropped)
	}

	// 手动 flush（与 worker 走同一代码路径）：BatchSize=100 → 一次带走全部 4 条。
	e.mu.Lock()
	gen := e.gen
	e.mu.Unlock()
	if gen == nil {
		t.Fatal("worker 未启动")
	}
	gen.flushBatch(e)

	spans := collectOTLPSpans(t, cap, 4)
	var models []string
	for _, s := range spans {
		models = append(models, s["name"].(string))
	}
	if strings.Join(models, ",") != "m3,m4,m5,m6" {
		t.Fatalf("导出的模型 = %v，期望最新 4 条 m3..m6", models)
	}
	if _, dropped2 := e.stats(); dropped2 != 2 {
		t.Fatalf("丢弃计数 = %d，期望 2", dropped2)
	}
}

// --- 重试：503 + Retry-After: 0 → 立即重试成功 ---

func TestOTLPRetryOn503WithRetryAfter(t *testing.T) {
	cfg := &OTLPConfig{BatchSize: 1, QueueMax: 16}
	cap := newOTLPCapture()
	cap.failFirst = 1
	srv := httptest.NewServer(cap.handler())
	t.Cleanup(srv.Close)

	e := newOTLPExporter(func() OTLPConfig { return *cfg }, nil)
	t.Cleanup(e.invalidate)
	e.flushEvery = time.Hour // 走攒满触发（BatchSize=1，首条即满）
	cfg.Endpoint = srv.URL

	e.enqueue(RequestLog{Time: time.Now().Unix(), Protocol: "anthropic", Model: "retry-me", Status: "ok"})

	waitFor(t, func() bool { return cap.count() >= 2 }, 5*time.Second)
	spans := parseOTLPSpans(t, cap.body(1))
	if len(spans) != 1 || spans[0]["name"] != "retry-me" {
		t.Fatalf("重试后的导出内容 = %v，期望 retry-me", spans)
	}
	time.Sleep(50 * time.Millisecond) // 给可能的第 3 次请求留窗口
	if got := cap.count(); got != 2 {
		t.Fatalf("请求数 = %d，期望恰好 2（首次 503 + Retry-After:0 重试）", got)
	}
	if queued, _ := e.stats(); queued != 0 {
		t.Fatalf("成功后积压 = %d，期望 0", queued)
	}
}

// --- 不可重试状态码：整批丢弃，不重试 ---

func TestOTLPNonRetryableStatusDropsBatch(t *testing.T) {
	cfg := &OTLPConfig{BatchSize: 1, QueueMax: 16}
	cap := newOTLPCapture()
	cap.status = http.StatusBadRequest
	srv := httptest.NewServer(cap.handler())
	t.Cleanup(srv.Close)

	e := newOTLPExporter(func() OTLPConfig { return *cfg }, nil)
	t.Cleanup(e.invalidate)
	e.flushEvery = time.Hour
	cfg.Endpoint = srv.URL

	e.enqueue(RequestLog{Time: time.Now().Unix(), Protocol: "anthropic", Model: "doomed", Status: "ok"})

	waitFor(t, func() bool { return cap.count() >= 1 }, 5*time.Second)
	time.Sleep(100 * time.Millisecond)
	if got := cap.count(); got != 1 {
		t.Fatalf("请求数 = %d，期望 1（400 不可重试，直接丢弃）", got)
	}
	if queued, _ := e.stats(); queued != 0 {
		t.Fatalf("丢弃后积压 = %d，期望 0", queued)
	}
}

// --- 配置变更作废积压：旧队列清空、新配置生效 ---

func TestOTLPInvalidateSwitchesDestination(t *testing.T) {
	oldCap := newOTLPCapture()
	oldSrv := httptest.NewServer(oldCap.handler())
	defer oldSrv.Close()
	newCap := newOTLPCapture()
	newSrv := httptest.NewServer(newCap.handler())
	defer newSrv.Close()

	cfg := &OTLPConfig{Endpoint: oldSrv.URL, BatchSize: 8, QueueMax: 16}
	e := newOTLPExporter(func() OTLPConfig { return *cfg }, nil)
	defer e.invalidate()
	e.flushEvery = 10 * time.Millisecond

	e.enqueue(RequestLog{Time: time.Now().Unix(), Protocol: "anthropic", Model: "old-1", Status: "ok"})
	e.enqueue(RequestLog{Time: time.Now().Unix(), Protocol: "anthropic", Model: "old-2", Status: "ok"})
	if queued, _ := e.stats(); queued != 2 {
		t.Fatalf("积压 = %d，期望 2", queued)
	}

	// 模拟 settings 变更钩子：作废积压 + 停 worker。
	e.invalidate()
	if queued, _ := e.stats(); queued != 0 {
		t.Fatalf("作废后积压 = %d，期望 0", queued)
	}

	// 新配置生效（换目的地），旧批次绝不发给新目的地。
	cfg.Endpoint = newSrv.URL
	e.enqueue(RequestLog{Time: time.Now().Unix(), Protocol: "anthropic", Model: "new-1", Status: "ok"})

	spans := collectOTLPSpans(t, newCap, 1)
	if spans[0]["name"] != "new-1" {
		t.Fatalf("新目的地收到 %v，期望 new-1", spans[0]["name"])
	}
	if got := oldCap.count(); got != 0 {
		t.Fatalf("旧目的地收到 %d 个请求，期望 0", got)
	}
	// 合并进新导出帧的只有新条目。
	if got := len(parseOTLPSpans(t, newCap.body(0))); got != 1 {
		t.Fatalf("新目的地单帧 span 数 = %d，期望 1（旧积压已作废）", got)
	}
}

// --- 打码与 headers 沿用（settings 包内 handler 级测试）---

func TestSettingsOTLPMaskingAndPreservation(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json")})

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body))
		rec := httptest.NewRecorder()
		g.handleUpdateSettings(rec, req)
		return rec
	}
	disk := func() string {
		b, err := os.ReadFile(g.settingsPath)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// 写入含鉴权头的 OTLP 配置 → 更新响应与 GET 均打码。
	rec := post(`{"otlp":{"endpoint":"http://127.0.0.1:4318","headers":{"Authorization":"Bearer super-secret"}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态 = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "super-secret") {
		t.Fatalf("更新响应泄露明文 headers: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), otlpHeadersMaskedKey) {
		t.Fatalf("更新响应缺少打码形态: %s", rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec = httptest.NewRecorder()
	g.handleSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET 状态 = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "super-secret") {
		t.Fatalf("GET 响应泄露明文 headers: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), otlpHeadersMaskedKey) {
		t.Fatalf("GET 响应缺少打码形态: %s", rec.Body.String())
	}
	if !strings.Contains(disk(), "super-secret") {
		t.Fatalf("落盘文件应保留明文 headers: %s", disk())
	}

	// 回写打码形态 → 沿用旧 headers（落盘仍是明文，打码占位符不落盘）。
	rec = post(`{"otlp":{"endpoint":"http://127.0.0.1:4318","headers":{"___masked___":"true"}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("回写打码形态状态 = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(disk(), "super-secret") {
		t.Fatalf("回写打码形态后旧 headers 丢失: %s", disk())
	}
	if strings.Contains(disk(), otlpHeadersMaskedKey) {
		t.Fatalf("打码占位符不应落盘: %s", disk())
	}

	// 不带 headers 键的部分更新 → 仍沿用旧 headers，其余字段正常更新。
	rec = post(`{"otlp":{"endpoint":"http://127.0.0.1:4318","batch_size":64}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("部分更新状态 = %d body=%s", rec.Code, rec.Body.String())
	}
	b := disk()
	if !strings.Contains(b, "super-secret") || !strings.Contains(b, `"batch_size": 64`) {
		t.Fatalf("部分更新应保留旧 headers 且写入新字段: %s", b)
	}

	// 显式空对象 → 清空 headers。
	rec = post(`{"otlp":{"endpoint":"http://127.0.0.1:4318","headers":{}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("清空状态 = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(disk(), "super-secret") {
		t.Fatalf("显式空对象应清空 headers: %s", disk())
	}

	// 端点校验：非 http(s) → 400，且不影响已有配置。
	rec = post(`{"otlp":{"endpoint":"ftp://nope"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法端点状态 = %d，期望 400 body=%s", rec.Code, rec.Body.String())
	}
}

// --- settings 更新 → 注册表钩子 → 作废积压（全链路）---

func TestSettingsUpdateInvalidatesOTLPBacklog(t *testing.T) {
	cap := newOTLPCapture()
	srv := httptest.NewServer(cap.handler())
	defer srv.Close()

	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json")})

	// 模拟 New() 接线：getCfg 走 settingsMu，invalidate 注册进实例注册表。
	e := newOTLPExporter(
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
	defer e.invalidate()
	defer otlpExporters.Delete(g)
	e.flushEvery = time.Hour

	post := func(body string) {
		req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body))
		rec := httptest.NewRecorder()
		g.handleUpdateSettings(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST settings 状态 = %d body=%s", rec.Code, rec.Body.String())
		}
	}

	// 启用 OTLP → 入队 2 条（不触发 flush：BatchSize 很大）。
	post(fmt.Sprintf(`{"otlp":{"endpoint":%q,"batch_size":100,"queue_max":16}}`, srv.URL))
	e.enqueue(RequestLog{Time: time.Now().Unix(), Protocol: "anthropic", Model: "stale-1", Status: "ok"})
	e.enqueue(RequestLog{Time: time.Now().Unix(), Protocol: "anthropic", Model: "stale-2", Status: "ok"})
	if queued, _ := e.stats(); queued != 2 {
		t.Fatalf("积压 = %d，期望 2", queued)
	}

	// 通过 settings API 改 OTLP 配置 → 钩子作废积压。
	post(`{"otlp":{"endpoint":"http://127.0.0.1:1","batch_size":100,"queue_max":16}}`)
	if queued, _ := e.stats(); queued != 0 {
		t.Fatalf("配置变更后积压 = %d，期望 0", queued)
	}
	if got := cap.count(); got != 0 {
		t.Fatalf("旧目的地收到 %d 个请求，期望 0", got)
	}

	// 旧配置的 endpoint 已失效（127.0.0.1:1）——若没作废而直接 flush 会失败；
	// 再改回测试服务器并 flush，只能带走新入队的条目。
	post(fmt.Sprintf(`{"otlp":{"endpoint":%q,"batch_size":100,"queue_max":16}}`, srv.URL))
	e.enqueue(RequestLog{Time: time.Now().Unix(), Protocol: "anthropic", Model: "fresh-1", Status: "ok"})
	e.mu.Lock()
	gen := e.gen
	e.mu.Unlock()
	gen.flushBatch(e)
	spans := parseOTLPSpans(t, cap.body(0))
	if len(spans) != 1 || spans[0]["name"] != "fresh-1" {
		t.Fatalf("导出内容 = %v，期望仅 fresh-1", spans)
	}
}
