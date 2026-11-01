package reverse

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

type wsCapture struct {
	auth    string
	account string
	beta    string
	origin  string
	body    string
}

func readMaskedFrame(br *bufio.Reader) (byte, []byte, error) {
	h := make([]byte, 2)
	if _, err := io.ReadFull(br, h); err != nil {
		return 0, nil, err
	}
	op := h[0] & 0x0f
	masked := h[1]&0x80 != 0
	n := int64(h[1] & 0x7f)
	if n == 126 {
		var b [2]byte
		if _, err := io.ReadFull(br, b[:]); err != nil {
			return 0, nil, err
		}
		n = int64(binary.BigEndian.Uint16(b[:]))
	} else if n == 127 {
		var b [8]byte
		if _, err := io.ReadFull(br, b[:]); err != nil {
			return 0, nil, err
		}
		n = int64(binary.BigEndian.Uint64(b[:]))
	}
	var mask []byte
	if masked {
		mask = make([]byte, 4)
		if _, err := io.ReadFull(br, mask); err != nil {
			return 0, nil, err
		}
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(br, data); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range data {
			data[i] ^= mask[i%4]
		}
	}
	return op, data, nil
}

func writeServerText(conn net.Conn, payload []byte) error {
	hdr := []byte{0x81}
	n := len(payload)
	if n < 126 {
		hdr = append(hdr, byte(n))
	} else {
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	}
	if _, err := conn.Write(hdr); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
}

func fakeCodexWS(t *testing.T, cap *wsCapture, frames []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		cap.auth = r.Header.Get("Authorization")
		cap.account = r.Header.Get("chatgpt-account-id")
		cap.beta = r.Header.Get("OpenAI-Beta")
		cap.origin = r.Header.Get("originator")
		hj := w.(http.Hijacker)
		conn, brw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		key := r.Header.Get("Sec-WebSocket-Key")
		fmt.Fprintf(brw.Writer, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", wsAccept(key))
		_ = brw.Writer.Flush()
		op, data, err := readMaskedFrame(brw.Reader)
		if err != nil || op != 0x1 {
			return
		}
		cap.body = string(data)
		for _, f := range frames {
			if err := writeServerText(conn, []byte(f)); err != nil {
				return
			}
		}
		// 等客户端读完再关。
		time.Sleep(200 * time.Millisecond)
	}))
}

func TestCodexUpstreamWebSocket(t *testing.T) {
	cap := &wsCapture{}
	frames := []string{
		`{"type":"response.created","response":{"id":"resp_ws"}}`,
		`{"type":"response.output_text.delta","delta":"hi"}`,
		`{"type":"response.completed","response":{"id":"resp_ws","status":"completed"}}`,
	}
	srv := fakeCodexWS(t, cap, frames)
	defer srv.Close()

	p := New(Profile{ID: "codex", NativeResponses: true, DefaultResponsesPath: "/responses"})
	env := provider.NewAccountEnv("ws-1", "codex")
	env.Credentials = map[string]string{
		"base_url": srv.URL, "access_token": "oat-token", "account_id": "acc-7",
	}
	env.HTTPClient = srv.Client()

	rec := httptest.NewRecorder()
	started, err := p.ResponsesWebSocketStream(context.Background(), env,
		[]byte(`{"model":"gpt-5-codex","input":"hello","stream":true}`), "gpt-5-codex", rec)
	if err != nil || !started {
		t.Fatalf("started=%v err=%v", started, err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: response.created") || !strings.Contains(body, "event: response.completed") {
		t.Fatalf("SSE body = %s", body)
	}
	if cap.auth != "Bearer oat-token" || cap.account != "acc-7" || cap.origin != "codex_cli_rs" ||
		!strings.Contains(cap.beta, "responses_websockets") {
		t.Fatalf("headers = %+v", cap)
	}
	if !strings.Contains(cap.body, `"type":"response.create"`) || !strings.Contains(cap.body, `"gpt-5-codex"`) {
		t.Fatalf("upstream body = %s", cap.body)
	}
}

func TestCodexUpstreamWebSocketFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest) // 拒绝升级
	}))
	defer srv.Close()
	p := New(Profile{ID: "codex", NativeResponses: true})
	env := provider.NewAccountEnv("ws-2", "codex")
	env.Credentials = map[string]string{"base_url": srv.URL, "access_token": "t", "account_id": "a"}
	env.HTTPClient = srv.Client()
	rec := httptest.NewRecorder()
	started, err := p.ResponsesWebSocketStream(context.Background(), env, []byte(`{"model":"x"}`), "x", rec)
	if started || err == nil {
		t.Fatalf("应回退 HTTP: started=%v err=%v", started, err)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("回退前不应写响应: %s", rec.Body.String())
	}

	// 中转账号（无 account_id）不走上游 WS。
	env2 := provider.NewAccountEnv("ws-3", "codex")
	env2.Credentials = map[string]string{"base_url": srv.URL, "access_token": "t"}
	if started, _ := p.ResponsesWebSocketStream(context.Background(), env2, []byte(`{"model":"x"}`), "x", rec); started {
		t.Fatal("无 account_id 不应走上游 WS")
	}
}

// 账号配置了代理时，上游 WS 必须走 CONNECT 隧道。
func TestCodexUpstreamWebSocketViaProxy(t *testing.T) {
	cap := &wsCapture{}
	wsSrv := fakeCodexWS(t, cap, []string{`{"type":"response.completed","response":{"id":"resp_p"}}`})
	defer wsSrv.Close()

	var connects int
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		connects++
		target, err := net.Dial("tcp", r.Host)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		hj := w.(http.Hijacker)
		client, _, err := hj.Hijack()
		if err != nil {
			_ = target.Close()
			return
		}
		_, _ = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go io.Copy(target, client)
		_, _ = io.Copy(client, target)
		_ = target.Close()
		_ = client.Close()
	}))
	defer proxy.Close()

	proxyURL, _ := url.Parse(proxy.URL)
	p := New(Profile{ID: "codex", NativeResponses: true})
	env := provider.NewAccountEnv("ws-proxy", "codex")
	env.Credentials = map[string]string{"base_url": wsSrv.URL, "access_token": "t", "account_id": "a"}
	env.HTTPClient = &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	rec := httptest.NewRecorder()
	started, err := p.ResponsesWebSocketStream(context.Background(), env, []byte(`{"model":"gpt-5-codex","input":"hi"}`), "gpt-5-codex", rec)
	if err != nil || !started {
		t.Fatalf("started=%v err=%v", started, err)
	}
	if connects != 1 {
		t.Fatalf("代理 CONNECT 次数 = %d", connects)
	}
	if !strings.Contains(rec.Body.String(), "response.completed") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

// HTTPS 代理（TLS 到代理）也要能 CONNECT 隧道。
func TestCodexUpstreamWebSocketViaHTTPSProxy(t *testing.T) {
	cap := &wsCapture{}
	wsSrv := fakeCodexWS(t, cap, []string{`{"type":"response.completed","response":{"id":"resp_tls"}}`})
	defer wsSrv.Close()

	var connects int
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		connects++
		target, err := net.Dial("tcp", r.Host)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		hj := w.(http.Hijacker)
		client, _, err := hj.Hijack()
		if err != nil {
			_ = target.Close()
			return
		}
		_, _ = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go io.Copy(target, client)
		_, _ = io.Copy(client, target)
		_ = target.Close()
		_ = client.Close()
	}))
	defer proxy.Close()

	oldCfg := wsProxyTLSConfig
	wsProxyTLSConfig = func() *tls.Config { return &tls.Config{InsecureSkipVerify: true} }
	defer func() { wsProxyTLSConfig = oldCfg }()

	proxyURL, _ := url.Parse(proxy.URL) // https://...
	p := New(Profile{ID: "codex", NativeResponses: true})
	env := provider.NewAccountEnv("ws-tls-proxy", "codex")
	env.Credentials = map[string]string{"base_url": wsSrv.URL, "access_token": "t", "account_id": "a"}
	env.HTTPClient = &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	rec := httptest.NewRecorder()
	started, err := p.ResponsesWebSocketStream(context.Background(), env, []byte(`{"model":"gpt-5-codex","input":"hi"}`), "gpt-5-codex", rec)
	if err != nil || !started {
		t.Fatalf("started=%v err=%v", started, err)
	}
	if connects != 1 || !strings.Contains(rec.Body.String(), "response.completed") {
		t.Fatalf("connects=%d body=%s", connects, rec.Body.String())
	}
}

func TestResolveWSEndpoint(t *testing.T) {
	cases := []struct{ base, endpoint, want string }{
		{"https://api.example.com/v1", "/responses", "wss://api.example.com/v1/responses"},
		{"http://127.0.0.1:8080", "responses", "ws://127.0.0.1:8080/responses"},
		{"", "wss://ws.example.com/chat", "wss://ws.example.com/chat"},
		{"https://api.example.com", "https://other.example.com/responses", "wss://other.example.com/responses"},
		{"https://api.example.com", "", ""},
	}
	for _, tc := range cases {
		if got := resolveWSEndpoint(tc.base, tc.endpoint); got != tc.want {
			t.Fatalf("resolve(%q,%q)=%q want %q", tc.base, tc.endpoint, got, tc.want)
		}
	}
}

// TestGenericResponsesWebSocket 验证非 Codex 渠道也能直通上游 Responses WS：
// 端点由 Profile.ResponsesWSEndpoint 声明，鉴权复用 HTTP 管线的 spec.chat.auth。
func TestGenericResponsesWebSocket(t *testing.T) {
	cap := &wsCapture{}
	frames := []string{
		`{"type":"response.created","response":{"id":"resp_relay"}}`,
		`{"type":"response.output_text.delta","delta":"ok"}`,
		`{"type":"response.completed","response":{"id":"resp_relay","status":"completed"}}`,
	}
	srv := fakeCodexWS(t, cap, frames)
	defer srv.Close()

	p := New(Profile{ID: "ws-relay", NativeResponses: true, ResponsesWSEndpoint: "/responses"})
	env := provider.NewAccountEnv("relay-ws", "ws-relay")
	env.Credentials = map[string]string{"base_url": srv.URL, "api_key": "sk-relay"}
	env.HTTPClient = srv.Client()

	rec := httptest.NewRecorder()
	started, err := p.ResponsesWebSocketStream(context.Background(), env,
		[]byte(`{"model":"relay-model","input":"hi","stream":true}`), "relay-model", rec)
	if err != nil || !started {
		t.Fatalf("started=%v err=%v", started, err)
	}
	if !strings.Contains(rec.Body.String(), "event: response.completed") {
		t.Fatalf("SSE body = %s", rec.Body.String())
	}
	if cap.auth != "Bearer sk-relay" {
		t.Fatalf("通用渠道鉴权头 = %q", cap.auth)
	}
	if cap.account != "" || cap.origin != "" {
		t.Fatalf("通用渠道不应带 Codex 专属头: %+v", cap)
	}
	if !strings.Contains(cap.body, `"type":"response.create"`) || !strings.Contains(cap.body, `"relay-model"`) {
		t.Fatalf("upstream body = %s", cap.body)
	}
}

func TestGenericWSOffWhenNoEndpoint(t *testing.T) {
	p := New(Profile{ID: "no-ws"})
	env := provider.NewAccountEnv("a", "no-ws")
	env.Credentials = map[string]string{"api_key": "x"}
	rec := httptest.NewRecorder()
	started, err := p.ResponsesWebSocketStream(context.Background(), env, []byte(`{}`), "m", rec)
	if started || err != nil {
		t.Fatalf("未声明端点应直接回退: started=%v err=%v", started, err)
	}
}
