package gateway

import (
	"context"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// TTFT 口径必须统一：只有"真实内容"才算首字节。
//
// 历史缺陷：三条路径各自在完全不同的时机调用 markFirstByte ——
// IR 在收到任意首个上游事件（含空握手 EventStart）时标记；
// HTTP 直通在读 body 之前标记；WS 直通要等整条流跑完才标记。
// 同一栏数字因此在不同渠道间不可比。
func TestIsContentEventExcludesHandshake(t *testing.T) {
	// 这些是"真的有内容要发给客户端"的事件。
	content := []ir.StreamEventType{
		ir.EventTextDelta, ir.EventReasoningDelta,
		ir.EventToolCallStart, ir.EventToolCallDelta,
		ir.EventUsage, ir.EventDone,
	}
	for _, ev := range content {
		if !isContentEvent(ev) {
			t.Errorf("%v 应算内容事件", ev)
		}
	}
	// 握手/空事件不算：把它们算进去会让"模型还在思考"显示成低 TTFT。
	notContent := []ir.StreamEventType{ir.EventStart}
	for _, ev := range notContent {
		if isContentEvent(ev) {
			t.Errorf("%v 不该算内容事件", ev)
		}
	}
}

// 首字节与响应头是两个独立时刻，先后关系必须正确。
func TestRequestTimerSeparatesHeaderAndContent(t *testing.T) {
	tm := newRequestTimer()
	time.Sleep(10 * time.Millisecond)
	tm.markHeader() // 上游开始响应
	headerAt := tm.headerMS()
	if headerAt < 5 {
		t.Fatalf("headerMS = %d，应 >= 5", headerAt)
	}
	if got := tm.firstByteMS(); got != 0 {
		t.Fatalf("还没内容时 firstByteMS 应为 0，得到 %d", got)
	}

	time.Sleep(15 * time.Millisecond)
	tm.markFirstByte() // 首个正文增量
	firstAt := tm.firstByteMS()
	if firstAt <= headerAt {
		t.Fatalf("首字节(%d) 应晚于响应头(%d)", firstAt, headerAt)
	}

	// 再次标记不应改变已记录的值（幂等）。
	tm.markFirstByte()
	tm.markHeader()
	if tm.firstByteMS() != firstAt {
		t.Fatalf("重复标记不该改变首字节：%d -> %d", firstAt, tm.firstByteMS())
	}
	if tm.headerMS() != headerAt {
		t.Fatalf("重复标记不该改变 header：%d -> %d", headerAt, tm.headerMS())
	}
}

// markFirstByte 首次调用时，如果 header 还没记（IR 路径里两者可能同时到），
// 应把 header 也补上，避免出现"有首字节但没有 header"的空洞。
func TestMarkFirstByteBackfillsHeader(t *testing.T) {
	tm := newRequestTimer()
	time.Sleep(5 * time.Millisecond)
	tm.markFirstByte()
	if tm.headerMS() == 0 {
		t.Fatal("markFirstByte 应回填 header 时刻")
	}
	if tm.headerMS() != tm.firstByteMS() {
		t.Fatalf("回填后两者应相等：header=%d first=%d", tm.headerMS(), tm.firstByteMS())
	}
}

// 没标记时两个指标都是 0（UI 据此显示"—"而不是 0ms）。
func TestRequestTimerZeroWhenUnmarked(t *testing.T) {
	tm := newRequestTimer()
	if tm.firstByteMS() != 0 || tm.headerMS() != 0 {
		t.Fatalf("未标记时都应为 0：first=%d header=%d", tm.firstByteMS(), tm.headerMS())
	}
}

// 端到端：openStream 只标 header，watchStreamErrors 才标首字节。
// 这条链路保证"首字节"在不同协议间口径一致。
func TestOpenStreamMarksHeaderNotFirstByte(t *testing.T) {
	g := New(Config{
		SettingsPath:     t.TempDir() + "/settings.json",
		AccountStorePath: t.TempDir() + "/accounts.json",
		UsagePath:        "-",
		MaxRecords:       10,
		FirstByteTimeout: 2 * time.Second,
		MaxAttempts:      1,
	})
	env := provider.NewAccountEnv("acc-hdr", "openai-compat")
	h := func(ctx context.Context, e *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
		ch := make(chan ir.UnifiedStreamEvent, 3)
		// 先来一个空握手事件（真实上游普遍如此）。
		ch <- ir.UnifiedStreamEvent{Type: ir.EventStart}
		ch <- ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: "hello"}
		close(ch)
		return ch, nil
	}
	tm := newRequestTimer()
	ch, err := g.openStream(context.Background(), context.Background(), h, env,
		&ir.UnifiedChatRequest{Model: "m", Stream: true}, tm)
	if err != nil {
		t.Fatalf("openStream: %v", err)
	}
	// 此时只是"上游开始响应"。毫秒取整可能在快路径下为 0，
	// 所以用 hasHeader/hasFirstByte 判断"是否标记过"，不看毫秒数。
	if !tm.hasHeader() {
		t.Fatal("openStream 应标记 header")
	}
	if tm.hasFirstByte() {
		t.Fatal("openStream 不该标记首字节（握手事件不算内容）")
	}
	if tm.firstByteMS() != 0 {
		t.Fatalf("openStream 不该标记首字节（握手事件不算内容），得到 %d", tm.firstByteMS())
	}
	// 把流读完：watchStreamErrors 遇到正文事件时才标首字节。
	wrapped := g.watchStreamErrors(ch, env, tm, nil)
	for range wrapped {
	}
	if !tm.hasFirstByte() {
		t.Fatal("收到正文事件后应标记首字节")
	}
	if tm.firstByteMS() < tm.headerMS() {
		t.Fatalf("首字节(%d) 不该早于 header(%d)", tm.firstByteMS(), tm.headerMS())
	}
}
