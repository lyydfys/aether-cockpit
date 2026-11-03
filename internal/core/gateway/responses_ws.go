package gateway

import (
	"bufio"
	"bytes"
	"context"
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

	// socket 级会话：Codex 在一条连接上连续跑多轮，只靠 previous_response_id
	// 指代上一轮。把它缓存在连接上（而不是只依赖 store）有两个好处：
	//   - store:false（Codex 每帧都带）不会让网关"记不住"上下文；
	//   - 网关重启/过期清理 store 后，同一条连接内的多轮仍然能续上。
	sock := &wsSocket{turns: make([]wsTurn, 0, wsSocketKeep)}

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
			// 控制帧优先：取消当前轮而不是当成新请求。
			if handled, responseID := wsControlFrame(ws, payload); handled {
				if cancelled := sock.cancelCurrent(); cancelled {
					_ = ws.writeJSON(map[string]any{
						"type": "response.cancelled",
						"error": map[string]any{
							"code":    "cancelled",
							"message": "本轮已按客户端请求取消",
						},
						"response_id": responseID,
					})
				} else {
					// 没有正在跑的轮次：明确回一条，避免客户端一直等。
					_ = ws.writeJSON(map[string]any{
						"type": "error",
						"error": map[string]any{
							"type":    "invalid_request_error",
							"code":    "no_active_response",
							"message": "没有正在进行的响应可取消",
						},
					})
				}
				continue
			}
			body, ok := responsesWSBody(payload)
			if !ok {
				_ = ws.writeJSON(map[string]any{
					"type":  "error",
					"error": map[string]any{"type": "invalid_request_error", "message": "请求体不是合法 JSON"},
				})
				continue
			}
			g.runResponsesWSTurn(ws, sock, baseHeader, remoteAddr, body)
		}
	}
}

// wsSocketKeep 是一条连接上保留的轮次数。
//
// 2 条即可：客户端只会引用最近一轮（Codex 的 previous_response_id 永远指上一轮），
// 再往前就应该让客户端重发完整会话——保留太多等于变相实现了无界缓存。
const wsSocketKeep = 2

// wsTurn 是连接内缓存的一轮：id + 将来自回拼的完整上下文（input 与 output）。
type wsTurn struct {
	ID    string            `json:"id"`
	Items []json.RawMessage `json:"items"`
}

// wsSocket 是一条 WebSocket 连接自己的会话状态。并发安全。
type wsSocket struct {
	mu      sync.Mutex
	turns   []wsTurn
	cancel  context.CancelFunc
	running bool
}

// lookup 查上一轮；命中返回其上下文副本。
func (s *wsSocket) lookup(id string) ([]json.RawMessage, bool) {
	if id == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.turns {
		if t.ID == id {
			return append([]json.RawMessage(nil), t.Items...), true
		}
	}
	return nil, false
}

// remember 记一轮（超出上限丢最旧的）。
func (s *wsSocket) remember(id string, items []json.RawMessage) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turns = append(s.turns, wsTurn{ID: id, Items: items})
	if len(s.turns) > wsSocketKeep {
		s.turns = append([]wsTurn(nil), s.turns[len(s.turns)-wsSocketKeep:]...)
	}
}

// begin 记录当轮的可取消 ctx。
func (s *wsSocket) begin(cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel = cancel
	s.running = true
}

// end 在当轮结束时清掉（避免误取消下一轮）。
func (s *wsSocket) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel = nil
	s.running = false
}

// cancelCurrent 取消正在跑的一轮；返回是否真的取消了。
func (s *wsSocket) cancelCurrent() bool {
	s.mu.Lock()
	cancel, running := s.cancel, s.running
	s.mu.Unlock()
	if !running || cancel == nil {
		return false
	}
	cancel()
	return true
}

// wsControlFrame 识别控制帧（response.interrupt / response.cancel）。
//
// 返回值 handled 表示"这条帧不是请求，已经处理完毕"；responseID 用于回执。
func wsControlFrame(ws *wsConn, payload []byte) (handled bool, responseID string) {
	var probe struct {
		Type       string `json:"type"`
		ResponseID string `json:"response_id"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return false, ""
	}
	switch strings.ToLower(strings.TrimSpace(probe.Type)) {
	case "response.interrupt", "response.cancel", "response.stop":
		return true, probe.ResponseID
	}
	return false, ""
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
//
// 两件事在这里做：
//  1. socket 会话续接：若本帧引用的上一轮在本连接缓存里，就把它的
//     input+output 拼成完整会话重发，并**去掉** previous_response_id——
//     这样既不吃 store，也不会因为 store 过期而丢历史；
//  2. 收集本轮产物（response id + 输出 items），作为下一轮的可拼回上下文。
func (g *Gateway) runResponsesWSTurn(ws *wsConn, sock *wsSocket, header http.Header, remoteAddr string, body []byte) {
	body = sock.spliceHistory(body)

	// 当轮用可取消的 ctx：客户端发 response.interrupt 时取消整轮，
	// 上游连接随之关闭，不再继续烧 token。
	ctx, cancel := context.WithCancel(context.Background())
	sock.begin(cancel)
	defer sock.end()
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://aether.local/v1/responses", bytes.NewReader(body))
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

	// 记下本轮：客户端下一帧只会给 id，上下文要靠这里拼回去。
	// 存 input+output 两段，和 aether 的 socket 会话语义一致。
	if id, out := sw.turn(); id != "" {
		in := socketInputItems(body)
		items := make([]json.RawMessage, 0, len(in)+len(out))
		items = append(items, in...)
		items = append(items, out...)
		sock.remember(id, items)
	}

	if sw.events == 0 {
		// 没有 SSE 事件：把错误体原样发回去，客户端至少能看到原因。
		text := strings.TrimSpace(sw.raw.String())
		if text == "" {
			text = `{"type":"error","error":{"message":"上游没有返回事件"}}`
		}
		_ = ws.writeFrame(wsOpText, []byte(text))
	}
}

// spliceHistory 把本帧的 previous_response_id 展开成连接内缓存的完整上下文。
//
// 命中时返回一个新 body：input = 上一轮 input+output + 本轮 input，
// 并删掉 previous_response_id（上游与 IR 路径都不该再去找 store）。
// 未命中时原样返回，交给 store 路径处理（store 也没有就会报
// previous_response_not_found，这是刻意的：让客户端自己重发全量）。
func (s *wsSocket) spliceHistory(body []byte) []byte {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	prev := strings.Trim(strings.TrimSpace(string(m["previous_response_id"])), `"`)
	if prev == "" {
		return body
	}
	history, ok := s.lookup(prev)
	if !ok {
		return body
	}

	cur, err := responsesInputItems(m["input"])
	if err != nil {
		return body
	}
	merged := make([]json.RawMessage, 0, len(history)+len(cur))
	merged = append(merged, history...)
	merged = append(merged, cur...)
	items, err := json.Marshal(merged)
	if err != nil {
		return body
	}
	m["input"] = items
	delete(m, "previous_response_id")
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}

// socketInputItems 取出请求体里的 input 条目（供 socket 会话缓存）。
func socketInputItems(body []byte) []json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil
	}
	items, err := responsesInputItems(m["input"])
	if err != nil {
		return nil
	}
	return items
}

// responsesInputItems 把 Responses 的 input 归一成"条目数组"。
//
// input 允许两种形态：字符串（等价于一条 user 消息）与条目数组。
// 字符串会被包成 {"role":"user","content":"..."}，与 Responses 的
// EasyInputMessage 形状一致。
func responsesInputItems(raw json.RawMessage) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var arr []json.RawMessage
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, err
		}
		return arr, nil
	}
	if trimmed[0] == '"' {
		msg, err := json.Marshal(map[string]any{
			"role":    "user",
			"content": json.RawMessage(trimmed),
		})
		if err != nil {
			return nil, err
		}
		return []json.RawMessage{msg}, nil
	}
	return nil, nil
}

// wsSSEWriter 把 SSE 块里的 data 行转成 WS 文本帧。
type wsSSEWriter struct {
	ws     *wsConn
	mu     sync.Mutex
	buf    []byte
	raw    bytes.Buffer
	status int
	events int
	// turnID / turnOut 是本轮的 response id 与输出 items（socket 会话缓存用）。
	turnID  string
	turnOut []json.RawMessage
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
		w.captureTurn(payload)
		if err := w.ws.writeFrame(wsOpText, []byte(payload)); err == nil {
			w.events++
		}
	}
}

// captureTurn 从 response.completed 事件里取出本轮 id 与 output items。
//
// 只在 completed 上取：中途的 delta 事件是增量，拼起来容易重复；
// completed 里的 output 是最终结果，正是下一轮要拼回去的那一段。
func (w *wsSSEWriter) captureTurn(payload string) {
	if !strings.Contains(payload, "response.completed") {
		return
	}
	var ev struct {
		Response struct {
			ID     string            `json:"id"`
			Output []json.RawMessage `json:"output"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		return
	}
	if ev.Response.ID == "" {
		return
	}
	w.turnID = ev.Response.ID
	w.turnOut = ev.Response.Output
}

// turn 返回本轮 id 与输出 items（供 socket 会话缓存）。
func (w *wsSSEWriter) turn() (string, []json.RawMessage) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.turnID, w.turnOut
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
