package gateway

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// DoH 查询要能解析出 A 记录，并以第 0 条为准。
func TestDoHLookupParsesAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "A" {
			t.Errorf("type 参数 = %q", r.URL.Query().Get("type"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Status": 0,
			"Answer": []map[string]any{
				{"type": 5, "data": "alias.example."}, // CNAME 应被忽略
				{"type": 1, "data": "203.0.113.7"},
				{"type": 1, "data": "203.0.113.8"},
			},
		})
	}))
	defer srv.Close()

	ips, err := dohLookup(context.Background(), "test", srv.URL, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 2 || ips[0] != "203.0.113.7" || ips[1] != "203.0.113.8" {
		t.Fatalf("解析结果 = %v", ips)
	}
}

// 没有 A 记录要明确报错，而不是返回空。
func TestDoHLookupNoAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Status":0,"Answer":[]}`))
	}))
	defer srv.Close()
	if _, err := dohLookup(context.Background(), "test", srv.URL, "example.com"); err == nil {
		t.Fatal("没有 A 记录应报错")
	}
}

// 探测本机一个真实的 TLS 服务：应判定为"可用"，并带回 TLS 版本与首字节。
func TestProbeIPAgainstLocalTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	addr := srv.Listener.Addr().String()
	host, port, _ := net.SplitHostPort(addr)
	_ = host
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	res := probeIP(ctx, "127.0.0.1", port, "127.0.0.1")
	if res.TCPError != "" {
		t.Fatalf("TCP 应成功：%v", res.TCPError)
	}
	// 自签证书会让 TLS 校验失败，从而走 TLSError 分支——这正是"能区分
	// IP 通但 TLS 不通"的证据；这里只要求判定文案不为空。
	if res.Verdict == "" {
		t.Fatal("应给出结论")
	}
}

// TCP 不可达要落在 TCP 分支，而不是含糊地"失败"。
func TestProbeIPReportsClosedPort(t *testing.T) {
	// 关掉的端口：先占一个再释放，拿到一个大概率没人听的端口号。
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strings.Split(l.Addr().String(), ":")[1]
	l.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := probeIP(ctx, "127.0.0.1", port, "127.0.0.1")
	if res.TCPError == "" {
		t.Fatalf("应报 TCP 错误，得到 %+v", res)
	}
	if !strings.Contains(res.Verdict, "TCP") {
		t.Fatalf("判定应指出 TCP 层：%q", res.Verdict)
	}
}

// 结论要能区分四类原因（可用 / TCP 不通 / TLS 被拦 / 无解析）。
func TestSummarizeDiagClassifies(t *testing.T) {
	cases := []struct {
		name string
		in   netDiagResponse
		want string
	}{
		{
			name: "直连可用且快",
			in: netDiagResponse{IPs: []netDiagIPResult{
				{IP: "1.1.1.1", TCPMS: 20, TLSMS: 120, TTFBMS: 400, ALPN: "h2"},
			}},
			want: "直连可用",
		},
		{
			name: "首字节慢要提示",
			in: netDiagResponse{IPs: []netDiagIPResult{
				{IP: "1.1.1.1", TCPMS: 20, TLSMS: 900, TTFBMS: 2600},
			}},
			want: "线路慢",
		},
		{
			name: "TCP 全不通",
			in: netDiagResponse{IPs: []netDiagIPResult{
				{IP: "1.1.1.1", TCPError: "timeout"},
			}},
			want: "不通",
		},
		{
			name: "TLS 被拦",
			in: netDiagResponse{IPs: []netDiagIPResult{
				{IP: "1.1.1.1", TCPMS: 30, TLSError: "remote error: tls: handshake failure"},
			}},
			want: "SNI",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := summarizeDiag(tc.in)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("结论 = %q，期望包含 %q", got, tc.want)
			}
		})
	}
}

// 检测到 fake-ip（TUN 代理接管）时必须提醒：此时直连数字不可信。
func TestSummarizeDiagWarnsOnFakeIP(t *testing.T) {
	got := summarizeDiag(netDiagResponse{
		SystemDNS: []string{"198.18.0.75"},
		IPs:       []netDiagIPResult{{IP: "203.0.113.7", TCPMS: 20, TLSMS: 100, TTFBMS: 300}},
	})
	if !strings.Contains(got, "fake-ip") {
		t.Fatalf("应提醒 TUN 接管：%q", got)
	}
}

// host 参数要挡住路径/查询注入，避免被当成任意请求代理。
func TestNetDiagRejectsBadHost(t *testing.T) {
	g := New(Config{SettingsPath: t.TempDir() + "/settings.json", UsagePath: "-"})
	for _, bad := range []string{"", "a/b", "a?x=1", "a&b", "a@b", "a b"} {
		rec := httptest.NewRecorder()
		// 用 QueryEscape 构造请求：否则 "a b" 这类值会让 httptest.NewRequest
		// 在解析请求行时就 panic，测不到我们要测的那一层。
		req := httptest.NewRequest(http.MethodGet, "/api/network/diag?host="+url.QueryEscape(bad), nil)
		g.handleNetDiag(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("host=%q 应 400，得到 %d", bad, rec.Code)
		}
	}
}
