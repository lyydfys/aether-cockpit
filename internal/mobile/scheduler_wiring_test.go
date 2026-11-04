package mobile

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// getAdminJSON 用管理会话读一个 /api 端点（移动模式下 /api/* 必须带 Bearer）。
func getAdminJSON(t *testing.T, addr, path, key string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		t.Fatalf("build request %s: %v", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp.StatusCode, string(body)
}

// TestRuntimeStartsCheckinScheduler 覆盖 Stage 6 的最后一块缺口：
// 移动运行时以前从不构造调度器，导致 `GET /api/scheduler` 恒返回 `enabled:false`、
// `POST /api/scheduler/run` 恒 503——也就是"签到"在手机端等于不存在。
//
// 期望：启动运行时之后，调度器真的被构造并挂到网关上，`/api/scheduler` 返回 enabled:true。
func TestRuntimeStartsCheckinScheduler(t *testing.T) {
	cfg := testConfig(t)
	rt := startForTest(t, cfg)
	defer func() {
		_ = rt.Stop(context.Background())
	}()

	st := rt.Status()
	if st.State != StateRunning || st.Addr == "" {
		t.Fatalf("status = %+v", st)
	}

	code, body := getAdminJSON(t, st.Addr, "/api/scheduler", cfg.AdminSessionKey)
	if code != http.StatusOK {
		t.Fatalf("/api/scheduler status = %d body=%s", code, body)
	}
	if !strings.Contains(body, `"enabled":true`) {
		t.Fatalf("移动运行时没有接线签到调度器（仍是未启动态）: %s", body)
	}
	// 未接线时会带这个 reason；接线后不应再出现。
	if strings.Contains(body, "调度器未启动") {
		t.Fatalf("/api/scheduler 仍报告调度器未启动: %s", body)
	}

	// 手动触发端点也必须可用（以前恒 503）。
	code, body = postAdminJSON(t, st.Addr, "/api/scheduler/run", cfg.AdminSessionKey, `{}`)
	if code == http.StatusServiceUnavailable {
		t.Fatalf("POST /api/scheduler/run 仍 503（调度器未接线）: %s", body)
	}
}

// postAdminJSON 触发类端点用。
func postAdminJSON(t *testing.T, addr, path, key, payload string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+path, strings.NewReader(payload))
	if err != nil {
		t.Fatalf("build request %s: %v", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp.StatusCode, string(body)
}
