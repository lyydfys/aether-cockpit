package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// 端口运行中切换。
//
// 监听器归桌面壳/headless main 所有：网关只暴露一个切换回调，由它们负责
// "先在新端口监听、再关旧监听"。Wails 的界面走同源 AssetHandler，不依赖
// 这个端口；浏览器直接打开的管理页需要改用新地址（响应里会给出 url）。
// 局域网共享在另一个监听器上，为保证状态一致，开启时拒绝切换。

// SetPortSwitcher 注入端口切换回调：返回实际监听地址（host:port）。
func (g *Gateway) SetPortSwitcher(fn func(addr string) (string, error)) {
	if fn == nil {
		g.portSwitch.Store(nil)
		return
	}
	g.portSwitch.Store(&fn)
}

// ListenAddr 返回启动时应监听的地址（settings 里保存的端口优先）。
func (g *Gateway) ListenAddr() string {
	g.addrMu.RLock()
	defer g.addrMu.RUnlock()
	return g.cfg.Addr
}

// normalizeListenAddr 接受 "7860"、"127.0.0.1:7860"、"http://host:7860"。
func normalizeListenAddr(raw, current string) (string, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "http://"), "https://")
	raw = strings.TrimRight(raw, "/")
	if raw == "" {
		return "", fmt.Errorf("端口不能为空")
	}
	host := ""
	portStr := raw
	if strings.Contains(raw, ":") {
		h, p, err := net.SplitHostPort(raw)
		if err != nil {
			return "", fmt.Errorf("不是合法的监听地址: %s", raw)
		}
		host, portStr = h, p
	} else if h, _, err := net.SplitHostPort(current); err == nil {
		host = h
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("端口必须是 1-65535: %s", portStr)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// applyPort 切换监听端口并持久化；返回新的公开地址。
func (g *Gateway) applyPort(raw string) (string, error) {
	addr, err := normalizeListenAddr(raw, g.ListenAddr())
	if err != nil {
		return "", err
	}
	// 幂等：设置页保存整份 prefs 时会带上当前端口，不能因此重新监听。
	if addr == g.ListenAddr() || addr == g.PublicAddr() {
		g.settingsMu.Lock()
		g.settings.ListenAddr = addr
		_ = g.saveSettings()
		g.settingsMu.Unlock()
		return "http://" + g.PublicAddr(), nil
	}
	fn := g.portSwitch.Load()
	if fn == nil || *fn == nil {
		return "", fmt.Errorf("当前运行模式不支持热切换端口；请修改配置后重启")
	}
	// 局域网共享监听在主端口+1：先停旧的，切换成功后在新端口+1 重启。
	lanWasOn := g.LANEnabled()
	if lanWasOn {
		if err := g.SetLAN(false); err != nil {
			return "", err
		}
	}
	actual, err := (*fn)(addr)
	if err != nil {
		if lanWasOn {
			_ = g.SetLAN(true)
		}
		return "", err
	}
	g.SetPublicAddr(actual)
	g.addrMu.Lock()
	g.cfg.Addr = addr
	g.addrMu.Unlock()
	g.settingsMu.Lock()
	g.settings.ListenAddr = addr
	saveErr := g.saveSettings()
	g.settingsMu.Unlock()
	if saveErr != nil {
		return "", saveErr
	}
	if lanWasOn {
		if lanErr := g.SetLAN(true); lanErr != nil {
			log.Printf("[WARN] 端口切换后局域网共享未能重启: %v", lanErr)
			g.settingsMu.Lock()
			g.settings.LAN = false
			_ = g.saveSettings()
			g.settingsMu.Unlock()
		}
	}
	// 端口变了，已接入的 Agent 配置里写的还是旧地址——不重写的话，
	// 用户下次打开 Claude Code / Codex 会连一个已经没人监听的端口，
	// 表现为"昨天还好好的，今天全连不上"。这里把已接入的全部重写一遍。
	g.rewireAgents("http://" + actual)
	return "http://" + actual, nil
}

// rewireAgents 把已接入 Agent 的配置改写到新地址。
//
// 只动"当前确实处于接入状态"的客户端：用户主动断开过的不该被顺手改回来。
// 失败只记日志并汇总一条 notice——端口已经切完了，不能因为某个客户端
// 配置文件写不进去就把整个切换判成失败。
func (g *Gateway) rewireAgents(origin string) {
	mgr := g.clientsManager()
	if mgr == nil {
		return
	}
	key := strings.TrimSpace(g.GatewayKey())
	if key == "" {
		key = "aether-local"
	}
	var failed []string
	for _, a := range g.buildAetherState().Agents {
		if !a.Wired {
			continue
		}
		if _, err := mgr.Apply(a.ID, g.driftParamsFor(a, origin, key)); err != nil {
			failed = append(failed, a.ID)
			log.Printf("[WARN] 端口切换后重写 %s 配置失败: %v", a.ID, err)
		}
	}
	if len(failed) > 0 {
		log.Printf("[WARN] 有 %d 个 Agent 配置未能改写到 %s：%s", len(failed), origin, strings.Join(failed, ", "))
	}
}

// handlePortSetting POST /api/settings/port {port|addr|value}
func (g *Gateway) handlePortSetting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Port  any `json:"port"`
		Addr  any `json:"addr"`
		Value any `json:"value"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败"})
		return
	}
	val := body.Port
	if val == nil {
		val = body.Addr
	}
	if val == nil {
		val = body.Value
	}
	var text string
	switch v := val.(type) {
	case float64:
		text = strconv.Itoa(int(v))
	case string:
		text = v
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 port"})
		return
	}
	url, err := g.applyPort(text)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	view := g.settingsView()
	view["url"] = url
	view["publicURL"] = url
	writeJSON(w, http.StatusOK, view)
}
