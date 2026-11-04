package mobile

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestDiagnoseReportsLoopbackListen 覆盖真机上抓到的一条**误报**：
//
// 移动运行时构造 `gateway.Config` 时没有设置 `Addr`，于是 `g.cfg.Addr == ""`，
// 而诊断的 `isPublicListen("")` 把空串当成"监听所有网卡"——自检里出现
// 「[错误] 监听所有网卡但未设置 API Key」，可手机端明明只绑 127.0.0.1。
//
// 期望：运行时把真实监听地址交给网关配置，自检据此给出「监听地址安全」，
// 且**不再出现**"监听所有网卡"。
func TestDiagnoseReportsLoopbackListen(t *testing.T) {
	cfg := testConfig(t)
	rt := startForTest(t, cfg)
	defer func() {
		_ = rt.Stop(context.Background())
	}()

	st := rt.Status()
	if st.State != StateRunning || st.Addr == "" {
		t.Fatalf("status = %+v", st)
	}

	code, body := getAdminJSON(t, st.Addr, "/api/diagnose", cfg.AdminSessionKey)
	if code != http.StatusOK {
		t.Fatalf("/api/diagnose status = %d body=%s", code, body)
	}
	if strings.Contains(body, "监听所有网卡") {
		t.Fatalf("自检把回环监听误报成公网监听（运行时没给 gateway.Config 传 Addr）: %s", body)
	}
	if !strings.Contains(body, "监听地址安全") {
		t.Fatalf("自检没有给出「监听地址安全」: %s", body)
	}
	// 报出来的地址必须就是运行时实际绑定的那个回环地址。
	if !strings.Contains(body, st.Addr) {
		t.Fatalf("自检里的监听地址与实际绑定不一致：want 含 %q，body=%s", st.Addr, body)
	}
}
