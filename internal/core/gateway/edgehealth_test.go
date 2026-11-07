package gateway

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 劣化检测：基线稳定后突然变慢 → 告警；恢复后 → 恢复告警并解除标记。
// 这是两次全量扫描之间的"即时告警"路径（30 秒一次单探针）。
func TestCheckEdgeHealthDegradesAndRecovers(t *testing.T) {
	edgeAuto.state = map[string]*EdgeAutoState{}
	edgeHealth.m = map[string]*edgeHealthTrack{}
	g := &Gateway{settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	g.hostPins().Set("h.example.com", []string{"1.1.1.1"})
	g.edgeScan = func(ctx context.Context, host, path string, candidates []string, concurrency int, timeout time.Duration) []edgeCandidate {
		return nil // 劣化触发的切换在单测里不落新线路
	}
	latency := int64(100)
	healthy := true
	g.edgeProbe = func(ctx context.Context, host, ip, path string, timeout time.Duration) edgeCandidate {
		if !healthy {
			return edgeCandidate{IP: ip, Error: "TCP: timeout"}
		}
		return edgeCandidate{IP: ip, OK: true, TTFBMS: latency, Status: 200}
	}
	cfg := EdgeAutoConfig{Enabled: true}

	// 建立基线（3 次 100ms）。
	for i := 0; i < 3; i++ {
		if g.checkEdgeHealth(context.Background(), "h.example.com", cfg) {
			t.Fatal("基线期不应判劣化")
		}
	}
	if st := edgeAuto.snapshot("h.example.com"); st.Degraded {
		t.Fatalf("基线期不应置为劣化：%+v", st)
	}

	// 变慢到 800ms → 告警。
	latency = 800
	if !g.checkEdgeHealth(context.Background(), "h.example.com", cfg) {
		t.Fatal("明显变慢应判劣化")
	}
	st := edgeAuto.snapshot("h.example.com")
	if !st.Degraded || len(st.Alerts) == 0 || st.HealthMS != 800 {
		t.Fatalf("劣化状态不对：%+v", st)
	}
	if st.BaselineMS != 100 {
		t.Fatalf("基线应保持 100ms，得到 %d", st.BaselineMS)
	}
	if !strings.Contains(st.Alerts[0].Message, "变慢") {
		t.Fatalf("告警文案不对：%q", st.Alerts[0].Message)
	}

	// 恢复 → info 告警并解除标记。
	latency = 90
	g.checkEdgeHealth(context.Background(), "h.example.com", cfg)
	st = edgeAuto.snapshot("h.example.com")
	if st.Degraded {
		t.Fatalf("恢复后不该仍标记劣化：%+v", st)
	}
	if len(st.Alerts) == 0 || !strings.Contains(st.Alerts[0].Message, "恢复") {
		t.Fatalf("缺少恢复告警：%+v", st.Alerts)
	}
	if st.Alerts[0].Level != "info" {
		t.Fatalf("恢复告警级别应为 info：%+v", st.Alerts[0])
	}
}

// 连续失败 → 一次告警（不是每次失败都刷屏）。
func TestCheckEdgeHealthConsecutiveFailures(t *testing.T) {
	edgeAuto.state = map[string]*EdgeAutoState{}
	edgeHealth.m = map[string]*edgeHealthTrack{}
	g := &Gateway{settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	g.hostPins().Set("f.example.com", []string{"2.2.2.2"})
	g.edgeScan = func(ctx context.Context, host, path string, candidates []string, concurrency int, timeout time.Duration) []edgeCandidate {
		return nil
	}
	g.edgeProbe = func(ctx context.Context, host, ip, path string, timeout time.Duration) edgeCandidate {
		return edgeCandidate{IP: ip, Error: "TLS: handshake timeout"}
	}
	cfg := EdgeAutoConfig{Enabled: true}

	if g.checkEdgeHealth(context.Background(), "f.example.com", cfg) {
		t.Fatal("单次失败不应立即告警（可能有抖动）")
	}
	if !g.checkEdgeHealth(context.Background(), "f.example.com", cfg) {
		t.Fatal("连续两次失败应告警")
	}
	st := edgeAuto.snapshot("f.example.com")
	if !st.Degraded || len(st.Alerts) == 0 {
		t.Fatalf("失败告警缺失：%+v", st)
	}
	if !strings.Contains(st.Alerts[0].Message, "连续失败") {
		t.Fatalf("告警文案不对：%q", st.Alerts[0].Message)
	}
	// 第三次失败不应重复刷屏（仍是 1 条）。
	g.checkEdgeHealth(context.Background(), "f.example.com", cfg)
	if got := len(edgeAuto.snapshot("f.example.com").Alerts); got != 1 {
		t.Fatalf("连续失败不应重复告警，告警数 = %d", got)
	}
}

// 未被钉住（没有固定线路）时不做健康采样。
func TestCheckEdgeHealthSkippedWithoutPins(t *testing.T) {
	edgeAuto.state = map[string]*EdgeAutoState{}
	edgeHealth.m = map[string]*edgeHealthTrack{}
	g := &Gateway{settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	g.edgeProbe = func(ctx context.Context, host, ip, path string, timeout time.Duration) edgeCandidate {
		t.Fatal("没有固定线路时不该发起探测")
		return edgeCandidate{}
	}
	if g.checkEdgeHealth(context.Background(), "none.example.com", EdgeAutoConfig{Enabled: true}) {
		t.Fatal("无固定线路不应判劣化")
	}
}

// 智能体把告警按时间倒序保存，最多 10 条。
func TestAppendEdgeAlertCap(t *testing.T) {
	st := EdgeAutoState{}
	for i := 0; i < 12; i++ {
		appendEdgeAlert(&st, "warn", "e"+string(rune('a'+i)), int64(100+i))
	}
	if len(st.Alerts) != 10 {
		t.Fatalf("告警数 = %d，want 10", len(st.Alerts))
	}
	if st.Alerts[0].Message != "el" {
		t.Fatalf("最新告警应排在最前，得到 %q", st.Alerts[0].Message)
	}
	if st.LastAlert != 111 {
		t.Fatalf("LastAlert = %d，want 111", st.LastAlert)
	}
}
