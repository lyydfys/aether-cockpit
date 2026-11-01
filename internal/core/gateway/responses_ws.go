package gateway

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Responses WebSocket ingress（对齐 relay-panel 的 GET /v1/responses Upgrade）。
//
// 客户端在同一个连接上按 turn 发 Responses 请求体，服务端把现有 HTTP
// 管线的 SSE 事件逐条转成 WebSocket 文本帧。这样 Codex CLI 一类用
// WebSocket 传输的客户端也能直接走本网关，而不需要另写一套协议栈。
//
// 不依赖第三方库：握手、帧编解码、分片与 ping/pong 都在这里完成。

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// wsMaxConnections 限制同时打开的 WS 连接：空闲连接不设读超时时，
// 没有上限会耗尽文件描述符。relay-panel 也按 Key 限制入口连接数。
const wsMaxConnections = 256

// wsMaxConnectionsPerKey 是单个调用方密钥同时打开的 WS 连接上限：
// 一个密钥的脚本泄漏连接不能占满全进程的配额。
const wsMaxConnectionsPerKey = 8

// handleResponsesWebSocket 处理 GET /v1/responses 的 WebSocket 升级。
func (g *Gateway) handleResponsesWebSocket(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		writeJSON(w, http.StatusUpgradeRequired, map[string]any{
			"error": map[string]any{
				"type": "invalid_request_error", "code": "upgrade_required",
				"message": "WebSocket upgrade required (Upgrade: websocket)",
			},
		})
		return
	}
	if g.wsActive.Add(1) > wsMaxConnections {
		g.wsActive.Add(-1)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]any{
				"type": "service_unavailable", "code": "ws_capacity",
				"message": "WebSocket 连接数已达上限，请稍后重试",
			},
		})
		return
	}
	defer g.wsActive.Add(-1)

	keyID := ""
	if ck := callerKeyFrom(r.Context()); ck != nil {
		keyID = ck.ID
	}
	if !g.acquireWSKey(keyID) {
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": map[string]any{
				"type": "rate_limit_error", "code": "ws_key_capacity",
				"message": "该密钥同时打开的 WebSocket 连接过多，请稍后重试",
			},
		})
		return
	}
	defer g.releaseWSKey(keyID)

	hj, ok := w.(http.Hijacker)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "连接不支持升级"})
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		_ = conn.Close()
		return
	}
	sum := sha1.Sum([]byte(key + wsGUID))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	_, _ = fmt.Fprintf(brw.Writer, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
	if err := brw.Writer.Flush(); err != nil {
		_ = conn.Close()
		return
	}

	ws := &wsConn{conn: conn, br: brw.Reader}
	defer ws.close()

	// 复用手握请求的鉴权与来源信息；RemoteAddr 保持真实对端，
	// 不能把它改成回环地址，否则会绕过 IP 白名单。
	baseHeader := r.Header.Clone()
	remoteAddr := r.RemoteAddr

	for {
		op, payload, err := ws.readMessage()
		if err != nil {
			return
		}
		switch op {
		case wsOpClose:
			_ = ws.writeFrame(wsOpClose, payload)
			return
		case wsOpPing:
			_ = ws.writeFrame(wsOpPong, payload)
			continue
		case wsOpPong:
			continue
		case wsOpText:
			body, ok := responsesWSBody(payload)
			if !ok {
				_ = ws.writeJSON(map[string]any{
					"type":  "error",
					"error": map[string]any{"type": "invalid_request_error", "message": "请求体不是合法 JSON"},
				})
				continue
			}
			g.runResponsesWSTurn(ws, baseHeader, remoteAddr, body)
		}
	}
}

// responsesWSBody 把 WS 消息折成 POST /v1/responses 的请求体。
// 兼容两种形态：直接给 Responses 请求体，或 Realtime 风格的
// {"type":"response.create","response":{...}}。
func responsesWSBody(raw []byte) ([]byte, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false
	}
	if t := strings.TrimSpace(string(m["type"])); strings.Trim(t, `"`) == "response.create" {
		if inner, ok := m["response"]; ok && len(inner) > 0 && inner[0] == '{' {
			var body map[string]json.RawMessage
			if err := json.Unmarshal(inner, &body); err != nil {
				return nil, false
			}
			if _, ok := body["stream"]; !ok {
				body["stream"] = json.RawMessage(`true`)
			}
			out, err := json.Marshal(body)
			return out, err == nil
		}
		delete(m, "type")
	}
	if _, ok := m["stream"]; !ok {
		m["stream"] = json.RawMessage(`true`)
	}
	out, err := json.Marshal(m)
	return out, err == nil
}

// runResponsesWSTurn 跑一轮 HTTP 管线，把 SSE data 逐条写成 WS 文本帧。
func (g *Gateway) runResponsesWSTurn(ws *wsConn, header http.Header, remoteAddr string, body []byte) {
	req, err := http.NewRequest(http.MethodPost, "http://aether.local/v1/responses", bytes.NewReader(body))
	if err != nil {
		_ = ws.writeJSON(map[string]any{"type": "error", "error": map[string]any{"message": err.Error()}})
		return
	}
	req.Header = header.Clone()
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr

	sw := &wsSSEWriter{ws: ws}
	g.ServeHTTP(sw, req)
	sw.flush()

	if sw.events == 0 {
		// 没有 SSE 事件：把错误体原样发回去，客户端至少能看到原因。
		text := strings.TrimSpace(sw.raw.String())
		if text == "" {
			text = `{"type":"error","error":{"message":"上游没有返回事件"}}`
		}
		_ = ws.writeFrame(wsOpText, []byte(text))
	}
}

// wsSSEWriter 把 SSE 块里的 data 行转成 WS 文本帧。
type wsSSEWriter struct {
	ws     *wsConn
	mu     sync.Mutex
	buf    []byte
	raw    bytes.Buffer
	status int
	events int
}

func (w *wsSSEWriter) Header() http.Header { return http.Header{} }

func (w *wsSSEWriter) WriteHeader(status int) { w.status = status }

// Flush 满足 http.Flusher：转码器按块写 SSE，Write 已即时发出 WS 帧，
// 这里无需额外动作（保留不完整块，等下一次 Write 补齐）。
func (w *wsSSEWriter) Flush() {}

func (w *wsSSEWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.raw.Write(p)
	w.buf = append(w.buf, p...)
	for {
		i := bytes.Index(w.buf, []byte("\n\n"))
		if i < 0 {
			break
		}
		block := w.buf[:i]
		w.buf = w.buf[i+2:]
		w.sendSSEBlock(block)
	}
	return len(p), nil
}

func (w *wsSSEWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.sendSSEBlock(w.buf)
		w.buf = nil
	}
}

func (w *wsSSEWriter) sendSSEBlock(block []byte) {
	for _, line := range strings.Split(string(block), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		if err := w.ws.writeFrame(wsOpText, []byte(payload)); err == nil {
			w.events++
		}
	}
}

// --- 帧编解码 ---

const (
	wsOpContinuation byte = 0x0
	wsOpText         byte = 0x1
	wsOpBinary       byte = 0x2
	wsOpClose        byte = 0x8
	wsOpPing         byte = 0x9
	wsOpPong         byte = 0xA
)

// acquireWSKey 占用一个按 Key 的连接名额；空 ID（主 API Key / 本机）不限。
func (g *Gateway) acquireWSKey(id string) bool {
	if id == "" {
		return true
	}
	g.wsKeyMu.Lock()
	defer g.wsKeyMu.Unlock()
	if g.wsKeyActive == nil {
		g.wsKeyActive = make(map[string]int, 8)
	}
	if g.wsKeyActive[id] >= wsMaxConnectionsPerKey {
		return false
	}
	g.wsKeyActive[id]++
	return true
}

func (g *Gateway) releaseWSKey(id string) {
	if id == "" {
		return
	}
	g.wsKeyMu.Lock()
	defer g.wsKeyMu.Unlock()
	if n := g.wsKeyActive[id]; n > 0 {
		if n == 1 {
			delete(g.wsKeyActive, id)
		} else {
			g.wsKeyActive[id] = n - 1
		}
	}
}

type wsConn struct {
	conn   net.Conn
	br     *bufio.Reader
	writeM sync.Mutex
}

func (c *wsConn) close() { _ = c.conn.Close() }

func (c *wsConn) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.writeFrame(wsOpText, b)
}

func (c *wsConn) writeFrame(op byte, payload []byte) error {
	c.writeM.Lock()
	defer c.writeM.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	var hdr []byte
	hdr = append(hdr, 0x80|op)
	n := len(payload)
	switch {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n <= 65535:
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 127)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		hdr = append(hdr, b[:]...)
	}
	if _, err := c.conn.Write(hdr); err != nil {
		return err
	}
	_, err := c.conn.Write(payload)
	return err
}

// readMessage 读一条完整消息（处理分片）。
func (c *wsConn) readMessage() (byte, []byte, error) {
	var opcode byte
	var payload []byte
	for {
		h := make([]byte, 2)
		if _, err := io.ReadFull(c.br, h); err != nil {
			return 0, nil, err
		}
		fin := h[0]&0x80 != 0
		op := h[0] & 0x0f
		masked := h[1]&0x80 != 0
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
		// 控制帧与一条消息的上限：16MB，防止恶意长度把内存打满。
		if n < 0 || n > 16<<20 {
			return 0, nil, fmt.Errorf("websocket 帧过大: %d", n)
		}
		var mask []byte
		if masked {
			mask = make([]byte, 4)
			if _, err := io.ReadFull(c.br, mask); err != nil {
				return 0, nil, err
			}
		}
		data := make([]byte, n)
		if _, err := io.ReadFull(c.br, data); err != nil {
			return 0, nil, err
		}
		if masked {
			for i := range data {
				data[i] ^= mask[i%4]
			}
		}
		if op == wsOpContinuation {
			payload = append(payload, data...)
			if fin {
				return opcode, payload, nil
			}
			continue
		}
		if opcode == 0 {
			opcode = op
		}
		payload = append(payload, data...)
		if fin {
			return opcode, payload, nil
		}
	}
}
