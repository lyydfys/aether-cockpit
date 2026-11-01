package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"aether-cockpit/internal/core/clients"
	"aether-cockpit/internal/core/sessions"
	"aether-cockpit/internal/core/wsl"
)

// WSL 接入：把本机 Windows 侧的客户端注入扩展到 WSL 发行版里。
//
// 为什么需要单独的监听：WSL2 是 NAT，发行版里的 127.0.0.1 是它自己。
// 本机实测（2026-10-28，Ubuntu-22.04）：
//   - WSL → http://127.0.0.1:<port> 不通；
//   - WSL → http://<WSL 虚拟网卡 IP>:<port> 通；
//   - 绑定 <WSL 虚拟网卡 IP>:<port> 后，WSL 能连、局域网地址连不上。
//
// 因此这里按需在 WSL 网卡地址上再开一个监听（同一端口、不同地址，
// 与回环监听互不冲突），WSL 里的客户端只看到这个地址。它的信任边界
// 与回环相同：只有本机的 WSL 虚拟网络能到达（见 servedOnWSL）。

// syncWSLEnabled 把"WSL 接入"开关同步给会话扫描器——它也要停止枚举 WSL 根，
// 否则关掉开关后 /api/sessions 仍在跑 wsl.exe 与 UNC stat。
func (g *Gateway) syncWSLEnabled() {
	g.settingsMu.Lock()
	off := g.settings.WSLDisabled
	g.settingsMu.Unlock()
	sessions.SetWSLEnabled(!off)
}

// warmSessions 在 WSL 关闭时后台预热会话列表：首次要读几百个 jsonl 的文件头
// （实测 ~4 s），热了之后是毫秒级。WSL 开着时不预热——那会顺带枚举 WSL。
func (g *Gateway) warmSessions() {
	g.settingsMu.Lock()
	off := g.settings.WSLDisabled
	g.settingsMu.Unlock()
	if off {
		go sessions.Scan()
	}
}

// wslTargets 把检测到的 WSL 家目录映射成客户端注入目标。
func (g *Gateway) wslTargets() []clients.Target {
	g.settingsMu.Lock()
	off := g.settings.WSLDisabled
	g.settingsMu.Unlock()
	if off {
		// 关掉接入时连检测都不做：不跑 wsl.exe、不 stat UNC。
		return nil
	}
	// 用非阻塞版本：首次检测可能要几十秒，库页/客户端页不该等它。
	homes, _ := wsl.TryHomes()
	out := make([]clients.Target, 0, len(homes))
	for _, h := range homes {
		slug := wsl.Slug(h.Distro)
		if slug == "" {
			slug = "wsl"
		}
		out = append(out, clients.Target{
			ID:    "wsl-" + slug,
			Label: "WSL " + h.Distro,
			Home:  h.Path,
			WSL:   h.Distro,
		})
	}
	return out
}

type wslListener struct {
	mu   sync.Mutex
	srv  *http.Server
	addr string
	ip   string
}

// wslIP 返回（并缓存）WSL 网卡在 Windows 侧的地址。
func (g *Gateway) wslIP() string {
	g.wslListen.mu.Lock()
	defer g.wslListen.mu.Unlock()
	if g.wslListen.ip == "" {
		if ip, err := wsl.HostIP(); err == nil {
			g.wslListen.ip = ip
		}
	}
	return g.wslListen.ip
}

// ensureWSLEntry 确保 WSL 可达的监听在跑，返回 WSL 里应使用的基地址。
func (g *Gateway) ensureWSLEntry() (string, error) {
	ip, err := wsl.HostIP()
	if err != nil {
		return "", err
	}
	port := lanPort(g.PublicAddr())
	if port == 0 {
		return "", fmt.Errorf("无法确定网关端口（%s）", g.PublicAddr())
	}
	base := "http://" + net.JoinHostPort(ip, strconv.Itoa(port))

	// 主监听已经绑定通配地址（0.0.0.0 / ::）时，WSL 直接用这个地址即可。
	if host, _, err := net.SplitHostPort(g.PublicAddr()); err == nil {
		switch host {
		case "", "0.0.0.0", "::", "[::]":
			return base, nil
		}
	}

	want := net.JoinHostPort(ip, strconv.Itoa(port))
	g.wslListen.mu.Lock()
	defer g.wslListen.mu.Unlock()
	g.wslListen.ip = ip
	if g.wslListen.srv != nil && g.wslListen.addr == want {
		return base, nil
	}
	if g.wslListen.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = g.wslListen.srv.Shutdown(ctx)
		cancel()
		g.wslListen.srv, g.wslListen.addr = nil, ""
	}
	ln, err := net.Listen("tcp", want)
	if err != nil {
		return "", fmt.Errorf("无法在 WSL 网卡地址 %s 上监听：%v", want, err)
	}
	srv := &http.Server{Handler: g.mux, ReadHeaderTimeout: 30 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	g.wslListen.srv, g.wslListen.addr = srv, want
	return base, nil
}

// CloseWSL 停掉 WSL 监听（进程退出时）。
func (g *Gateway) CloseWSL() {
	g.wslListen.mu.Lock()
	defer g.wslListen.mu.Unlock()
	if g.wslListen.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = g.wslListen.srv.Shutdown(ctx)
		cancel()
	}
	g.wslListen.srv, g.wslListen.addr = nil, ""
}

// servedOnWSL 报告请求是否到达 WSL 专用监听。
//
// 用 LocalAddr 判断而不是 RemoteAddr：监听只绑在 WSL 网卡地址上，
// 内核保证从该网卡进来的连接 LocalAddr 就是它；外网/局域网请求的
// LocalAddr 是别的地址，无法伪造。
func (g *Gateway) servedOnWSL(r *http.Request) bool {
	ip := g.wslIP()
	if ip == "" {
		return false
	}
	la, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return false
	}
	host, _, err := net.SplitHostPort(la.String())
	if err != nil {
		return false
	}
	got := net.ParseIP(host)
	return got != nil && got.Equal(net.ParseIP(ip))
}

// handleWSL GET /api/wsl —— WSL 发行版检测结果与接入入口。
func (g *Gateway) handleWSL(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.wslView())
}

// handleWSLRefresh POST /api/wsl/refresh —— 丢掉检测缓存重扫一次。
func (g *Gateway) handleWSLRefresh(w http.ResponseWriter, r *http.Request) {
	wsl.Invalidate()
	writeJSON(w, http.StatusOK, g.wslView())
}

// clientOriginFor 计算某客户端条目应写入的网关地址：WSL 条目用 WSL 网卡
// 入口（按需开启监听），本机条目沿用请求来源。
func (g *Gateway) clientOriginFor(r *http.Request, mgr *clients.Manager, id string) (string, error) {
	if tgt, ok := mgr.TargetOf(id); ok {
		if tgt.WSL != "" {
			// 先把发行版拉起来：否则 UNC 上的"文件不存在"可能是发行版
			// 没跑，配置合并会误判成首次接入而丢掉用户原字段。
			if err := wsl.EnsureRunning(tgt.WSL); err != nil {
				return "", err
			}
		}
		return g.ensureWSLEntry()
	}
	return requestOrigin(r, g.PublicAddr()), nil
}

// wslView 是 /api/clients 与 /api/wsl 共用的 WSL 状态。
func (g *Gateway) wslView() map[string]any {
	g.settingsMu.Lock()
	off := g.settings.WSLDisabled
	g.settingsMu.Unlock()
	if off {
		return map[string]any{
			"available": false, "disabled": true, "homes": []any{},
			"reason": "WSL 接入已在设置中关闭（设置 → 常规 → WSL 接入）；重新打开后检测才会再运行",
		}
	}
	// 非阻塞：首次检测可能要几十秒（wsl.exe 冷启动 + UNC stat），界面先拿到
	// "正在检测"，由后台补上结果，而不是让整个库页卡住（实测 30.9 s → 毫秒）。
	homes, ready := wsl.TryHomes()
	ip, ipErr := wsl.HostIP()
	port := lanPort(g.PublicAddr())
	hosts := make([]map[string]any, 0, len(homes))
	for _, h := range homes {
		hosts = append(hosts, map[string]any{"distro": h.Distro, "path": h.Path})
	}
	out := map[string]any{
		// IP 探测只在 Windows 有 WSL2 时成功；没有 WSL 的机器上这是
		// "不可用 + 原因"，不是错误状态。
		"available": ipErr == nil && len(homes) > 0,
		"homes":     hosts,
		"port":      port,
		"hostIP":    ip,
	}
	if ipErr == nil && port > 0 {
		out["baseURL"] = "http://" + net.JoinHostPort(ip, strconv.Itoa(port))
	}
	if !ready {
		out["detecting"] = true
	}
	switch {
	case ipErr != nil:
		out["reason"] = ipErr.Error()
	case len(homes) == 0:
		if !ready {
			out["reason"] = "正在后台检测 WSL（首次较慢，约数十秒），稍后点「重新检测」"
			break
		}
		// 有 WSL 但家目录为空时，界面上必须说清原因：是发行版冷启动没响应，
		// 还是里面根本没装客户端——否则只显示"不可用"，用户无从下手。
		if p := wsl.LastProblem(); p != "" {
			out["reason"] = p
		}
	}
	return out
}
