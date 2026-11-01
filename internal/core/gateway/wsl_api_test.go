package gateway

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/core/clients"
)

// wslTestGateway 造一个带 WSL 覆盖目标（临时目录）的网关。
func wslTestGateway(t *testing.T) (*Gateway, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("AETHER_WSL_HOMES", home)
	t.Setenv("AETHER_WSL_DISTRO", "Ubuntu-22.04")
	g := New(Config{Addr: "127.0.0.1:7860"})
	return g, home
}

func TestWSLTargetsFromHomes(t *testing.T) {
	g, home := wslTestGateway(t)
	targets := g.wslTargets()
	if len(targets) != 1 {
		t.Fatalf("wslTargets() = %+v", targets)
	}
	tg := targets[0]
	if tg.ID != "wsl-ubuntu-22-04" || tg.WSL != "Ubuntu-22.04" || tg.Home != home {
		t.Errorf("目标映射错误：%+v", tg)
	}
	if tg.Label != "WSL Ubuntu-22.04" {
		t.Errorf("Label = %q", tg.Label)
	}
}

func TestClientsListsWSLRows(t *testing.T) {
	g, _ := wslTestGateway(t)
	rec := httptest.NewRecorder()
	g.handleClients(rec, httptest.NewRequest(http.MethodGet, "/api/clients", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Clients []struct {
			ID   string `json:"id"`
			WSL  string `json:"wsl"`
			Path string `json:"path"`
		} `json:"clients"`
		Targets []struct {
			ID string `json:"id"`
		} `json:"targets"`
		WSL map[string]any `json:"wsl"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是 JSON: %v", err)
	}
	found := false
	for _, c := range body.Clients {
		if c.ID == "wsl-ubuntu-22-04--claude_code" {
			found = true
			if c.WSL != "Ubuntu-22.04" {
				t.Errorf("wsl 字段 = %q", c.WSL)
			}
			if !strings.Contains(c.Path, ".claude") {
				t.Errorf("path = %q", c.Path)
			}
		}
	}
	if !found {
		t.Fatalf("clients 里没有 WSL 条目：%+v", body.Clients)
	}
	if len(body.Targets) != 1 || body.Targets[0].ID != "wsl-ubuntu-22-04" {
		t.Errorf("targets = %+v", body.Targets)
	}
	if body.WSL == nil {
		t.Error("响应里缺少 wsl 视图")
	}
}

func TestWSLApplyRequiresReachableEntry(t *testing.T) {
	g, _ := wslTestGateway(t)
	mgr := g.clientsManager()
	// 真实探测：本机有 WSL 时能拿到地址并开监听；没有时必须是明确错误，
	// 绝不能把 127.0.0.1 写进 WSL 配置（那是发行版自己的回环）。
	origin, err := g.clientOriginFor(httptest.NewRequest(http.MethodPost, "/api/clients/x/apply", nil), mgr, "wsl-ubuntu-22-04--claude_code")
	if err != nil {
		t.Skipf("本机无 WSL 入口，跳过（错误是明确的）：%v", err)
	}
	if !strings.HasPrefix(origin, "http://") {
		t.Fatalf("origin = %q", origin)
	}
	if strings.Contains(origin, "127.0.0.1") || strings.Contains(origin, "localhost") {
		t.Fatalf("WSL 入口不能是回环地址：%q", origin)
	}
	g.CloseWSL()
}

func TestServedOnWSLUsesLocalAddr(t *testing.T) {
	g := New(Config{Addr: "127.0.0.1:7860"})
	g.wslListen.ip = "172.27.16.1"

	withLocal := func(local string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
		req.RemoteAddr = "172.27.16.5:4321"
		ctx := context.WithValue(req.Context(), http.LocalAddrContextKey,
			&net.TCPAddr{IP: net.ParseIP(local), Port: 7860})
		return req.WithContext(ctx)
	}

	if !g.adminAllowed(withLocal("172.27.16.1")) {
		t.Fatal("从 WSL 网卡地址进来的请求应放行（与回环同一信任边界）")
	}
	if g.adminAllowed(withLocal("192.168.3.48")) {
		t.Fatal("落在局域网地址上的请求不应被当成 WSL")
	}

	// 没有 LocalAddr（进程内调用）时不因 WSL 而误放行：仍走远程规则。
	plain := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	plain.RemoteAddr = "10.0.0.9:3333"
	if g.adminAllowed(plain) {
		t.Fatal("未配置 API Key 时远程请求不应放行")
	}
}

func TestWSLViewShowsDetectedHome(t *testing.T) {
	g, home := wslTestGateway(t)
	view := g.wslView()
	homes, _ := view["homes"].([]map[string]any)
	if len(homes) != 1 {
		t.Fatalf("homes = %+v", view["homes"])
	}
	if homes[0]["distro"] != "Ubuntu-22.04" || homes[0]["path"] != home {
		t.Errorf("home 条目错误：%+v", homes[0])
	}
	if view["port"] != 7860 {
		t.Errorf("port = %v", view["port"])
	}
}

// TestStateShowsWSLAgentWired 锁住前端契约：Agents 页读 /api/state 的
// agents[].wired，WSL 客户端接入后必须出现"已接入"的 WSL 行。
func TestStateShowsWSLAgentWired(t *testing.T) {
	g, _ := wslTestGateway(t)
	mgr := g.clientsManager()
	if _, err := mgr.Apply("wsl-ubuntu-22-04--claude_code", clients.ApplyParams{
		BaseURL: "http://172.27.16.1:7860", APIKey: "k",
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	st := g.buildMagpieState()
	var found *magpieAgent
	for i := range st.Agents {
		if st.Agents[i].ID == "wsl-ubuntu-22-04--claude_code" {
			found = &st.Agents[i]
		}
	}
	if found == nil {
		t.Fatalf("state.agents 里没有 WSL 行：%d 条", len(st.Agents))
	}
	if !found.Wired {
		t.Errorf("接入后 wired 应为 true：%+v", found)
	}
	if !strings.Contains(found.Name, "WSL") {
		t.Errorf("行名应带 WSL 标记：%q", found.Name)
	}
}
