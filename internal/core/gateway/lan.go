package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 局域网共享：在回环监听之外，再开一个只服务本机网络的监听。
//
// 默认关闭，必须显式打开；打开前要求已有主 API Key 或启用的调用方密钥，
// 否则远程请求会全部 401（打开一个"看似能用、其实全拒绝"的端口没有意义）。
// 端口默认取主端口 + 1，避免与回环监听抢同一个端口。
// TLS 不内置：对外请走 Cloudflare Tunnel 等反代（转发头会让请求按远程校验密钥）。

type lanState struct {
	mu   sync.Mutex
	srv  *http.Server
	addr string // 实际监听地址（含端口）
	urls []string
	on   bool
	// cfgAddr 是显式监听地址（测试/自定义端口用），空则自动。
	cfgAddr string
}

// LANEnabled 报告局域网共享是否在运行。
func (g *Gateway) LANEnabled() bool {
	g.lan.mu.Lock()
	defer g.lan.mu.Unlock()
	return g.lan.on
}

// LANURLs 返回当前可用的局域网地址（如 http://192.168.1.5:7861）。
func (g *Gateway) LANURLs() []string {
	g.lan.mu.Lock()
	defer g.lan.mu.Unlock()
	return append([]string(nil), g.lan.urls...)
}

// lanPort 从实际监听地址里取端口。
func lanPort(addr string) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return n
}

// lanListenAddr 计算要监听的地址：显式配置优先，否则 0.0.0.0:<主端口+1>。
func (g *Gateway) lanListenAddr() string {
	if g.lan.cfgAddr != "" {
		return g.lan.cfgAddr
	}
	port := lanPort(g.PublicAddr())
	if port == 0 {
		port = 7860
	}
	return fmt.Sprintf("0.0.0.0:%d", port+1)
}

// LANURLsFor 枚举本机非回环 IPv4 地址，拼成访问地址（`http://ip:port`）。
//
// 供移动运行时在**主监听绑 0.0.0.0** 时展示"别的设备该填什么地址"；
// 与桌面侧 `lanURLsFor` 同一套枚举口径，避免两处给出不同答案。
func LANURLsFor(listenAddr string) []string { return lanURLsFor(listenAddr) }

// lanURLsFor 枚举本机非回环 IPv4 地址，拼成访问地址。
func lanURLsFor(listenAddr string) []string {
	port := lanPort(listenAddr)
	if port == 0 {
		return nil
	}
	host, _, _ := net.SplitHostPort(listenAddr)
	// 测试或用户显式指定回环时，只给回环地址。
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return []string{"http://" + net.JoinHostPort(host, strconv.Itoa(port))}
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := make([]string, 0, 4)
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.To4() == nil {
				continue
			}
			if ip.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, "http://"+net.JoinHostPort(ip.String(), strconv.Itoa(port)))
		}
	}
	return out
}

// SetLAN 打开/关闭局域网共享。
func (g *Gateway) SetLAN(on bool) error {
	g.lan.mu.Lock()
	defer g.lan.mu.Unlock()
	if g.lan.on == on {
		return nil
	}
	if !on {
		if g.lan.srv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = g.lan.srv.Shutdown(ctx)
			cancel()
		}
		g.lan.srv, g.lan.addr, g.lan.urls, g.lan.on = nil, "", nil, false
		return nil
	}
	addr := g.lanListenAddr()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("无法监听 %s：%v", addr, err)
	}
	srv := &http.Server{
		Handler:           g.mux,
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	g.lan.srv = srv
	g.lan.addr = ln.Addr().String()
	g.lan.urls = lanURLsFor(g.lan.addr)
	g.lan.on = true
	return nil
}

// RestoreLAN 在启动时按设置恢复局域网共享。
func (g *Gateway) RestoreLAN() {
	g.settingsMu.Lock()
	on := g.settings.LAN
	addr := g.settings.LANAddr
	g.settingsMu.Unlock()
	if !on {
		return
	}
	g.lan.cfgAddr = strings.TrimSpace(addr)
	// 没有任何可用的远程凭据时不自动打开，避免开出一个全 401 的端口。
	if g.cfg.APIKey == "" && (g.callerKeys == nil || !g.callerKeys.hasEnabled()) {
		return
	}
	if err := g.SetLAN(true); err != nil {
		// 端口被占用等情况：保持关闭，设置页会显示错误。
		g.settingsMu.Lock()
		g.settings.LAN = false
		_ = g.saveSettings()
		g.settingsMu.Unlock()
	}
}

// handleLANSettings POST /api/settings/lan
func (g *Gateway) handleLANSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On *bool `json:"on"`
	}
	if err := decodeBody(r, &body); err != nil || body.On == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 on"})
		return
	}
	on := *body.On
	if on && g.cfg.APIKey == "" && (g.callerKeys == nil || !g.callerKeys.hasEnabled()) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "先在网关页创建一个调用方密钥（或设置主 API Key），否则远程请求会全部被拒绝",
		})
		return
	}
	if err := g.SetLAN(on); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	g.settingsMu.Lock()
	g.settings.LAN = on
	err := g.saveSettings()
	g.settingsMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, g.settingsView())
}

// settingsView 是 GET /api/settings 与局域网开关共用的设置视图：
// 打码后的设置 + 运行时计算字段（lan / lanURLs）。
func (g *Gateway) settingsView() map[string]any {
	g.settingsMu.Lock()
	masked := maskSettings(g.settings)
	ui := g.settings.UI
	origins := append([]string(nil), g.settings.CORSOrigins...)
	allowIPs := append([]string(nil), g.settings.AllowIPs...)
	globalProxy := g.settings.GlobalProxy
	g.settingsMu.Unlock()
	b, err := json.Marshal(masked)
	if err != nil {
		return map[string]any{"lan": g.LANEnabled(), "lanURLs": g.LANURLs()}
	}
	view := map[string]any{}
	if err := json.Unmarshal(b, &view); err != nil {
		view = map[string]any{}
	}
	for k, v := range ui {
		if _, exists := view[k]; !exists {
			view[k] = v
		}
	}
	// 远程实例：附 hasKey（Key 密文已在 maskSettings 里去掉）。
	g.settingsMu.Lock()
	remoteQuotas := maskedRemoteQuotaView(g.settings.RemoteQuotas)
	g.settingsMu.Unlock()
	if len(remoteQuotas) > 0 {
		view["remote_quotas"] = remoteQuotas
	}
	view["lan"] = g.LANEnabled()
	view["lanURLs"] = g.LANURLs()
	view["desktop_app"] = g.cfg.DesktopApp
	view["autostart"] = g.autoStartStatus()
	view["corsOrigins"] = origins
	view["allowIPs"] = allowIPs
	view["allowIPsEnv"] = ""
	view["allowIPsFromEnv"] = []string{}
	view["proxy"] = maskProxyURL(globalProxy)
	view["addr"] = g.PublicAddr()
	if _, portStr, err := net.SplitHostPort(g.PublicAddr()); err == nil {
		if n, conv := strconv.Atoi(portStr); conv == nil {
			view["port"] = n
		}
	}
	// 脱敏页/设置页把 prefs.redact 当作开关状态；以引擎实际状态为准。
	eng := g.redactEngine()
	if _, ok := view["redact"]; !ok {
		view["redact"] = eng.Enabled()
	}
	if _, ok := view["redactActions"]; !ok {
		if a := eng.Actions(); len(a) > 0 {
			view["redactActions"] = a
		}
	}
	if _, ok := view["redactPersonal"]; !ok {
		view["redactPersonal"] = eng.Personal()
	}
	// 用户规则与白名单以引擎为准（保存时已落盘；这里保证形状与顺序一致）。
	view["redactRules"] = customRulesView(eng)
	view["redactAllow"] = eng.Allow()
	// 设置页 "Config folder" 一行：路径与 Open 按钮（settings/reveal）。
	view["dir"] = g.configDir()
	view["portable"] = false
	return view
}
