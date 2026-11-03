package gateway

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// 控制帧必须被识别成"取消"，而不是当成一次新的 response.create。
func TestWSControlFrameRecognized(t *testing.T) {
	cases := []struct {
		payload  string
		handled  bool
		response string
	}{
		{`{"type":"response.interrupt","response_id":"resp_1"}`, true, "resp_1"},
		{`{"type":"response.cancel","response_id":"resp_2"}`, true, "resp_2"},
		{`{"type":"response.stop"}`, true, ""},
		{`{"type":"response.create","response":{"model":"m"}}`, false, ""},
		{`{"model":"m","input":"hi"}`, false, ""},
		{`not json`, false, ""},
	}
	for _, tc := range cases {
		handled, id := wsControlFrame(nil, []byte(tc.payload))
		if handled != tc.handled || id != tc.response {
			t.Errorf("%s → handled=%v id=%q，期望 %v/%q", tc.payload, handled, id, tc.handled, tc.response)
		}
	}
}

// socket 会话只保留最近 2 轮（客户端只会引用上一轮，留多了等于无界缓存）。
func TestWSSocketKeepsTwoTurns(t *testing.T) {
	s := &wsSocket{}
	for i := 1; i <= 5; i++ {
		s.remember("r"+string(rune('0'+i)), []json.RawMessage{json.RawMessage(`{"n":1}`)})
	}
	if len(s.turns) != wsSocketKeep {
		t.Fatalf("保留 %d 轮，期望 %d", len(s.turns), wsSocketKeep)
	}
	if _, ok := s.lookup("r1"); ok {
		t.Error("最旧的轮次应已被丢弃")
	}
	if _, ok := s.lookup("r5"); !ok {
		t.Error("最新一轮应还在")
	}
}

// 无正在进行的轮次时，interrupt 要明确回"没有可取消的"，而不是静默吞掉。
func TestWSInterruptWithoutActiveTurn(t *testing.T) {
	srv := newTestGateway(t)
	c := dialRawWS(t, srv.Listener.Addr().String())

	if err := c.sendText([]byte(`{"type":"response.interrupt","response_id":"resp_none"}`)); err != nil {
		t.Fatalf("send interrupt: %v", err)
	}
	payload, err := c.readText(5 * time.Second)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(payload), "no_active_response") {
		t.Fatalf("应回 no_active_response，实际 %q", payload)
	}

	// 连接必须仍然可用：取消不该把 socket 弄坏。
	if err := c.sendText([]byte(`{"type":"response.create","response":{"model":"mock-native/echo-pro","input":"after interrupt"}}`)); err != nil {
		t.Fatalf("send after interrupt: %v", err)
	}
	done := false
	for i := 0; i < 40; i++ {
		p, err := c.readText(10 * time.Second)
		if err != nil {
			t.Fatalf("read after interrupt %d: %v", i, err)
		}
		if strings.Contains(string(p), `"response.completed"`) {
			done = true
			break
		}
	}
	if !done {
		t.Fatal("取消之后连接应仍能跑完一轮")
	}
}

// 同一 socket 的第二轮只带 previous_response_id（不带 store）时，
// 必须由 socket 会话直接续上，不依赖 store。
func TestWSSocketHistorySplice(t *testing.T) {
	s := &wsSocket{}
	s.remember("resp_prev", []json.RawMessage{
		json.RawMessage(`{"role":"user","content":"第一轮的问题"}`),
		json.RawMessage(`{"type":"message","role":"assistant","content":"第一轮的回答"}`),
	})

	body := []byte(`{"model":"m","previous_response_id":"resp_prev","input":[{"role":"user","content":"第二轮"}]}`)
	out := s.spliceHistory(body)

	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("splice 结果不是合法 JSON：%v", err)
	}
	if _, ok := m["previous_response_id"]; ok {
		t.Error("命中 socket 会话后应去掉 previous_response_id，避免 store 再查一次")
	}
	var items []map[string]any
	if err := json.Unmarshal(m["input"], &items); err != nil {
		t.Fatalf("input 解析失败：%v", err)
	}
	if len(items) != 3 {
		t.Fatalf("应拼成 3 条（前一轮 input+output + 本轮 input），实际 %d：%v", len(items), items)
	}
	if items[0]["content"] != "第一轮的问题" || items[2]["content"] != "第二轮" {
		t.Fatalf("拼接顺序不对：%v", items)
	}

	// 未命中时必须原样返回（交给 store 路径，store 也没有就报 not_found）。
	raw := []byte(`{"model":"m","previous_response_id":"resp_unknown","input":"x"}`)
	if got := s.spliceHistory(raw); string(got) != string(raw) {
		t.Fatalf("未命中不该改写请求体：%q", got)
	}
}

// input 为字符串时要包成一条 user 消息（Responses 的 EasyInputMessage 形状）。
func TestResponsesInputItemsShapes(t *testing.T) {
	items, err := responsesInputItems(json.RawMessage(`"你好"`))
	if err != nil || len(items) != 1 {
		t.Fatalf("字符串 input 应归一成 1 条：n=%d err=%v", len(items), err)
	}
	var msg map[string]any
	if err := json.Unmarshal(items[0], &msg); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if msg["role"] != "user" || msg["content"] != "你好" {
		t.Fatalf("形状不对：%v", msg)
	}

	arr, err := responsesInputItems(json.RawMessage(`[{"role":"user","content":"a"},{"role":"user","content":"b"}]`))
	if err != nil || len(arr) != 2 {
		t.Fatalf("数组 input 应原样返回 2 条：n=%d err=%v", len(arr), err)
	}
}
