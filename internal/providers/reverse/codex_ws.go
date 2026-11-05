package reverse

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

// Codex 订阅的上游 WebSocket（对齐 aether 的 codex_socket）：
// 一轮一个连接，发送 HTTP 请求体 + "type":"response.create"，逐帧收事件，
// 再以 SSE 写给下游。握手/首帧失败时返回 started=false，调用方退回 HTTP。
const codexWSBeta = "responses_websockets=2026-02-06"

func (p *Provider) ResponsesWebSocketStream(ctx context.Context, env *provider.AccountEnv, rawReq []byte, upstreamModel string, w http.ResponseWriter) (bool, error) {
	token := strings.TrimSpace(env.GetCredential("access_token"))
	officialCodex := p.profile.ID == "codex" &&
		strings.TrimSpace(env.GetCredential("account_id")) != "" && token != ""

	sp := spec.Load(p.profile.ID)
	endpoint := strings.TrimSpace(p.profile.ResponsesWSEndpoint)
	if sp != nil {
		if v := strings.TrimSpace(sp.Endpoint("responses_ws", "")); v != "" {
			endpoint = v
		}
	}
	if endpoint == "" && !officialCodex {
		return false, nil
	}

	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" && sp != nil {
		base = strings.TrimRight(strings.TrimSpace(sp.BaseURL), "/")
	}
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(p.profile.DefaultBaseURL), "/")
	}
	if endpoint == "" {
		// Codex 官方订阅：chatgpt.com 的 Responses WS 端点。
		if base == "" {
			base = "https://chatgpt.com/backend-api/codex"
		}
		endpoint = "/responses"
	}
	wsURL := resolveWSEndpoint(base, endpoint)
	if wsURL == "" {
		return false, nil
	}

	// 请求体 = 原 Responses 请求 + type=response.create（Codex 帧格式）；
	// model 按 HTTP 直通同一口径改写（auto 取凭证，spec 映射最后生效）。
	if upstreamModel == autoModelUpstream {
		upstreamModel = strings.TrimSpace(env.GetCredential("model"))
	}
	if sp := spec.Load(p.profile.ID); sp != nil && upstreamModel != "" {
		upstreamModel = sp.MapModel(upstreamModel)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rawReq, &body); err != nil {
		return false, err
	}
	if upstreamModel != "" {
		if m, err := json.Marshal(upstreamModel); err == nil {
			body["model"] = m
		}
	}
	body["type"] = json.RawMessage(`"response.create"`)
	frameBody, err := json.Marshal(body)
	if err != nil {
		return false, err
	}

	headers := map[string]string{}
	if officialCodex {
		headers = map[string]string{
			"Authorization":       "Bearer " + token,
			"chatgpt-account-id":  strings.TrimSpace(env.GetCredential("account_id")),
			"originator":          "codex_cli_rs",
			"OpenAI-Beta":         codexWSBeta,
			"User-Agent":          "codex_cli_rs/0.16.0 (Aether Cockpit)",
			"x-client-request-id": randomWSUUID(),
		}
	} else {
		// 通用渠道：鉴权与固定头复用 HTTP 管线的同一套规则
		// （spec.chat.auth 决定头名/前缀，HeadersTemplate 可覆盖）。
		dummy, _ := http.NewRequest(http.MethodPost, wsURL, nil)
		if aerr := applyAuth(dummy, env, authFor(sp, &p.profile)); aerr != nil {
			return false, aerr
		}
		if len(p.profile.Headers) > 0 {
			applyHeaders(dummy, env, p.profile.Headers)
		}
		if sp != nil {
			applyHeaders(dummy, env, sp.HeadersTemplate)
		}
		for k := range dummy.Header {
			if v := dummy.Header.Get(k); v != "" {
				headers[k] = v
			}
		}
	}
	conn, err := dialWS(ctx, wsURL, headers, wsProxyFor(env, wsURL))
	if err != nil {
		return false, err
	}
	defer conn.close()

	// 客户端取消时关掉 socket，读循环随之退出。
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			conn.close()
		case <-stop:
		}
	}()

	if err := conn.writeText(frameBody); err != nil {
		return false, err
	}

	started := false
	for {
		payload, err := conn.readText()
		if err != nil {
			if !started {
				return false, err
			}
			return true, err
		}
		var ev struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(payload, &ev)
		name := ev.Type
		if name == "" {
			name = "message"
		}
		if !started {
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			started = true
		}
		_, _ = io.WriteString(w, "event: "+name+"\ndata: ")
		_, _ = w.Write(payload)
		_, _ = io.WriteString(w, "\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		switch name {
		case "response.completed", "response.failed", "error":
			return true, nil
		}
	}
}

// resolveWSEndpoint 把绝对 ws(s) 地址或相对路径解析成可拨号的 WebSocket URL。
func resolveWSEndpoint(base, endpoint string) string {
	e := strings.TrimSpace(endpoint)
	if e == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(e, "ws://"), strings.HasPrefix(e, "wss://"):
		return e
	case strings.HasPrefix(e, "https://"), strings.HasPrefix(e, "http://"):
		if u, err := url.Parse(e); err == nil {
			switch u.Scheme {
			case "https":
				u.Scheme = "wss"
			case "http":
				u.Scheme = "ws"
			}
			return u.String()
		}
		return ""
	}
	if base == "" {
		return ""
	}
	full := strings.TrimRight(base, "/") + "/" + strings.TrimLeft(e, "/")
	u, err := url.Parse(full)
	if err != nil {
		return ""
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	return u.String()
}

// --- 最小 WebSocket 客户端（仅文本帧；握手校验 + 掩码 + ping） ---

type wsClient struct {
	conn  net.Conn
	br    *bufio.Reader
	write sync.Mutex
}

func (c *wsClient) close() { _ = c.conn.Close() }

// wsProxyFor 从账号 HTTP 客户端的代理配置里取上游 WS 的代理：
// 全局代理 / 渠道代理 / 账号代理最终都体现在 Transport.Proxy 上。
func wsProxyFor(env *provider.AccountEnv, wsURL string) *url.URL {
	client := env.Client()
	if client == nil {
		return nil
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok || tr.Proxy == nil {
		return nil
	}
	u, err := url.Parse(wsURL)
	if err != nil {
		return nil
	}
	probe := &http.Request{URL: &url.URL{Scheme: "https", Host: u.Host}}
	p, err := tr.Proxy(probe)
	if err != nil {
		return nil
	}
	return p
}

func dialWS(ctx context.Context, wsURL string, headers map[string]string, proxy *url.URL) (*wsClient, error) {
	u, err := url.Parse(wsURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "wss" && u.Scheme != "ws" {
		return nil, fmt.Errorf("不支持的 WebSocket 地址: %s", wsURL)
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		if u.Scheme == "wss" {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	d := net.Dialer{Timeout: 15 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, err
	}
	if proxy != nil {
		// HTTP CONNECT 隧道：WS 与 HTTPS 一样先打通再握手。
		pconn, perr := dialWSViaProxy(ctx, proxy, host)
		_ = conn.Close()
		if perr != nil {
			return nil, perr
		}
		conn = pconn
	}
	key := base64.StdEncoding.EncodeToString(randomBytes(16))
	var req strings.Builder
	fmt.Fprintf(&req, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n", u.RequestURI(), u.Host, key)
	for k, v := range headers {
		if k == "" || v == "" || strings.ContainsAny(k, "\r\n") || strings.ContainsAny(v, "\r\n") {
			continue
		}
		fmt.Fprintf(&req, "%s: %s\r\n", k, v)
	}
	req.WriteString("\r\n")
	if _, err := io.WriteString(conn, req.String()); err != nil {
		_ = conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = resp.Body.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("上游拒绝 WebSocket 升级: %s", resp.Status)
	}
	want := wsAccept(key)
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != "" && got != want {
		_ = conn.Close()
		return nil, fmt.Errorf("WebSocket 握手校验失败")
	}
	_ = conn.SetReadDeadline(time.Time{})
	return &wsClient{conn: conn, br: br}, nil
}

func (c *wsClient) writeText(payload []byte) error {
	c.write.Lock()
	defer c.write.Unlock()
	mask := randomBytes(4)
	hdr := []byte{0x81}
	n := len(payload)
	switch {
	case n < 126:
		hdr = append(hdr, 0x80|byte(n))
	case n <= 65535:
		hdr = append(hdr, 0x80|126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 0x80|127)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		hdr = append(hdr, b[:]...)
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

func (c *wsClient) readText() ([]byte, error) {
	for {
		h := make([]byte, 2)
		if _, err := io.ReadFull(c.br, h); err != nil {
			return nil, err
		}
		op := h[0] & 0x0f
		n := int64(h[1] & 0x7f)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(c.br, b[:]); err != nil {
				return nil, err
			}
			n = int64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(c.br, b[:]); err != nil {
				return nil, err
			}
			n = int64(binary.BigEndian.Uint64(b[:]))
		}
		if n < 0 || n > 16<<20 {
			return nil, fmt.Errorf("WebSocket 帧过大: %d", n)
		}
		data := make([]byte, n)
		if _, err := io.ReadFull(c.br, data); err != nil {
			return nil, err
		}
		switch op {
		case 0x1: // text
			return data, nil
		case 0x8: // close
			return nil, io.EOF
		case 0x9: // ping
			_ = c.writeControl(0xA, data)
		case 0xA: // pong
			continue
		default:
			continue
		}
	}
}

func (c *wsClient) writeControl(op byte, payload []byte) error {
	c.write.Lock()
	defer c.write.Unlock()
	mask := randomBytes(4)
	hdr := []byte{0x80 | op, 0x80 | byte(len(payload))}
	hdr = append(hdr, mask...)
	data := make([]byte, len(payload))
	for i := range payload {
		data[i] = payload[i] ^ mask[i%4]
	}
	if _, err := c.conn.Write(hdr); err != nil {
		return err
	}
	_, err := c.conn.Write(data)
	return err
}

// wsProxyTLSConfig 是连接 HTTPS 代理时用的 TLS 配置（测试可替换）。
var wsProxyTLSConfig = func() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12}
}

// dialWSViaProxy 通过 HTTP/HTTPS 代理建立 CONNECT 隧道。
func dialWSViaProxy(ctx context.Context, proxy *url.URL, target string) (net.Conn, error) {
	addr := proxy.Host
	if !strings.Contains(addr, ":") {
		if proxy.Scheme == "https" {
			addr += ":443"
		} else {
			addr += ":80"
		}
	}
	var conn net.Conn
	var err error
	if proxy.Scheme == "https" {
		host := proxy.Hostname()
		cfg := wsProxyTLSConfig()
		cfg.ServerName = host
		d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 15 * time.Second}, Config: cfg}
		conn, err = d.DialContext(ctx, "tcp", addr)
	} else {
		d := net.Dialer{Timeout: 15 * time.Second}
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, err
	}
	var req strings.Builder
	fmt.Fprintf(&req, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n", target, target)
	if proxy.User != nil {
		user := proxy.User.Username()
		pass, _ := proxy.User.Password()
		cred := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		fmt.Fprintf(&req, "Proxy-Authorization: Basic %s\r\n", cred)
	}
	req.WriteString("\r\n")
	if _, err := io.WriteString(conn, req.String()); err != nil {
		_ = conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("代理拒绝 CONNECT: %s", resp.Status)
	}
	_ = resp.Body.Close()
	return conn, nil
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func wsAccept(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func randomWSUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
