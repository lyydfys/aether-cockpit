package minimax

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func testEnv(srv *httptest.Server) *provider.AccountEnv {
	env := provider.NewAccountEnv("acc", "minimax-code")
	env.Credentials = map[string]string{
		"access_token":      "tok",
		"user_id":           "u1",
		"base_url":          srv.URL,
		"agent_base_url":    srv.URL,
		"platform_base_url": srv.URL,
	}
	env.HTTPClient = srv.Client()
	return env
}

func collect(t *testing.T, ch <-chan ir.UnifiedStreamEvent) []ir.UnifiedStreamEvent {
	t.Helper()
	var out []ir.UnifiedStreamEvent
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

func TestChatStreamSSE(t *testing.T) {
	var gotAuth, gotAPIKey, gotUA, gotAgent, gotVersion, gotSession, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("x-api-key")
		gotUA = r.Header.Get("User-Agent")
		gotAgent = r.Header.Get("X-Mavis-Agent-Id")
		gotVersion = r.Header.Get("anthropic-version")
		gotSession = r.Header.Get("X-Mavis-Session-Id")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":10,"cache_read_input_tokens":2,"cache_creation_input_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`)
	}))
	defer srv.Close()

	p := &Provider{profile: cnProfile}
	req := &ir.UnifiedChatRequest{
		Model: "minimax-code/MiniMax-M3", UpstreamModel: "MiniMax-M3", Stream: true, SessionKey: "sess-1",
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}
	ch, err := p.ChatStream(context.Background(), testEnv(srv), req)
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, ch)
	if gotPath != "/mavis/api/v1/llm/v1/messages" || gotAuth != "Bearer tok" || gotAPIKey != "sk-xxx" ||
		gotUA != "MiniMaxAgent" || gotAgent != "main" || gotVersion != "2023-06-01" || gotSession != "sess-1" {
		t.Fatalf("headers/path: %q %q %q %q %q %q %q", gotPath, gotAuth, gotAPIKey, gotUA, gotAgent, gotVersion, gotSession)
	}
	var text string
	var usage *ir.Usage
	var done ir.UnifiedStreamEvent
	for _, ev := range events {
		switch ev.Type {
		case ir.EventTextDelta:
			text += ev.TextDelta
		case ir.EventUsage:
			usage = ev.Usage
		case ir.EventDone:
			done = ev
		}
	}
	if text != "hello" {
		t.Fatalf("text = %q", text)
	}
	if usage == nil || usage.InputTokens != 10 || usage.CachedTokens != 2 || usage.CacheCreationTokens != 1 || usage.OutputTokens != 5 || usage.TotalTokens != 18 {
		t.Fatalf("usage = %+v", usage)
	}
	if done.FinishReason != "stop" {
		t.Fatalf("done = %+v", done)
	}
}

func TestChatStreamToolUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `event: message_start
data: {"type":"message_start","message":{"id":"msg_2","usage":{"input_tokens":3}}}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"SZ\"}"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}

event: message_stop
data: {"type":"message_stop"}
`)
	}))
	defer srv.Close()

	p := &Provider{profile: cnProfile}
	ch, err := p.ChatStream(context.Background(), testEnv(srv), &ir.UnifiedChatRequest{
		Model: "minimax-code/MiniMax-M3", UpstreamModel: "MiniMax-M3", Stream: true,
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "weather?"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var start, delta bool
	args := ""
	finish := ""
	for _, ev := range collect(t, ch) {
		switch ev.Type {
		case ir.EventToolCallStart:
			start = ev.ToolCallID == "toolu_1" && ev.ToolName == "get_weather"
		case ir.EventToolCallDelta:
			delta = ev.ToolCallID == "toolu_1"
			args += ev.ArgsDelta
		case ir.EventDone:
			finish = ev.FinishReason
		}
	}
	if !start || !delta || args != `{"city":"SZ"}` || finish != "tool_calls" {
		t.Fatalf("start=%v delta=%v args=%q finish=%q", start, delta, args, finish)
	}
}

func TestChatRequestBody(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = jsonDecode(r.Body, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_3","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`)
	}))
	defer srv.Close()

	p := &Provider{profile: cnProfile}
	temp := 0.3
	max := 64
	req := &ir.UnifiedChatRequest{
		Model: "minimax-code/MiniMax-M3", UpstreamModel: "MiniMax-M3", Stream: false,
		Instructions: "be brief", Temperature: &temp, MaxOutputTokens: &max,
		Tools: []ir.UnifiedToolSpec{{Name: "echo", Description: "d", Parameters: []byte(`{"type":"object"}`)}},
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"},
			{Type: ir.ItemTypeFunctionCall, CallID: "c1", Name: "echo", Arguments: `{"x":1}`},
			{Type: ir.ItemTypeFunctionCallOutput, CallID: "c1", Output: "1"},
		},
	}
	ch, err := p.ChatStream(context.Background(), testEnv(srv), req)
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, ch)
	if body["model"] != "MiniMax-M3" || body["max_tokens"].(float64) != 64 || body["system"] != "be brief" {
		t.Fatalf("body = %#v", body)
	}
	if len(body["tools"].([]any)) != 1 {
		t.Fatalf("tools = %#v", body["tools"])
	}
	msgs := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %#v", msgs)
	}
	if events[len(events)-1].Type != ir.EventDone {
		t.Fatalf("events = %#v", events)
	}
}

func TestCheckinClaim(t *testing.T) {
	var statusOK, claimOK, sigOK bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("device_platform") != "web" || q.Get("is_desktop") != "1" || q.Get("desktop_version") != "0.6.12" || q.Get("user_id") != "u1" {
			t.Errorf("query = %v", q)
		}
		ts := r.Header.Get("x-timestamp")
		sig := r.Header.Get("x-signature")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/minimax-cloud/api/v1/signin/status":
			statusOK = true
			sum := md5.Sum([]byte(ts + signSecret))
			sigOK = sig == hex.EncodeToString(sum[:])
			_, _ = io.WriteString(w, `{"data":{"days":[
				{"day_no":1,"status":3,"points":5},
				{"day_no":2,"status":2,"points":10,"is_today":true},
				{"day_no":3,"status":1},{"day_no":4,"status":1},{"day_no":5,"status":1},{"day_no":6,"status":1},{"day_no":7,"status":1}
			]}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/minimax-cloud/api/v1/signin/claim":
			claimOK = true
			body, _ := io.ReadAll(r.Body)
			sum := md5.Sum([]byte(ts + signSecret + string(body)))
			if sig != hex.EncodeToString(sum[:]) || string(body) != "{}" {
				t.Errorf("claim signature/body: %q %q", sig, body)
			}
			_, _ = io.WriteString(w, `{"data":{"claim_result":1,"points":10}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := &Provider{profile: cnProfile}
	report, err := p.ExecuteTasks(context.Background(), testEnv(srv), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !statusOK || !claimOK || !sigOK {
		t.Fatalf("status=%v claim=%v sig=%v", statusOK, claimOK, sigOK)
	}
	if report.Outcome != provider.CheckinClaimed || report.Credit != 10 || report.Streak != 2 {
		t.Fatalf("report = %+v", report)
	}
}

func TestCheckinDoneAndRefused(t *testing.T) {
	cases := []struct {
		body    string
		outcome string
	}{
		{`{"data":{"days":[{"day_no":1,"status":3,"is_today":true}]}}`, provider.CheckinDone},
		{`{"data":{"days":[{"day_no":1,"status":4,"is_today":true}]}}`, provider.CheckinIneligible},
		{`{"data":{"days":[{"day_no":1,"status":1,"is_today":true}]}}`, provider.CheckinInactive},
		{`{"statusInfo":{"code":1000048,"message":"invalid access token"}}`, provider.CheckinFailed},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, tc.body)
		}))
		p := &Provider{profile: cnProfile}
		report, _ := p.ExecuteTasks(context.Background(), testEnv(srv), nil)
		srv.Close()
		if report.Outcome != tc.outcome {
			t.Fatalf("%s → %q, want %q", tc.body, report.Outcome, tc.outcome)
		}
	}
}

func TestManifest(t *testing.T) {
	for _, id := range []string{"minimax-code", "minimax-code-global"} {
		p, ok := provider.Get(id)
		if !ok {
			t.Fatalf("%s 未注册", id)
		}
		m := p.Manifest()
		if m.ID != id || m.Family != "minimax" || len(m.Models) != 4 || len(m.Tasks) != 1 || !m.Capabilities.CheckIn {
			t.Fatalf("%s manifest = %+v", id, m)
		}
	}
}

func jsonDecode(r io.Reader, v any) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func TestQueryQuota(t *testing.T) {
	var gotWorkspaceBody, gotMembershipBody, gotGroup string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/matrix/api/v1/user/get_user_extra_info":
			b, _ := io.ReadAll(r.Body)
			gotWorkspaceBody = string(b)
			_, _ = io.WriteString(w, `{"workspaces":[{"workspace_id":12,"workspace_type":0},{"workspace_id":99,"workspace_type":1}]}`)
		case "/matrix/api/v1/commerce/get_membership_info":
			b, _ := io.ReadAll(r.Body)
			gotMembershipBody = string(b)
			_, _ = io.WriteString(w, `{"has_token_plan":true,"token_plan_tier":"Max","token_plan_expires_at":1893456000000,"op_group_id":"g1","op_credit_summary":{"total_remaining_amount":"88.5"}}`)
		case "/minimax-cloud/api/v1/credit/details":
			now := time.Now().UnixMilli()
			_, _ = io.WriteString(w, fmt.Sprintf(`{"total_count":4,"details":[
				{"credit_type":1,"granted_amount":"1,000.5","remaining_amount":"400.5","expire_at_ms":%d},
				{"credit_type":2,"granted_amount":"10","remaining_amount":"10","expire_at_ms":%d},
				{"credit_type":1,"granted_amount":"50","remaining_amount":"50","expire_at_ms":%d},
				{"credit_type":3,"granted_amount":"999","remaining_amount":"999","expire_at_ms":%d}
			]}`, now+86400_000, now+2*86400_000, now-86400_000, now+86400_000))
		case "/v1/api/openplatform/coding_plan/remains":
			gotGroup = r.Header.Get("X-Group-Id")
			now := time.Now().UnixMilli()
			_, _ = io.WriteString(w, fmt.Sprintf(`{"model_remains":[
				{"model_name":"general",
				 "current_interval_remaining_percent":40,"current_interval_status":1,
				 "start_time":%d,"end_time":%d,
				 "current_weekly_remaining_percent":80,"current_weekly_status":1,
				 "weekly_start_time":%d,"weekly_end_time":%d},
				{"model_name":"opus","current_interval_remaining_percent":0,
				 "current_interval_status":2,"start_time":%d,"end_time":%d}
			]}`, now-3600_000, now+4*3600_000, now-86400_000, now+6*86400_000, now-3600_000, now+4*3600_000))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := &Provider{profile: cnProfile}
	q, err := p.QueryQuota(context.Background(), testEnv(srv))
	if err != nil {
		t.Fatal(err)
	}
	if q.Balance != 88.5 || q.BalanceUnit != "积分" || q.Plan != "Max" || q.Until != 1893456000 {
		t.Fatalf("quota = %+v", q)
	}
	if gotWorkspaceBody != "{}" || gotMembershipBody != `{"workspace_id":12}` || gotGroup != "g1" {
		t.Fatalf("bodies = %q / %q group=%q", gotWorkspaceBody, gotMembershipBody, gotGroup)
	}
	if len(q.Windows) != 4 {
		t.Fatalf("windows = %#v", q.Windows)
	}
	if q.Windows[0].Name != "5 hours" || q.Windows[0].Used != 60 || q.Windows[0].Aside {
		t.Fatalf("interval = %#v", q.Windows[0])
	}
	if q.Windows[1].Name != "7 days" || q.Windows[1].Used != 20 {
		t.Fatalf("weekly = %#v", q.Windows[1])
	}
	if q.Windows[2].Name != "Opus · 5 hours" || q.Windows[2].Used != 100 || !q.Windows[2].Aside {
		t.Fatalf("opus = %#v", q.Windows[2])
	}
	cw := q.Windows[3]
	if cw.Name != "Credits" || !cw.Aside || cw.Unit != "credits" || cw.Limit != 1011 || cw.Amount != 600 {
		t.Fatalf("credits = %#v", cw)
	}
	if cw.Used < 59.3 || cw.Used > 59.5 || cw.ResetsAt == 0 {
		t.Fatalf("credits used/reset = %#v", cw)
	}
}

func TestCreditWindowClampsNegative(t *testing.T) {
	now := time.Now().UnixMilli()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, fmt.Sprintf(`{"total_count":1,"details":[
			{"credit_type":1,"granted_amount":"100","remaining_amount":"-5","expire_at_ms":%d}
		]}`, now+3600_000))
	}))
	defer srv.Close()
	w := (&Provider{profile: cnProfile}).creditWindow(context.Background(), testEnv(srv), "tok", "u1")
	if w == nil || w.Used != 100 || w.Amount != 100 || w.Limit != 100 {
		t.Fatalf("window = %#v", w)
	}
}

func TestQueryQuotaPlanFailureKeepsBalance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/matrix/api/v1/user/get_user_extra_info":
			_, _ = io.WriteString(w, `{"workspaces":[{"workspace_id":12,"workspace_type":0}]}`)
		case "/matrix/api/v1/commerce/get_membership_info":
			_, _ = io.WriteString(w, `{"has_token_plan":true,"token_plan_tier":"Max","op_credit_summary":{"total_remaining_amount":"5"}}`)
		default:
			http.Error(w, "down", 500)
		}
	}))
	defer srv.Close()
	q, err := (&Provider{profile: cnProfile}).QueryQuota(context.Background(), testEnv(srv))
	if err != nil {
		t.Fatal(err)
	}
	if q.Balance != 5 || len(q.Windows) != 0 {
		t.Fatalf("quota = %+v", q)
	}
}

func TestQueryQuotaAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"statusInfo":{"code":1000048,"message":"invalid access token"}}`)
	}))
	defer srv.Close()
	p := &Provider{profile: cnProfile}
	if _, err := p.QueryQuota(context.Background(), testEnv(srv)); err == nil {
		t.Fatal("应返回鉴权错误")
	}
}
