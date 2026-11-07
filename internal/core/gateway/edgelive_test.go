package gateway

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

// 真实请求的首 token 时间用来校准基线；单次偏高不告警，连续两次才告警。
// 阈值刻意比探针宽（>2x 且多 800ms）：首 token 里大部分是上游模型思考时间。
func TestNoteEdgeLiveTTFTDegradesOnSustainedSlowdown(t *testing.T) {
	edgeAuto.state = map[string]*EdgeAutoState{}
	edgeHealth.m = map[string]*edgeHealthTrack{}
	g := &Gateway{settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	g.edgeScan = func(ctx context.Context, host, path string, candidates []string, concurrency int, timeout time.Duration) []edgeCandidate {
		return nil
	}
	// open-design 渠道的 BaseURL 就是 amr-link.open-design.ai。
	if _, ok := provider.Get("open-design"); !ok {
		t.Skip("缺少 open-design 渠道")
	}
	g.settingsMu.Lock()
	g.settings.EdgeAuto = map[string]EdgeAutoConfig{
		"amr-link.open-design.ai": {Enabled: true, Path: "/v1/models"},
	}
	g.settingsMu.Unlock()

	// 建立基线：三次 600ms。
	for i := 0; i < 3; i++ {
		g.noteEdgeLiveTTFT("open-design", 600)
	}
	if st := edgeAuto.snapshot("amr-link.open-design.ai"); st.Degraded {
		t.Fatalf("基线期不应告警：%+v", st)
	}
	if st := edgeAuto.snapshot("amr-link.open-design.ai"); st.LiveSamples != 3 || st.LiveMS != 600 {
		t.Fatalf("样本统计不对：%+v", st)
	}

	// 单次 2500ms：不告警（可能是模型思考久）。
	g.noteEdgeLiveTTFT("open-design", 2500)
	if st := edgeAuto.snapshot("amr-link.open-design.ai"); st.Degraded {
		t.Fatalf("单次偏高不应告警：%+v", st)
	}
	// 再来一次 2500ms：连续两次 → 告警。
	g.noteEdgeLiveTTFT("open-design", 2500)
	st := edgeAuto.snapshot("amr-link.open-design.ai")
	if !st.Degraded || len(st.Alerts) == 0 {
		t.Fatalf("连续两次变慢应告警：%+v", st)
	}
	if !strings.Contains(st.Alerts[0].Message, "首 token") {
		t.Fatalf("告警文案应说明是真实请求：%q", st.Alerts[0].Message)
	}
	if st.LiveBaseline <= 0 {
		t.Fatalf("应记录真实请求基线：%+v", st)
	}

	// 恢复正常样本后，streak 归零（不再触发）。
	g.noteEdgeLiveTTFT("open-design", 620)
	g.noteEdgeLiveTTFT("open-design", 610)
	t2 := healthTrackFor("amr-link.open-design.ai")
	t2.mu.Lock()
	streak := t2.liveSlowStreak
	t2.mu.Unlock()
	if streak != 0 {
		t.Fatalf("恢复后连续计数应归零，得到 %d", streak)
	}
}

// 未开启自动切换的域名不采集真实请求样本。
func TestNoteEdgeLiveTTFTSkipsDisabled(t *testing.T) {
	edgeAuto.state = map[string]*EdgeAutoState{}
	edgeHealth.m = map[string]*edgeHealthTrack{}
	g := &Gateway{settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	g.noteEdgeLiveTTFT("open-design", 5000)
	if st := edgeAuto.snapshot("amr-link.open-design.ai"); st.LiveSamples != 0 {
		t.Fatalf("未开启时不应采样：%+v", st)
	}
}

// 全量候选池：288 个、包含 SJC 段、可复现。
func TestDenseEdgeCandidates(t *testing.T) {
	dense := defaultEdgeCandidatesDense(288)
	if len(dense) != 288 {
		t.Fatalf("全量候选数 = %d，want 288", len(dense))
	}
	again := defaultEdgeCandidatesDense(288)
	for i := range dense {
		if dense[i] != again[i] {
			t.Fatalf("全量候选顺序必须可复现：%d %q != %q", i, dense[i], again[i])
		}
	}
	hasSJC := false
	for _, ip := range dense {
		if strings.HasPrefix(ip, "104.17.") {
			hasSJC = true
		}
	}
	if !hasSJC {
		t.Fatal("全量池缺少 SJC 段")
	}
	// 默认池与全量池长度不同（步长 4 vs 2）。
	if len(defaultEdgeCandidates(288)) >= len(dense) {
		t.Fatalf("默认池不应超过全量池：%d vs %d", len(defaultEdgeCandidates(288)), len(dense))
	}
}

// 主机名解析。
func TestHostOfBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://amr-link.open-design.ai/v1": "amr-link.open-design.ai",
		"https://API.Example.com":            "api.example.com",
		"":                                   "",
		"not a url":                          "",
	}
	for in, want := range cases {
		if got := hostOfBaseURL(in); got != want {
			t.Errorf("hostOfBaseURL(%q) = %q，want %q", in, got, want)
		}
	}
}
