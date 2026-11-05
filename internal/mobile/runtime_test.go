package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testConfig(t *testing.T) RuntimeConfig {
	t.Helper()
	base := t.TempDir()
	return RuntimeConfig{
		DataDir:         filepath.Join(base, "aether"),
		CacheDir:        filepath.Join(base, "files-cache"),
		TempDir:         filepath.Join(base, "cache-tmp"),
		ListenHost:      "127.0.0.1",
		Port:            0,
		Platform:        "android",
		AdminSessionKey: "do-not-log",
	}
}

func startForTest(t *testing.T, cfg RuntimeConfig) *Runtime {
	t.Helper()
	rt, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })
	return rt
}

func getHealth(t *testing.T, addr string) string {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read health: %v", err)
	}
	return string(body)
}

func TestStartHealthStop(t *testing.T) {
	cfg := testConfig(t)
	rt := startForTest(t, cfg)

	st := rt.Status()
	if st.State != StateRunning || st.Addr == "" {
		t.Fatalf("status = %+v", st)
	}
	paths := rt.Paths()
	for _, dir := range []string{paths.DataDir, paths.UsageDir, paths.CacheDir, paths.TempDir} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("dir %s: %v", dir, err)
		}
	}
	if want := filepath.Join(cfg.DataDir, "state.db"); paths.StateDB != want {
		t.Fatalf("StateDB = %q, want %q", paths.StateDB, want)
	}
	if _, err := os.Stat(paths.StateDB); err != nil {
		t.Fatalf("state layer must create %s: %v", paths.StateDB, err)
	}
	if want := filepath.Join(cfg.DataDir, "logs", "aether.log"); paths.LogPath != want {
		t.Fatalf("LogPath = %q, want %q", paths.LogPath, want)
	}
	if info, err := os.Stat(paths.LogPath); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("log file %s: %v", paths.LogPath, err)
	}
	if got := getHealth(t, st.Addr); got != `{"status":"ok"}` {
		t.Fatalf("health = %q", got)
	}

	addr := st.Addr
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if st := rt.Status(); st.State != StateStopped || st.Addr != "" {
		t.Fatalf("after stop = %+v", st)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	logData, err := os.ReadFile(paths.LogPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	logText := string(logData)
	for _, want := range []string{"mobile runtime started", "mobile runtime stopped"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q: %s", want, logText)
		}
	}
	for _, forbidden := range []string{cfg.AdminSessionKey, cfg.DataDir} {
		if strings.Contains(logText, forbidden) {
			t.Fatalf("log leaked %q: %s", forbidden, logText)
		}
	}
	if _, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
		t.Fatalf("old listener %s still accepts", addr)
	}
}

func TestUsageAggregateFlushedOnStop(t *testing.T) {
	cfg := testConfig(t)
	rt := startForTest(t, cfg)
	paths := rt.Paths()
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if info, err := os.Stat(paths.UsagePath); err != nil || info.Size() == 0 {
		t.Fatalf("usage aggregate %s missing after Stop: %v", paths.UsagePath, err)
	}
	var file struct {
		Days map[string]json.RawMessage `json:"days"`
	}
	raw, err := os.ReadFile(paths.UsagePath)
	if err != nil {
		t.Fatalf("read usage: %v", err)
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("usage json: %v", err)
	}
	if file.Days == nil {
		t.Fatalf("usage json has no days: %s", raw)
	}

	// The next runtime must read the same aggregate and stop cleanly.
	rt2 := startForTest(t, cfg)
	if err := rt2.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestThreeStartHealthStopCycles(t *testing.T) {
	for i := 0; i < 3; i++ {
		rt := startForTest(t, testConfig(t))
		addr := rt.Status().Addr
		if got := getHealth(t, addr); got != `{"status":"ok"}` {
			t.Fatalf("cycle %d health = %q", i+1, got)
		}
		if err := rt.Stop(context.Background()); err != nil {
			t.Fatalf("cycle %d stop: %v", i+1, err)
		}
		if _, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			t.Fatalf("cycle %d old listener %s still accepts", i+1, addr)
		}
	}
}

func TestRuntimeServesGatewayAndGuardsManagement(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)

	cfg := testConfig(t)
	rt := startForTest(t, cfg)
	paths := rt.Paths()
	status := rt.Status()
	if status.AdminSession != cfg.AdminSessionKey {
		t.Fatalf("AdminSession = %q, want the injected key", status.AdminSession)
	}
	if len(status.CallerKey) != 43 || status.CallerKey == status.AdminSession {
		t.Fatalf("CallerKey = %q, want an independent ephemeral key", status.CallerKey)
	}
	if info, err := os.Stat(paths.ModelsDir); err != nil || !info.IsDir() {
		t.Fatalf("ModelsDir %s: %v", paths.ModelsDir, err)
	}
	if info, err := os.Stat(paths.SpecDir); err != nil || !info.IsDir() {
		t.Fatalf("SpecDir %s: %v", paths.SpecDir, err)
	}

	client := &http.Client{Timeout: 3 * time.Second}
	base := "http://" + status.Addr
	authorized := func(path, key string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		return resp
	}

	resp, err := client.Get(base + "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("model API without caller key status = %d: %s", resp.StatusCode, body)
	}
	var callerGuard struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &callerGuard); err != nil || callerGuard.Code != "caller_auth_required" {
		t.Fatalf("caller guard = %+v, err = %v", callerGuard, err)
	}

	resp = authorized("/v1/models", status.CallerKey)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("GET /v1/models with caller key status = %d: %s", resp.StatusCode, body)
	}
	var list struct {
		Object string            `json:"object"`
		Data   []json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		resp.Body.Close()
		t.Fatalf("decode /v1/models: %v", err)
	}
	resp.Body.Close()
	if list.Object != "list" || list.Data == nil {
		t.Fatalf("/v1/models = %+v", list)
	}

	// 两把 key 互不通用。
	resp = authorized("/v1/models", status.AdminSession)
	if resp.StatusCode != http.StatusUnauthorized {
		resp.Body.Close()
		t.Fatalf("admin key must not unlock /v1, status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp, err = client.Get(base + "/api/providers")
	if err != nil {
		t.Fatalf("GET /api/providers: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("management API without session status = %d: %s", resp.StatusCode, body)
	}
	var guard struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &guard); err != nil || guard.Code != "admin_auth_required" {
		t.Fatalf("management guard = %+v, err = %v", guard, err)
	}

	resp = authorized("/api/providers", status.CallerKey)
	if resp.StatusCode != http.StatusUnauthorized {
		resp.Body.Close()
		t.Fatalf("caller key must not unlock management, status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = authorized("/api/providers", status.AdminSession)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorized management API status = %d: %s", resp.StatusCode, body)
	}

	resp, err = client.Get(base + "/v1/definitely-missing")
	if err != nil {
		t.Fatalf("GET /v1/definitely-missing: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown /v1 route status = %d, want 404 from gateway", resp.StatusCode)
	}

	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	logData, err := os.ReadFile(paths.LogPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(logData), status.AdminSession) || strings.Contains(string(logData), status.CallerKey) {
		t.Fatalf("log leaked a session key: %s", logData)
	}

	if _, err := os.Stat(filepath.Join(cwd, "data")); !os.IsNotExist(err) {
		t.Fatalf("gateway must not use relative data/: %v", err)
	}
}

func TestStartGeneratesRandomSessionKeys(t *testing.T) {
	admins := make([]string, 0, 2)
	callers := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		cfg := testConfig(t)
		cfg.AdminSessionKey = ""
		rt, err := Start(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		status := rt.Status()
		if len(status.AdminSession) != 43 || len(status.CallerKey) != 43 {
			t.Fatalf("sessions = %q / %q, want two 43-char keys", status.AdminSession, status.CallerKey)
		}
		if status.AdminSession == status.CallerKey {
			t.Fatal("admin session and caller key must differ")
		}
		if err := rt.Stop(context.Background()); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		after := rt.Status()
		if after.AdminSession != "" || after.CallerKey != "" {
			t.Fatal("Stop should clear both in-memory keys")
		}
		admins = append(admins, status.AdminSession)
		callers = append(callers, status.CallerKey)
	}
	if admins[0] == admins[1] || callers[0] == callers[1] {
		t.Fatal("each start must generate fresh session keys")
	}
}

// 只有回环与 0.0.0.0（局域网共享）两种 host 被接受；其余一律拒绝。
// 0.0.0.0 的用例见 lan_share_test.go（TestLanSharingBindsAllIPv4AndReportsURLs）。
func TestStartRejectsNonLoopback(t *testing.T) {
	cfg := testConfig(t)
	cfg.ListenHost = "192.168.1.5"
	_, err := Start(context.Background(), cfg)
	var se *StartError
	if !errors.As(err, &se) || se.Code != "listen_host_not_allowed" {
		t.Fatalf("err = %v, want listen_host_not_allowed", err)
	}
}

func TestStartRejectsUnknownPlatform(t *testing.T) {
	cfg := testConfig(t)
	cfg.Platform = "windows"
	_, err := Start(context.Background(), cfg)
	var se *StartError
	if !errors.As(err, &se) || se.Code != "unsupported_platform" {
		t.Fatalf("err = %v, want unsupported_platform", err)
	}
}

func TestStartRejectsEmptyDataDir(t *testing.T) {
	cfg := testConfig(t)
	cfg.DataDir = ""
	_, err := Start(context.Background(), cfg)
	var se *StartError
	if !errors.As(err, &se) || se.Code != "data_dir_required" {
		t.Fatalf("err = %v, want data_dir_required", err)
	}
}

func TestStartRejectsInvalidPort(t *testing.T) {
	for _, port := range []int{-1, 70000} {
		cfg := testConfig(t)
		cfg.Port = port
		_, err := Start(context.Background(), cfg)
		var se *StartError
		if !errors.As(err, &se) || se.Code != "invalid_port" {
			t.Fatalf("port %d err = %v, want invalid_port", port, err)
		}
	}
}

func TestStartRejectsInvalidPaths(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*RuntimeConfig)
	}{
		{"empty-cache", func(c *RuntimeConfig) { c.CacheDir = "" }},
		{"empty-temp", func(c *RuntimeConfig) { c.TempDir = "" }},
		{"relative-cache", func(c *RuntimeConfig) { c.CacheDir = "relative-cache" }},
		{"duplicate-roots", func(c *RuntimeConfig) { c.CacheDir = c.TempDir }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(t)
			tc.mutate(&cfg)
			_, err := Start(context.Background(), cfg)
			var se *StartError
			if !errors.As(err, &se) || se.Code != "invalid_paths" {
				t.Fatalf("err = %v, want invalid_paths", err)
			}
		})
	}
}

func TestStartFailsWhenPortIsInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	cfg := testConfig(t)
	cfg.Port = port
	_, err = Start(context.Background(), cfg)
	var se *StartError
	if !errors.As(err, &se) || se.Code != "listen_failed" {
		t.Fatalf("err = %v, want listen_failed for occupied port %d", err, port)
	}
}

func TestHealthzRejectsNonGET(t *testing.T) {
	rt := startForTest(t, testConfig(t))
	resp, err := http.Post("http://"+rt.Status().Addr+"/healthz", "text/plain", nil)
	if err != nil {
		t.Fatalf("POST /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}

func TestCallerKeysEndpoint(t *testing.T) {
	cfg := testConfig(t)
	rt := startForTest(t, cfg)
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "http://"+rt.Status().Addr+"/api/caller-keys", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+rt.Status().AdminSession)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/caller-keys: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"keys"`) {
		t.Fatalf("body missing keys: %s", body)
	}
}
