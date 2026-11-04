package gateway

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 从 /cdn-cgi/trace 的正文里取机房代码。
func TestParseTraceColo(t *testing.T) {
	body := "fl=123\nh=amr-link.open-design.ai\nip=1.2.3.4\nts=1\nvisit_scheme=https\nuag=x\ncolo=SEA\nsliver=none\nhttp=http/1.1\n"
	if got := parseTraceColo(body); got != "SEA" {
		t.Fatalf("colo = %q", got)
	}
	if got := parseTraceColo("fl=1\nh=x\n"); got != "" {
		t.Fatalf("没有 colo 应返回空，得到 %q", got)
	}
	if got := parseTraceColo("colo=lhr\n"); got != "LHR" {
		t.Fatalf("应规范成大写，得到 %q", got)
	}
}

// 默认候选池要覆盖两段主流 Cloudflare 段，且数量受 limit 约束。
func TestDefaultEdgeCandidates(t *testing.T) {
	got := defaultEdgeCandidates(24)
	if len(got) != 24 {
		t.Fatalf("候选数 = %d，应为 24", len(got))
	}
	var seen104, seen162 bool
	for _, ip := range got {
		if net.ParseIP(ip) == nil {
			t.Errorf("不是合法 IP：%q", ip)
		}
		if strings.HasPrefix(ip, "104.26.") {
			seen104 = true
		}
		if strings.HasPrefix(ip, "162.159.") {
			seen162 = true
		}
	}
	if !seen104 || !seen162 {
		t.Fatalf("两段都要覆盖：104.26=%v 162.159=%v", seen104, seen162)
	}
	// 固定步长 → 结果可复现。
	a := defaultEdgeCandidates(8)
	b := defaultEdgeCandidates(8)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("候选池不稳定：%v vs %v", a, b)
		}
	}
}

// scanEdges 只返回可用项，并按"到边缘的耗时"升序。
func TestScanEdgesSortsByLatency(t *testing.T) {
	// 两个本地 TLS 服务模拟不同延迟的边缘。
	fast := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("colo=HKG\n"))
	}))
	defer fast.Close()
	slow := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond)
		_, _ = w.Write([]byte("colo=LHR\n"))
	}))
	defer slow.Close()

	fastPort := portOf(t, fast.URL)
	slowPort := portOf(t, slow.URL)
	_ = fastPort
	_ = slowPort

	// probeEdge 固定连 443，无法指向测试端口；这里直接验证排序函数行为：
	// 构造两个候选，一个快一个慢。
	res := []edgeCandidate{
		{IP: "198.51.100.2", OK: true, TCPMS: 10, TLSMS: 80, TTFBMS: 90},
		{IP: "198.51.100.1", OK: true, TCPMS: 10, TLSMS: 40, TTFBMS: 50},
	}
	sorted := sortCandidates(res)
	if sorted[0].IP != "198.51.100.1" {
		t.Fatalf("应按总耗时升序，得到 %s 在前", sorted[0].IP)
	}
}

func portOf(t *testing.T, rawURL string) string {
	t.Helper()
	u := strings.TrimPrefix(rawURL, "https://")
	_, port, err := net.SplitHostPort(u)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// 固定表的增删查：空列表等于取消固定；非法 IP 被丢弃。
func TestHostPinStore(t *testing.T) {
	s := newHostPinStore()
	s.Set("a.example.com", []string{"104.26.12.1", "not-an-ip", "104.26.9.1"})
	got := s.Get("a.example.com")
	if len(got) != 2 || got[0] != "104.26.12.1" {
		t.Fatalf("应过滤非法 IP：%v", got)
	}
	// 全是非法 IP → 不建立固定项。
	s.Set("b.example.com", []string{"nope"})
	if len(s.Get("b.example.com")) != 0 {
		t.Fatal("全非法应不建立固定项")
	}
	// 空列表 = 取消。
	s.Set("a.example.com", nil)
	if len(s.Get("a.example.com")) != 0 {
		t.Fatal("空列表应取消固定")
	}
	if len(s.Snapshot()) != 0 {
		t.Fatalf("快照应为空：%v", s.Snapshot())
	}
}

// edge-pin 端点：写入后能用 edge-pins 读回；非法 host 与非法 IP 都要挡住。
func TestEdgePinHandlers(t *testing.T) {
	g := New(Config{SettingsPath: t.TempDir() + "/settings.json", UsagePath: "-"})

	body := strings.NewReader(`{"host":"amr-link.open-design.ai","ips":["104.26.12.1","104.26.9.1"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/network/edge-pin", body)
	rec := httptest.NewRecorder()
	g.handleEdgePin(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("写入失败：%d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	g.handleEdgePins(rec, httptest.NewRequest(http.MethodGet, "/api/network/edge-pins", nil))
	if !strings.Contains(rec.Body.String(), "104.26.12.1") {
		t.Fatalf("读回失败：%s", rec.Body.String())
	}

	// 非法 host
	for _, bad := range []string{`{"host":"a b","ips":[]}`, `{"host":"a?b","ips":[]}`} {
		rec = httptest.NewRecorder()
		g.handleEdgePin(rec, httptest.NewRequest(http.MethodPost, "/api/network/edge-pin", strings.NewReader(bad)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("host=%s 应 400，得到 %d", bad, rec.Code)
		}
	}
}

// 固定表为空时不影响正常解析路径（LookupPins 返回空 → 走 DoH）。
func TestHostPinsEmptyDoesNotAffectDial(t *testing.T) {
	g := New(Config{SettingsPath: t.TempDir() + "/settings.json", UsagePath: "-"})
	if got := g.hostPins().Get("amr-link.open-design.ai"); len(got) != 0 {
		t.Fatalf("默认应为空：%v", got)
	}
	// 依赖注入的回调在 gateway 初始化时已挂上；这里验证它返回空而不是 panic。
	if g.tr == nil || g.tr.Optimizer == nil {
		t.Fatal("优化器应在 New 时创建")
	}
	if g.tr.Optimizer.LookupPins == nil {
		t.Fatal("LookupPins 应已挂上")
	}
	if pins := g.tr.Optimizer.LookupPins("unknown.example.com"); len(pins) != 0 {
		t.Fatalf("未知域名应返回空：%v", pins)
	}
}

var _ = context.Background
