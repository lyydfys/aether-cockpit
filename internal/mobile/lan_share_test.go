package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// 局域网共享（用户点名「并增加局域网」）的 Go 侧合同：
//
//  1. 默认只绑回环，LanEnabled=false；
//  2. ListenHost=0.0.0.0 时才绑所有 IPv4，并把可访问地址回报给宿主；
//  3. **管理面不因绑定放宽**：没有 Authorization 的 /api/* 仍然 401
//     `admin_auth_required`，query 里塞 key 也不认；
//  4. 除回环/0.0.0.0 之外的 host 一律拒绝（防止被改成绑公网）。

func lanLocalAddr(t *testing.T, addr string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	// 0.0.0.0 的监听从本机回环打即可命中同一个 listener。
	return net.JoinHostPort("127.0.0.1", port)
}

func lanGet(t *testing.T, url, header, value string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request %s: %v", url, err)
	}
	if header != "" {
		req.Header.Set(header, value)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestDefaultStartBindsLoopbackOnly(t *testing.T) {
	cfg := testConfig(t)
	cfg.ListenHost = "" // 默认值
	rt := startForTest(t, cfg)

	st := rt.Status()
	if !strings.HasPrefix(st.Addr, LoopbackHost+":") {
		t.Fatalf("默认必须只绑回环，addr = %q", st.Addr)
	}
	if st.LanEnabled {
		t.Fatal("默认不得开启局域网共享")
	}
	if len(st.LanURLs) != 0 {
		t.Fatalf("默认不该有局域网地址: %v", st.LanURLs)
	}
}

func TestLanSharingBindsAllIPv4AndReportsURLs(t *testing.T) {
	cfg := testConfig(t)
	cfg.ListenHost = LanHost
	// 模拟 Android：宿主探测到的 Wi-Fi 地址经配置注入
	//（Go 进程在 SELinux untrusted_app 域拿不到 NETLINK_ROUTE，枚举会为空）。
	cfg.LanAddressHint = "192.168.3.24"
	rt := startForTest(t, cfg)

	st := rt.Status()
	// 必须是 IPv4 通配绑定（tcp4 → "0.0.0.0:port"）：双栈 `[::]` 会把管理面
	// 同时挂到本机 IPv6 上，等于扩大暴露面。
	if !strings.HasPrefix(st.Addr, LanHost+":") {
		t.Fatalf("开启局域网共享必须绑 0.0.0.0（IPv4，不允许 [::] 双栈），addr = %q", st.Addr)
	}
	if !st.LanEnabled {
		t.Fatal("LanEnabled 必须为 true")
	}
	// listener 真的在服务：同机回环可达，且无鉴权的 /healthz 仍匿名（这是设计）。
	code, body := lanGet(t, "http://"+lanLocalAddr(t, st.Addr)+"/healthz", "", "")
	if code != http.StatusOK || !strings.Contains(body, "ok") {
		t.Fatalf("/healthz on LAN listener = %d %q", code, body)
	}
	// 回报的地址必须是 http://<非回环 IPv4>:<主监听端口>；拿不到就为空（允许，CI 可能没有网卡）。
	_, port, _ := net.SplitHostPort(st.Addr)
	for _, u := range st.LanURLs {
		if !strings.HasPrefix(u, "http://") {
			t.Fatalf("局域网地址必须是 http:// 开头: %q", u)
		}
		if !strings.HasSuffix(u, ":"+port) {
			t.Fatalf("局域网地址端口必须等于主监听端口 %s: %q", port, u)
		}
		if strings.Contains(u, "127.0.0.1") || strings.Contains(u, "0.0.0.0") {
			t.Fatalf("局域网地址不能是回环/通配地址: %q", u)
		}
	}
	// 宿主 hint 必须生效（这是 Android 上唯一可靠的来源）。
	want := "http://192.168.3.24:" + port
	if len(st.LanURLs) == 0 || st.LanURLs[0] != want {
		t.Fatalf("hint 未生效: want[0]=%q, got=%v", want, st.LanURLs)
	}
}

// lanShareURLs 是纯函数：hint 优先、非法/重复被丢弃、端口取自真实监听地址。
// 注意：跑测的机器如果有真实网卡，枚举结果也会并进列表，所以这里只断言
// "hint 排第一 / 只出现一次 / 脏值被丢"，不假设列表只有一项。
func TestLanShareURLsBuildsFromHintAndRejectsJunk(t *testing.T) {
	urls := lanShareURLs("0.0.0.0:42037", "192.168.3.24")
	if len(urls) == 0 || urls[0] != "http://192.168.3.24:42037" {
		t.Fatalf("hint 必须排第一: %v", urls)
	}
	urls = lanShareURLs(
		"0.0.0.0:42037",
		"192.168.3.24, 127.0.0.1; 0.0.0.0 | ::1 169.254.1.1 example.com  192.168.3.24",
	)
	count := 0
	for _, u := range urls {
		for _, bad := range []string{"127.0.0.1", "0.0.0.0", "169.254", "::1", "example.com"} {
			if strings.Contains(u, bad) {
				t.Fatalf("脏值混进列表: %q (in %v)", bad, urls)
			}
		}
		if u == "http://192.168.3.24:42037" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("hint 应只出现一次，实际 %d 次: %v", count, urls)
	}
	// 端口非法 → 没有地址可给，不能瞎编。
	if got := lanShareURLs("0.0.0.0:0", "192.168.3.24"); len(got) != 0 {
		t.Fatalf("端口 0 不该产出地址: %v", got)
	}
}

// 这条就是用户的要求：局域网打开后，管理面照样要会话凭据。
func TestLanSharingKeepsAdminPlaneAuthenticated(t *testing.T) {
	cfg := testConfig(t)
	cfg.AdminSessionKey = "" // 让运行时自己生成，避免注入值掩盖真实路径
	cfg.ListenHost = LanHost
	rt := startForTest(t, cfg)

	st := rt.Status()
	if !st.LanEnabled || st.AdminSession == "" {
		t.Fatalf("前置不成立: lanEnabled=%v sessionEmpty=%v", st.LanEnabled, st.AdminSession == "")
	}
	base := "http://" + lanLocalAddr(t, st.Addr)

	// 1) 完全不带 Authorization: 401 + admin_auth_required（不是 404、不是 200）
	code, body := lanGet(t, base+"/api/accounts", "", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("无凭据访问 /api/accounts 必须 401，实际 %d: %s", code, body)
	}
	var guard struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(body), &guard); err != nil || guard.Code != "admin_auth_required" {
		t.Fatalf("管理面守卫 = %+v，err = %v，body = %s", guard, err, body)
	}

	// 2) query 里塞会话 key 不认（只认 Authorization 头）
	code, body = lanGet(t, base+"/api/accounts?adminSession="+st.AdminSession, "", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("query 传会话 key 必须被拒，实际 %d: %s", code, body)
	}
	code, body = lanGet(t, base+"/api/accounts?key="+st.AdminSession, "", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("query 传 key 必须被拒，实际 %d: %s", code, body)
	}

	// 3) 调用方密钥不能解锁管理面
	code, body = lanGet(t, base+"/api/accounts", "Authorization", "Bearer "+st.CallerKey)
	if code != http.StatusUnauthorized {
		t.Fatalf("调用方 key 不得解锁管理面，实际 %d: %s", code, body)
	}

	// 4) 正确的会话凭据才 200
	code, body = lanGet(t, base+"/api/accounts", "Authorization", "Bearer "+st.AdminSession)
	if code != http.StatusOK {
		t.Fatalf("带会话凭据必须 200，实际 %d: %s", code, body)
	}
}

func TestStartRejectsArbitraryListenHost(t *testing.T) {
	cfg := testConfig(t)
	cfg.ListenHost = "192.168.1.5"
	_, err := Start(context.Background(), cfg)
	var se *StartError
	if !errors.As(err, &se) || se.Code != "listen_host_not_allowed" {
		t.Fatalf("err = %v，want listen_host_not_allowed", err)
	}
}

func TestLanSharingStopsBackToLoopback(t *testing.T) {
	cfg := testConfig(t)
	cfg.ListenHost = LanHost
	rt := startForTest(t, cfg)
	if !rt.Status().LanEnabled {
		t.Fatal("前置不成立")
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	st := rt.Status()
	if st.LanEnabled || st.Addr != "" || len(st.LanURLs) != 0 {
		t.Fatalf("停掉后不得残留局域网状态: %+v", st)
	}
}
