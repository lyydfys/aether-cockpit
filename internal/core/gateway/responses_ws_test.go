package gateway

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

var responseIDRe = regexp.MustCompile(`"id":"(resp_[A-Za-z0-9]+)"`)

// rawWSClient 是测试用的最小 WebSocket 客户端（客户端帧必须加掩码）。
type rawWSClient struct {
	conn net.Conn
	br   *bufio.Reader
}

func dialRawWS(t *testing.T, addr string) *rawWSClient {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	req := fmt.Sprintf("GET /v1/responses HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", addr, key)
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatalf("handshake write: %v", err)
	}
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("handshake read: %v", err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("status = %q", strings.TrimSpace(status))
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("handshake headers: %v", err)
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &rawWSClient{conn: conn, br: br}
}

func (c *rawWSClient) sendText(payload []byte) error {
	return c.sendFrame(wsOpText, true, payload)
}

// sendFrame 发送一帧（fin=false 用于分片）。
func (c *rawWSClient) sendFrame(op byte, fin bool, payload []byte) error {
	first := byte(0x00)
	if fin {
		first = 0x80
	}
	mask := []byte{0x11, 0x22, 0x33, 0x44}
	hdr := []byte{first | op}
	n := len(payload)
	switch {
	case n < 126:
		hdr = append(hdr, 0x80|byte(n))
	case n <= 65535:
		hdr = append(hdr, 0x80|126, byte(n>>8), byte(n))
	default:
		return fmt.Errorf("payload too large")
	}
	hdr = append(hdr, mask...)
	masked := make([]byte, n)
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	if _, err := c.conn.Write(hdr); err != nil {
		return err
	}
	_, err := c.conn.Write(masked)
	return err
}

func (c *rawWSClient) readText(timeout time.Duration) ([]byte, error) {
	op, data, err := c.readFrameOp(timeout)
	if err != nil {
		return nil, err
	}
	if op == wsOpClose {
		return nil, io.EOF
	}
	if op != wsOpText {
		return c.readText(timeout)
	}
	return data, nil
}

// readFrameOp 读一帧并返回 opcode 与载荷（测试 ping/pong 用）。
func (c *rawWSClient) readFrameOp(timeout time.Duration) (byte, []byte, error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(timeout))
	h := make([]byte, 2)
	if _, err := io.ReadFull(c.br, h); err != nil {
		return 0, nil, err
	}
	op := h[0] & 0x0f
	n := int64(h[1] & 0x7f)
	switch n {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(c.br, b[:]); err != nil {
			return 0, nil, err
		}
		n = int64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(c.br, b[:]); err != nil {
			return 0, nil, err
		}
		n = int64(binary.BigEndian.Uint64(b[:]))
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(c.br, data); err != nil {
		return 0, nil, err
	}
	return op, data, nil
}

func TestResponsesWebSocketTurn(t *testing.T) {
	srv := newTestGateway(t)
	c := dialRawWS(t, srv.Listener.Addr().String())

	if err := c.sendText([]byte(`{"type":"response.create","response":{"model":"mock-native/echo-pro","input":"ws hello"}}`)); err != nil {
		t.Fatalf("send: %v", err)
	}

	var events []string
	completed := false
	for i := 0; i < 50; i++ {
		payload, err := c.readText(15 * time.Second)
		if err != nil {
			t.Fatalf("read frame %d: %v (events=%v)", i, err, events)
		}
		events = append(events, string(payload))
		if strings.Contains(string(payload), `"response.completed"`) {
			completed = true
			break
		}
	}
	if len(events) == 0 || !completed {
		t.Fatalf("WS 事件不完整: %v", events)
	}
	// 第二条消息复用同一连接。
	if err := c.sendText([]byte(`{"model":"mock-native/echo-pro","input":"second turn"}`)); err != nil {
		t.Fatalf("send second: %v", err)
	}
	completed = false
	for i := 0; i < 50; i++ {
		payload, err := c.readText(15 * time.Second)
		if err != nil {
			t.Fatalf("read second turn: %v", err)
		}
		if strings.Contains(string(payload), `"response.completed"`) {
			completed = true
			break
		}
	}
	if !completed {
		t.Fatal("第二轮没有完成事件")
	}
}

func TestResponsesWebSocketRequiresUpgrade(t *testing.T) {
	srv := newTestGateway(t)
	resp, err := http.Get(srv.URL + "/v1/responses")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("status = %d, want 426", resp.StatusCode)
	}
}

// 分片消息必须拼成一条完整请求；ping 必须回 pong。
func TestResponsesWebSocketFragmentsAndPing(t *testing.T) {
	srv := newTestGateway(t)
	c := dialRawWS(t, srv.Listener.Addr().String())

	if err := c.sendFrame(wsOpPing, true, []byte("h")); err != nil {
		t.Fatalf("ping: %v", err)
	}
	op, payload, err := c.readFrameOp(5 * time.Second)
	if err != nil {
		t.Fatalf("pong read: %v", err)
	}
	if op != wsOpPong || string(payload) != "h" {
		t.Fatalf("pong = op %d payload %q", op, payload)
	}

	body := []byte(`{"model":"mock-native/echo-pro","input":"fragmented hello"}`)
	half := len(body) / 2
	if err := c.sendFrame(wsOpText, false, body[:half]); err != nil {
		t.Fatalf("fragment 1: %v", err)
	}
	if err := c.sendFrame(wsOpContinuation, true, body[half:]); err != nil {
		t.Fatalf("fragment 2: %v", err)
	}
	completed := false
	for i := 0; i < 50; i++ {
		data, err := c.readText(15 * time.Second)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if strings.Contains(string(data), `"response.completed"`) {
			completed = true
			break
		}
	}
	if !completed {
		t.Fatal("分片请求没有完成事件")
	}
}

func TestResponsesWebSocketConnectionCap(t *testing.T) {
	g := &Gateway{}
	g.wsActive.Store(wsMaxConnections)
	req := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Key", "x")
	rec := httptest.NewRecorder()
	g.handleResponsesWebSocket(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := g.wsActive.Load(); got != wsMaxConnections {
		t.Fatalf("计数泄漏: %d", got)
	}
}

func TestResponsesWebSocketPerKeyCap(t *testing.T) {
	g := &Gateway{wsKeyActive: map[string]int{"k1": wsMaxConnectionsPerKey}}
	req := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Key", "x")
	req = req.WithContext(withCallerKey(req.Context(), &callerKeyInfo{ID: "k1"}))
	rec := httptest.NewRecorder()
	g.handleResponsesWebSocket(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "5" {
		t.Fatalf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	if got := g.wsActive.Load(); got != 0 {
		t.Fatalf("全局计数泄漏: %d", got)
	}
	if n := g.wsKeyActive["k1"]; n != wsMaxConnectionsPerKey {
		t.Fatalf("按 Key 计数被改动: %d", n)
	}
}

func TestWSKeyAcquireRelease(t *testing.T) {
	g := &Gateway{}
	for i := 0; i < wsMaxConnectionsPerKey; i++ {
		if !g.acquireWSKey("k") {
			t.Fatalf("第 %d 个名额应成功", i+1)
		}
	}
	if g.acquireWSKey("k") {
		t.Fatal("超过上限仍成功")
	}
	g.releaseWSKey("k")
	if !g.acquireWSKey("k") {
		t.Fatal("释放后应可再占")
	}
	if !g.acquireWSKey("") {
		t.Fatal("空 Key 不应受限")
	}
}

// Codex 的典型 WS 第二轮：上一轮拿到 tool call，下一轮只回 function_call_output
// + previous_response_id。验证 WS 桥接能续上状态缝合，而不是把第二轮当新会话。
func TestResponsesWebSocketToolContinuation(t *testing.T) {
	srv := newTestGateway(t)
	c := dialRawWS(t, srv.Listener.Addr().String())

	first := []byte(`{"type":"response.create","response":{"model":"mock-native/echo-pro","input":"read main.go","tools":[{"type":"function","name":"read_file","parameters":{"type":"object"}}]}}`)
	if err := c.sendText(first); err != nil {
		t.Fatalf("send first: %v", err)
	}
	var respID string
	firstDone := false
	for i := 0; i < 60; i++ {
		payload, err := c.readText(15 * time.Second)
		if err != nil {
			t.Fatalf("read first %d: %v", i, err)
		}
		if respID == "" {
			if m := responseIDRe.FindSubmatch(payload); len(m) > 1 {
				respID = string(m[1])
			}
		}
		if strings.Contains(string(payload), `"response.completed"`) {
			firstDone = true
			break
		}
	}
	if !firstDone || respID == "" {
		t.Fatalf("first turn incomplete: done=%v id=%q", firstDone, respID)
	}

	second := []byte(`{"type":"response.create","response":{"model":"mock-native/echo-pro","previous_response_id":"` + respID + `","input":[{"type":"function_call_output","call_id":"call_real_1","output":"{\"path\":\"main.go\"}"}]}}`)
	if err := c.sendText(second); err != nil {
		t.Fatalf("send second: %v", err)
	}
	secondDone := false
	for i := 0; i < 60; i++ {
		payload, err := c.readText(15 * time.Second)
		if err != nil {
			t.Fatalf("read second %d: %v", i, err)
		}
		if strings.Contains(string(payload), `"response.completed"`) {
			secondDone = true
			break
		}
	}
	if !secondDone {
		t.Fatal("function_call_output continuation 没有完成")
	}
}
