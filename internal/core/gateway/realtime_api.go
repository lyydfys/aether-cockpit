package gateway

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"aether-cockpit/internal/core/tools"
)

// 实时语音：WebSocket 双向桥 + 会话密钥代理。
//
// 下游连 wss://网关/v1/realtime?model=...，网关以配置的实时服务
// （OpenAI Realtime / 兼容 Live 服务）建立上游连接，然后原样转发帧：
// 文本帧（JSON 事件）与二进制帧（音频）都保持类型与顺序。

var realtimeUpgrader = websocket.Upgrader{
	ReadBufferSize:  32 << 10,
	WriteBufferSize: 32 << 10,
	// 网关鉴权在 HTTP 层完成（g.auth），WS 握手不额外做 Origin 限制：
	// 本地面板与 SDK 的来源各不相同，限制 Origin 只会挡住合法客户端。
	CheckOrigin: func(*http.Request) bool { return true },
}

// realtimeConfig 返回当前实时语音配置（无配置时 ok=false）。
func (g *Gateway) realtimeConfig() (tools.RealtimeConfig, bool) {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.Tools == nil || g.settings.Tools.Realtime == nil {
		return tools.RealtimeConfig{}, false
	}
	cfg := *g.settings.Tools.Realtime
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return tools.RealtimeConfig{}, false
	}
	return cfg, true
}

// handleRealtimeSession POST /v1/realtime/sessions
func (g *Gateway) handleRealtimeSession(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "读取请求体失败"})
		return
	}
	cfg, settingsOK := g.realtimeConfig()
	target, terr := g.mediaTargetFor(r, body)
	if terr != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": mediaTargetError(terr)})
		return
	}
	if !settingsOK && target.BaseURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "尚未配置实时语音：请在设置页「工具服务 · 实时语音」填写 base_url，或把实时模型挂到某个渠道上"})
		return
	}
	// 会话请求体里通常带 model；渠道路由时必须换成上游名，否则上游不认虚拟名。
	if target.Model != "" {
		body = rewriteJSONModel(body, target.Model)
	}
	cfg = target.applyToRealtime(cfg)
	code, out, err := tools.ProxyRealtimeSessions(r.Context(), cfg, body)
	g.noteMediaResult(target, code, err)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(out)
}

// handleRealtimeWS GET /v1/realtime（WebSocket 升级）
func (g *Gateway) handleRealtimeWS(w http.ResponseWriter, r *http.Request) {
	cfg, settingsOK := g.realtimeConfig()
	query := r.URL.Query()
	hint := strings.TrimSpace(query.Get("model"))
	target, terr := g.resolveMediaTarget(hint)
	if terr != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": mediaTargetError(terr)})
		return
	}
	if !settingsOK && target.BaseURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "尚未配置实时语音：请在设置页「工具服务 · 实时语音」填写 base_url，或把实时模型挂到某个渠道上"})
		return
	}
	cfg = target.applyToRealtime(cfg)
	switch {
	case target.Model != "":
		// 渠道路由：上游只认自己的模型名，客户端传的虚拟名要换掉。
		query.Set("model", target.Model)
	case hint == "" && cfg.Model != "":
		query.Set("model", cfg.Model)
	}
	upstreamURL, err := realtimeWSURL(cfg.BaseURL, query)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	headers := http.Header{}
	if cfg.APIKey != "" {
		headers.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	if beta := strings.TrimSpace(r.Header.Get("OpenAI-Beta")); beta != "" {
		headers.Set("OpenAI-Beta", beta)
	} else {
		headers.Set("OpenAI-Beta", "realtime=v1")
	}

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	// WS 走独立拨号器，拿不到账号的 http.Client；只能把账号的显式代理塞进来。
	// "direct" 或不设时保持原有行为（不额外指定代理）。
	if target.Env != nil {
		if p := strings.TrimSpace(target.Env.Proxy()); p != "" && p != "direct" {
			if proxyURL, perr := url.Parse(p); perr == nil {
				dialer.Proxy = http.ProxyURL(proxyURL)
			}
		}
	}
	upstream, resp, err := dialer.DialContext(r.Context(), upstreamURL, headers)
	if err != nil {
		g.noteMediaResult(target, 0, err)
		msg := "连接实时语音上游失败"
		if resp != nil {
			msg += "（HTTP " + resp.Status + "）"
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": msg})
		return
	}
	defer upstream.Close()
	// 握手成功即视为该账号这一次可用：清掉连续失败计数，别让一次历史抖动
	// 把已经恢复的账号一直按在冷却里。
	g.noteMediaResult(target, http.StatusOK, nil)

	downstream, err := realtimeUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer downstream.Close()

	bridgeRealtime(downstream, upstream)
}

// realtimeWSURL 把配置的 HTTP base 转成 WebSocket 地址并保留查询参数。
func realtimeWSURL(base string, query url.Values) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(base), "/"))
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https", "wss":
		u.Scheme = "wss"
	case "http", "ws":
		u.Scheme = "ws"
	default:
		return "", errBadRealtimeScheme
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/realtime"
	u.RawQuery = query.Encode()
	return u.String(), nil
}

// bridgeRealtime 双向转发帧；任一侧断开后给另一侧发关闭帧并退出。
func bridgeRealtime(a, b *websocket.Conn) {
	errs := make(chan error, 2)
	pipe := func(dst, src *websocket.Conn) {
		for {
			mt, data, err := src.ReadMessage()
			if err != nil {
				errs <- err
				return
			}
			if err := dst.WriteMessage(mt, data); err != nil {
				errs <- err
				return
			}
		}
	}
	go pipe(a, b)
	go pipe(b, a)
	<-errs
	deadline := time.Now().Add(time.Second)
	_ = a.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), deadline)
	_ = b.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), deadline)
}

var errBadRealtimeScheme = errors.New("实时语音 base_url 必须是 http(s):// 或 ws(s)://")
