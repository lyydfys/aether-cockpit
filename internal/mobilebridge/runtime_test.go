package mobilebridge

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		DataDir:    filepath.Join(t.TempDir(), "aether"),
		ListenHost: "127.0.0.1",
		Port:       0,
		Platform:   "android",
	}
}

func startForTest(t *testing.T, cfg Config) *Runtime {
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
	if info, err := os.Stat(cfg.DataDir); err != nil || !info.IsDir() {
		t.Fatalf("data dir %s: %v", cfg.DataDir, err)
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
	if _, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
		t.Fatalf("old listener %s still accepts", addr)
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

func TestStartRejectsNonLoopback(t *testing.T) {
	cfg := testConfig(t)
	cfg.ListenHost = "0.0.0.0"
	_, err := Start(context.Background(), cfg)
	var se *StartError
	if !errors.As(err, &se) || se.Code != "listen_host_must_be_loopback" {
		t.Fatalf("err = %v, want listen_host_must_be_loopback", err)
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
