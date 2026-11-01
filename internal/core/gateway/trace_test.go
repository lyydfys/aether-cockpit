package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func TestTraceRecordsTriesAndTotals(t *testing.T) {
	g := &Gateway{traces: newTraceStore()}
	envA := provider.NewAccountEnv("acc-a", "mock-native")
	envA.Label = "主账号"
	envB := provider.NewAccountEnv("acc-b", "mock-native")
	envB.Label = "备用账号"

	r := g.traces.begin("claude-code", "mock-native/echo-pro", "sess-1")
	r.addSeat(traceSeat{ID: envA.AccountID, Kind: "account", Provider: "mock-native", Who: envA.Label, Model: "echo-pro"})
	r.addSeat(traceSeat{ID: envB.AccountID, Kind: "account", Provider: "mock-native", Who: envB.Label, Model: "echo-pro"})
	r.addTry(envA, "echo-pro", time.Now().Add(-2*time.Second), 429, "rate", false)
	r.addTry(envB, "echo-pro", time.Now(), 200, "", true)
	r.finish() // 真实请求在 serve 结束时收尾（done/status/ms）

	req := httptest.NewRequest("GET", "/api/gateway/trace?after=0", nil)
	rec := httptest.NewRecorder()
	g.handleGatewayTrace(rec, req)

	var resp struct {
		Seq    int64        `json:"seq"`
		Routes []traceRoute `json:"routes"`
		Totals struct {
			Requests int64 `json:"requests"`
			Rerouted int64 `json:"rerouted"`
		} `json:"totals"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if resp.Seq != 1 || len(resp.Routes) != 1 {
		t.Fatalf("seq/routes = %d/%d", resp.Seq, len(resp.Routes))
	}
	got := resp.Routes[0]
	if got.Agent != "claude" {
		t.Errorf("agent = %q, want claude（页面配色按 claude 识别）", got.Agent)
	}
	if len(got.Tries) != 2 || got.Tries[0].Status != 429 || got.Tries[0].Fail != "rate" {
		t.Errorf("tries = %+v", got.Tries)
	}
	if !got.Tries[1].Picked || !got.Done {
		t.Errorf("命中的尝试未标记: %+v done=%v", got.Tries[1], got.Done)
	}
	if len(got.Left) != 0 {
		t.Errorf("两个账号都试过，left 应为空: %+v", got.Left)
	}
	if resp.Totals.Requests != 1 || resp.Totals.Rerouted != 1 {
		t.Errorf("totals = %+v", resp.Totals)
	}
}

func TestTraceLongPollWakesOnNewRoute(t *testing.T) {
	g := &Gateway{traces: newTraceStore()}
	done := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		r := g.traces.begin("codex", "m", "")
		r.finish()
		close(done)
	}()
	start := time.Now()
	req := httptest.NewRequest("GET", "/api/gateway/trace?after=0&wait=1", nil)
	rec := httptest.NewRecorder()
	g.handleGatewayTrace(rec, req)
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("长轮询没有在新轨迹时唤醒，耗时 %v", took)
	}
	<-done
	var resp struct {
		Routes []traceRoute `json:"routes"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Routes) != 1 {
		t.Fatalf("routes = %d, want 1", len(resp.Routes))
	}
}

func TestTraceFailCode(t *testing.T) {
	cases := []struct {
		code   string
		status int
		fail   string
	}{
		{ir.ErrCodeUpstreamRateLimit, 429, "rate"},
		{ir.ErrCodeUpstreamAuth, 401, "auth"},
		{ir.ErrCodeBadRequest, 400, "shape"},
		{ir.ErrCodeUpstreamFailed, 502, "other"},
	}
	for _, c := range cases {
		status, fail := traceFailCode(ir.NewAPIError(c.code, "x"))
		if status != c.status || fail != c.fail {
			t.Errorf("%s -> %d/%s, want %d/%s", c.code, status, fail, c.status, c.fail)
		}
	}
}
