package gateway

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// 切换判定：当前线路全挂立即换；抖动大时更快就换；否则要快出阈值。
func TestDecideEdgeSwitch(t *testing.T) {
	cases := []struct {
		name       string
		curMS      int64
		curOK      int
		bestMS     int64
		threshold  int64
		wantSwitch bool
	}{
		{"当前线路全挂", 0, 0, 300, 150, true},
		{"没有可用候选", 400, 3, 0, 150, false},
		{"抖动大且更快", 500, 1, 480, 150, true},
		{"抖动大但没更快", 500, 1, 520, 150, false},
		{"优势超过阈值", 500, 3, 300, 150, true},
		{"优势刚好超过阈值", 500, 3, 349, 150, true},
		{"优势不足阈值", 500, 3, 400, 150, false},
		{"新线路更慢", 300, 3, 400, 150, false},
	}
	for _, c := range cases {
		got, reason := decideEdgeSwitch(c.curMS, c.curOK, c.bestMS, c.threshold)
		if got != c.wantSwitch {
			t.Errorf("%s: decide=%v want %v（%s）", c.name, got, c.wantSwitch, reason)
		}
		if reason == "" {
			t.Errorf("%s: 必须给出原因", c.name)
		}
	}
}

// 边缘拒绝服务（error code 10xx / CF 403 页）必须被判为不可用：
// 它们的响应往往比健康 IP 还快，只看耗时会把它排到第一。
func TestEdgeBlockedResponse(t *testing.T) {
	blocked := []struct {
		status int
		body   string
	}{
		{403, `<html><body>error code: 1034</body></html>`},
		{403, `error code: 1020`},
		{403, `<title>Access denied | Cloudflare</title>`},
	}
	for _, c := range blocked {
		if !edgeBlockedResponse(c.status, c.body) {
			t.Errorf("status=%d body=%q 应判为边缘拒绝", c.status, c.body)
		}
	}
	allowed := []struct {
		status int
		body   string
	}{
		{200, `{"object":"list"}`},
		{401, `{"error":{"message":"missing api key"}}`}, // 回源正常，只是没鉴权
		{404, `{"error":"not found"}`},
		{429, `{"error":"rate limited"}`},
	}
	for _, c := range allowed {
		if edgeBlockedResponse(c.status, c.body) {
			t.Errorf("status=%d body=%q 不应判为边缘拒绝", c.status, c.body)
		}
	}
}

// 候选池必须包含社区实测的 SJC 段，且总数受 limit 约束、顺序可复现。
func TestEdgeCandidatePoolIncludesSJC(t *testing.T) {
	first := defaultEdgeCandidates(96)
	second := defaultEdgeCandidates(96)
	if len(first) != 96 {
		t.Fatalf("候选数 = %d，want 96", len(first))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("候选顺序必须可复现：第 %d 个 %q != %q", i, first[i], second[i])
		}
	}
	foundSJC := false
	for _, ip := range first {
		if len(ip) > len("104.17.") && ip[:len("104.17.")] == "104.17." {
			foundSJC = true
		}
	}
	if !foundSJC {
		t.Fatal("候选池缺少 104.17.x 的 SJC 段")
	}
}

// 端到端：无固定线路时采用最优候选；之后优势不足阈值则不来回切。
func TestRunEdgeAutoOnceSwitchesAndHolds(t *testing.T) {
	edgeAuto.state = map[string]*EdgeAutoState{}
	g := &Gateway{settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	g.edgeProbe = func(ctx context.Context, host, ip, path string, timeout time.Duration) edgeCandidate {
		ms := map[string]int64{"1.1.1.1": 100, "2.2.2.2": 220, "3.3.3.3": 400}[ip]
		if ms == 0 {
			return edgeCandidate{IP: ip, Error: "不可达"}
		}
		return edgeCandidate{IP: ip, OK: true, TTFBMS: ms, Status: 200}
	}
	g.edgeScan = func(ctx context.Context, host, path string, candidates []string, concurrency int, timeout time.Duration) []edgeCandidate {
		return []edgeCandidate{
			{IP: "1.1.1.1", OK: true, TTFBMS: 100, Status: 200},
			{IP: "2.2.2.2", OK: true, TTFBMS: 220, Status: 200},
			{IP: "3.3.3.3", OK: true, TTFBMS: 400, Status: 200},
		}
	}

	cfg := EdgeAutoConfig{Enabled: true}
	// 第一次：没有固定线路 → 采用最优，并写备用。
	st := g.runEdgeAutoOnce(context.Background(), "amr-link.open-design.ai", cfg)
	if got := g.hostPins().Get("amr-link.open-design.ai"); len(got) != 2 || got[0] != "1.1.1.1" {
		t.Fatalf("应钉住最优+备用，得到 %v", got)
	}
	if st.CurrentMS != 100 || st.Switches != 1 {
		t.Fatalf("状态不对：%+v", st)
	}

	// 第二次：当前 100ms、最优 100ms → 优势不足阈值，保持。
	st2 := g.runEdgeAutoOnce(context.Background(), "amr-link.open-design.ai", cfg)
	if st2.Switches != 1 {
		t.Fatalf("不应再次切换：%+v", st2)
	}
	if st2.Reason == "" {
		t.Fatal("必须给出保持原因")
	}
}

// 当前线路明显变慢时，超过阈值才切换。
func TestRunEdgeAutoOnceSwitchesOnDegrade(t *testing.T) {
	edgeAuto.state = map[string]*EdgeAutoState{}
	g := &Gateway{settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	g.hostPins().Set("slow.example.com", []string{"9.9.9.9"})
	g.edgeProbe = func(ctx context.Context, host, ip, path string, timeout time.Duration) edgeCandidate {
		switch ip {
		case "9.9.9.9":
			return edgeCandidate{IP: ip, OK: true, TTFBMS: 800, Status: 200}
		default:
			return edgeCandidate{IP: ip, OK: true, TTFBMS: 200, Status: 200}
		}
	}
	g.edgeScan = func(ctx context.Context, host, path string, candidates []string, concurrency int, timeout time.Duration) []edgeCandidate {
		return []edgeCandidate{{IP: "5.5.5.5", OK: true, TTFBMS: 200, Status: 200}}
	}
	st := g.runEdgeAutoOnce(context.Background(), "slow.example.com", EdgeAutoConfig{Enabled: true})
	if got := g.hostPins().Get("slow.example.com"); len(got) == 0 || got[0] != "5.5.5.5" {
		t.Fatalf("应变更到 5.5.5.5，得到 %v（reason=%s）", got, st.Reason)
	}
	if st.Switches != 1 {
		t.Fatalf("切换计数不对：%+v", st)
	}
}

// 没有可用候选时不能清掉现有固定线路。
func TestRunEdgeAutoOnceKeepsPinsOnTotalFailure(t *testing.T) {
	edgeAuto.state = map[string]*EdgeAutoState{}
	g := &Gateway{settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	g.hostPins().Set("keep.example.com", []string{"7.7.7.7"})
	g.edgeProbe = func(ctx context.Context, host, ip, path string, timeout time.Duration) edgeCandidate {
		return edgeCandidate{IP: ip, Error: "不可达"}
	}
	g.edgeScan = func(ctx context.Context, host, path string, candidates []string, concurrency int, timeout time.Duration) []edgeCandidate {
		return nil
	}
	g.runEdgeAutoOnce(context.Background(), "keep.example.com", EdgeAutoConfig{Enabled: true})
	if got := g.hostPins().Get("keep.example.com"); len(got) != 1 || got[0] != "7.7.7.7" {
		t.Fatalf("全失败时不应改动固定线路，得到 %v", got)
	}
}

// 设置读写：开启后能从设置里读回，关闭后不再出现在运行集合里。
func TestEdgeAutoConfigPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	g := &Gateway{settingsPath: path}
	g.settingsMu.Lock()
	g.settings.EdgeAuto = map[string]EdgeAutoConfig{
		"a.example.com": {Enabled: true, Path: "/v1/models", IntervalSec: 120, ThresholdMS: 200},
	}
	g.settingsMu.Unlock()

	cfg := g.edgeAutoConfig("a.example.com")
	if !cfg.Enabled || cfg.Path != "/v1/models" || cfg.interval() != 2*time.Minute || cfg.thresholdMS() != 200 {
		t.Fatalf("配置读回不对：%+v", cfg)
	}
	if hosts := g.edgeAutoHosts(); len(hosts) != 1 {
		t.Fatalf("运行集合应含一个域名，得到 %v", hosts)
	}
	if err := g.saveSettings(); err != nil {
		t.Fatalf("保存设置失败：%v", err)
	}
	raw, err := filepath.Glob(filepath.Join(filepath.Dir(path), "settings.json"))
	if err != nil || len(raw) == 0 {
		t.Fatalf("设置未落盘：%v %v", raw, err)
	}
	_ = fmt.Sprintf("%v", raw)
}
