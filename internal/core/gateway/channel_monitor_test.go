package gateway

import (
	"testing"
	"time"
)

func TestMonitorIntervalClamp(t *testing.T) {
	g := &Gateway{}
	if d := g.monitorInterval(); d != 0 {
		t.Fatalf("default = %v, want 0", d)
	}
	g.settings.MonitorMinutes = 2
	if d := g.monitorInterval(); d != 5*time.Minute {
		t.Fatalf("2min = %v, want 5m", d)
	}
	g.settings.MonitorMinutes = 15
	if d := g.monitorInterval(); d != 15*time.Minute {
		t.Fatalf("15min = %v", d)
	}
	g.settings.MonitorMinutes = 5000
	if d := g.monitorInterval(); d != 1440*time.Minute {
		t.Fatalf("5000min = %v, want 24h", d)
	}
}

func TestRecordProbeHistory(t *testing.T) {
	g := &Gateway{}
	for i := 0; i < 25; i++ {
		g.recordProbe(channelProbe{
			ProviderID: "p", AccountID: "a", OK: i%2 == 0, Ms: int64(i), At: int64(i + 1),
		}, "p/model")
	}
	g.monitorMu.Lock()
	defer g.monitorMu.Unlock()
	p := g.monitor["p\x00a"]
	if p == nil {
		t.Fatal("probe not recorded")
	}
	if len(p.History) != probeHistoryLimit {
		t.Fatalf("history = %d, want %d", len(p.History), probeHistoryLimit)
	}
	if p.History[0].At != 25 || p.History[len(p.History)-1].At != 6 {
		t.Errorf("history order: first=%d last=%d", p.History[0].At, p.History[len(p.History)-1].At)
	}
	if p.At != 25 || p.Ms != 24 {
		t.Errorf("latest sample not kept: at=%d ms=%d", p.At, p.Ms)
	}
}
