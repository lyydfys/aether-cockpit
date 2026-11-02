package mobile

import (
	"context"
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
	if _, err := os.Stat(paths.StateDB); !os.IsNotExist(err) {
		t.Fatalf("state.db must not be created before the state layer: %v", err)
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
