package gateway

import (
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/transport"
	"aether-cockpit/internal/provider"
)

// POST /api/proxy/check：问 Cloudflare 的 trace 页，从直连与当前全局代理
// 各发一次，回两个出口地址，供设置页的 Test 按钮判断代理是否真的生效。
// 代理为空的运行时没有 through 这一段。
var proxyTraceURL = "https://www.cloudflare.com/cdn-cgi/trace"

type proxyLeg struct {
	IP      string `json:"ip,omitempty"`
	Loc     string `json:"loc,omitempty"`
	MS      int64  `json:"ms,omitempty"`
	Error   string `json:"error,omitempty"`
	Proxied bool   `json:"proxied,omitempty"`
}

func traceThrough(client *http.Client, proxied bool) proxyLeg {
	start := time.Now()
	req, err := http.NewRequest(http.MethodGet, proxyTraceURL, nil)
	if err != nil {
		return proxyLeg{Error: err.Error(), Proxied: proxied}
	}
	req.Header.Set("User-Agent", "AetherCockpit/proxy-check")
	ctx, cancel := contextWithTimeout(req, 8*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := client.Do(req)
	leg := proxyLeg{Proxied: proxied}
	if err != nil {
		leg.Error = err.Error()
		return leg
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	leg.MS = time.Since(start).Milliseconds()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		leg.Error = "HTTP " + resp.Status
		return leg
	}
	for _, line := range strings.Split(string(body), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "ip":
			leg.IP = v
		case "loc":
			leg.Loc = v
		}
	}
	if leg.IP == "" {
		leg.Error = "trace 页面没有返回出口地址"
	}
	return leg
}

func (g *Gateway) handleProxyCheck(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}
	directClient, _ := g.tr.NewClient(provider.NetPolicyDirectForce, "", "")
	out["direct"] = traceThrough(directClient, false)

	if current := strings.TrimSpace(g.tr.GlobalProxyValue()); current != "" {
		client, err := transport.NewFactory(current).NewClient(provider.NetPolicyProxyFirst, "", "")
		if err != nil {
			out["through"] = proxyLeg{Error: err.Error(), Proxied: true}
		} else {
			out["through"] = traceThrough(client, true)
		}
	}
	writeJSON(w, http.StatusOK, out)
}
