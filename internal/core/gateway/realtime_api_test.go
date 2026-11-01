package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"aether-cockpit/internal/core/tools"
)

func TestRealtimeSessionProxy(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/realtime/sessions" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"client_secret":{"value":"ek_1"},"model":"gpt-realtime"}`))
	}))
	defer upstream.Close()

	g := New(Config{Addr: "127.0.0.1:0", UsagePath: "-"})
	g.settings.Tools = &ToolsConfig{Realtime: &tools.RealtimeConfig{BaseURL: upstream.URL, APIKey: "rt-key"}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/realtime/sessions", strings.NewReader(`{"model":"gpt-realtime"}`))
	g.handleRealtimeSession(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ek_1") {
		t.Fatalf("sessions = %d %s", rec.Code, rec.Body.String())
	}
	if gotAuth != "Bearer rt-key" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}

// TestRealtimeWSBridge 起一个回声上游，验证网关双向转发保留帧类型、
// 补默认 model 并透传 Authorization。
func TestRealtimeWSBridge(t *testing.T) {
	upgrader := websocket.Upgrader{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/realtime" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("model"); got != "gpt-realtime" {
			t.Errorf("上游 model = %q（应补默认模型）", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer rt-key" {
			t.Errorf("上游 Authorization = %q", got)
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			mt, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(mt, append([]byte("echo:"), data...)); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()

	g := New(Config{Addr: "127.0.0.1:0", UsagePath: "-"})
	g.settings.Tools = &ToolsConfig{Realtime: &tools.RealtimeConfig{
		BaseURL: upstream.URL, APIKey: "rt-key", Model: "gpt-realtime",
	}}
	srv := httptest.NewServer(g)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/realtime"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("连接网关 WS 失败: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatalf("发送失败: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	mt, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取回声失败: %v", err)
	}
	if mt != websocket.TextMessage || string(data) != "echo:ping" {
		t.Fatalf("回声 = type %d %q", mt, data)
	}

	// 二进制帧也要原样转发（实时语音的音频分片）。
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte{0x01, 0x02}); err != nil {
		t.Fatalf("发送二进制失败: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	mt, data, err = conn.ReadMessage()
	if err != nil || mt != websocket.BinaryMessage || len(data) != len("echo:")+2 {
		t.Fatalf("二进制回声 = type %d len %d err %v", mt, len(data), err)
	}
}

func TestRealtimeWSURL(t *testing.T) {
	got, err := realtimeWSURL("https://api.openai.com/v1/", map[string][]string{"model": {"x"}})
	if err != nil || got != "wss://api.openai.com/v1/realtime?model=x" {
		t.Fatalf("url = %q err=%v", got, err)
	}
	if _, err := realtimeWSURL("ftp://x", nil); err == nil {
		t.Fatal("非法协议应报错")
	}
}
