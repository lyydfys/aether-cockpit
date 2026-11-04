package mobile

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestListenAddrConsistentEverywhere：同一个监听地址必须在**四处**给出同一个值。
//
// 背景：上一轮修掉了「移动运行时没给 gateway.Config 传 Addr」导致的误报。只验自检不够——
// 设置页、概览、/api/runtime、/api/settings 都可能各自读不同字段。这里把它们锁在一起：
//
//	rt.Status().Addr  ==  /api/settings 里出现  ==  /api/runtime 里出现  ==  /api/diagnose 里出现
func TestListenAddrConsistentEverywhere(t *testing.T) {
	cfg := testConfig(t)
	rt := startForTest(t, cfg)
	defer func() {
		_ = rt.Stop(context.Background())
	}()

	st := rt.Status()
	if st.State != StateRunning || st.Addr == "" {
		t.Fatalf("status = %+v", st)
	}
	want := st.Addr
	if !strings.HasPrefix(want, "127.0.0.1:") {
		t.Fatalf("移动运行时应当只绑回环，实际 %q", want)
	}

	for _, path := range []string{"/api/settings", "/api/runtime", "/api/diagnose"} {
		code, body := getAdminJSON(t, st.Addr, path, cfg.AdminSessionKey)
		if code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", path, code, body)
		}
		if !strings.Contains(body, want) {
			t.Fatalf("%s 里没有出现实际监听地址 %q：%s", path, want, body)
		}
	}
}
