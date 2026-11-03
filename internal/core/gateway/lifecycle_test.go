package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aether-cockpit/internal/core/sessions"
)

func lifecycleTestConfig(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	return Config{
		Addr:             "127.0.0.1:0",
		SettingsPath:     filepath.Join(dir, "settings.json"),
		UsagePath:        "-",
		ModelsDir:        filepath.Join(dir, "models"),
		AccountStorePath: filepath.Join(dir, "accounts.json"),
		// 基线测试不触碰真实 wsl.exe / 会话目录；需要观测的用例再覆盖。
		wslWarm:      func() {},
		sessionsWarm: func() {},
	}
}

func writeLifecycleSettings(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("写设置文件: %v", err)
	}
}

func closeTestGateway(t *testing.T, g *Gateway) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := g.Close(ctx); err != nil {
		t.Errorf("Close: %v", err)
	}
	sessions.SetWSLEnabled(true)
}

func TestGatewayCloseStopsBackgroundLoops(t *testing.T) {
	g := New(lifecycleTestConfig(t))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := g.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-g.ctx.Done():
	default:
		t.Fatal("Close 后生命周期 context 仍未取消")
	}
	if err := g.Close(ctx); err != nil {
		t.Fatalf("重复 Close 应安全: %v", err)
	}
}

func TestGatewayCloseWaitsForTrackedWork(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	cfg := lifecycleTestConfig(t)
	cfg.wslWarm = func() {
		close(started)
		<-release
	}
	g := New(cfg)
	<-started

	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := g.Close(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("未退出时应返回 ctx 超时，got %v", err)
	}

	close(release)
	long, cancelLong := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelLong()
	if err := g.Close(long); err != nil {
		t.Fatalf("后台工作退出后 Close 应成功: %v", err)
	}
	sessions.SetWSLEnabled(true)
}

func TestGoBackgroundAfterCloseIsIgnored(t *testing.T) {
	g := New(lifecycleTestConfig(t))
	closeTestGateway(t, g)

	var ran atomic.Bool
	g.goBackground(func() { ran.Store(true) })
	time.Sleep(20 * time.Millisecond)
	if ran.Load() {
		t.Fatal("Close 后不应再启动新的后台工作")
	}
}

func TestPlatformCapabilitiesSuppressDesktopSideEffects(t *testing.T) {
	t.Run("wsl-warmup", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			platform *PlatformCapabilities
			want     bool
		}{
			{"desktop-default", nil, true},
			{"mobile", &PlatformCapabilities{}, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var calls atomic.Int32
				cfg := lifecycleTestConfig(t)
				cfg.Platform = tc.platform
				cfg.wslWarm = func() { calls.Add(1) }
				g := New(cfg)
				t.Cleanup(func() { closeTestGateway(t, g) })

				deadline := time.Now().Add(500 * time.Millisecond)
				for calls.Load() == 0 && time.Now().Before(deadline) {
					time.Sleep(5 * time.Millisecond)
				}
				if got := calls.Load() > 0; got != tc.want {
					t.Fatalf("WSL 预热调用=%v want %v", got, tc.want)
				}
			})
		}
	})

	t.Run("local-session-warmup", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			platform *PlatformCapabilities
			want     bool
		}{
			{"desktop-default", nil, true},
			{"mobile", &PlatformCapabilities{}, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var calls atomic.Int32
				cfg := lifecycleTestConfig(t)
				cfg.Platform = tc.platform
				cfg.sessionsWarm = func() { calls.Add(1) }
				writeLifecycleSettings(t, cfg.SettingsPath, `{"wsl_disabled":true}`)
				g := New(cfg)
				t.Cleanup(func() { closeTestGateway(t, g) })

				deadline := time.Now().Add(500 * time.Millisecond)
				for calls.Load() == 0 && time.Now().Before(deadline) {
					time.Sleep(5 * time.Millisecond)
				}
				if got := calls.Load() > 0; got != tc.want {
					t.Fatalf("会话预热调用=%v want %v", got, tc.want)
				}
			})
		}
	})

	t.Run("keep-awake", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			platform *PlatformCapabilities
			wantOn   bool
		}{
			{"desktop-default", nil, true},
			{"mobile", &PlatformCapabilities{}, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var on, off atomic.Bool
				cfg := lifecycleTestConfig(t)
				cfg.Platform = tc.platform
				cfg.keepAwakeApply = func(v bool) error {
					if v {
						on.Store(true)
					} else {
						off.Store(true)
					}
					return nil
				}
				writeLifecycleSettings(t, cfg.SettingsPath, `{"keep_awake":true}`)
				g := New(cfg)
				if on.Load() != tc.wantOn {
					t.Fatalf("New 后保持唤醒=%v want %v", on.Load(), tc.wantOn)
				}
				closeTestGateway(t, g)
				if off.Load() != tc.wantOn {
					t.Fatalf("Close 后关闭保持唤醒=%v want %v", off.Load(), tc.wantOn)
				}
			})
		}
	})

	t.Run("auto-import", func(t *testing.T) {
		mobile := New(func() Config {
			cfg := lifecycleTestConfig(t)
			cfg.Platform = &PlatformCapabilities{}
			return cfg
		}())
		t.Cleanup(func() { closeTestGateway(t, mobile) })
		if mobile.autoImp.scan != nil {
			t.Fatal("移动平台不应初始化 Windows 自动导入")
		}

		desktop := New(lifecycleTestConfig(t))
		t.Cleanup(func() { closeTestGateway(t, desktop) })
		if desktop.autoImp.scan == nil {
			t.Fatal("桌面默认应初始化自动导入")
		}
	})
}

func TestMobilePlatformHandlersReportUnsupported(t *testing.T) {
	cfg := lifecycleTestConfig(t)
	cfg.Platform = &PlatformCapabilities{}
	g := New(cfg)
	t.Cleanup(func() { closeTestGateway(t, g) })

	cases := []struct {
		name    string
		method  string
		handler http.HandlerFunc
	}{
		{"wsl", http.MethodGet, g.handleWSL},
		{"wsl-refresh", http.MethodPost, g.handleWSLRefresh},
		{"autoimport-status", http.MethodGet, g.handleAutoImportStatus},
		{"autoimport-scan", http.MethodPost, g.handleAutoImportScan},
		{"agents-cli", http.MethodGet, g.handleAgentsCLI},
		{"launcher-desktop-launch", http.MethodPost, g.handleLauncherDesktopLaunch},
		{"importapps-discovery", http.MethodGet, g.handleImportAppsDiscovery},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.handler(rec, httptest.NewRequest(tc.method, "/", nil))
			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("status=%d want 501; body=%s", rec.Code, rec.Body.String())
			}
			var payload map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("响应不是 JSON: %v", err)
			}
			if payload["code"] != "platform_unsupported" {
				t.Fatalf("code=%v want platform_unsupported; body=%s", payload["code"], rec.Body.String())
			}
		})
	}

	t.Run("keep-awake-setting", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"keep_awake":true}`))
		g.handleUpdateSettings(rec, req)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status=%d want 501; body=%s", rec.Code, rec.Body.String())
		}
		g.settingsMu.Lock()
		got := g.settings.KeepAwake
		g.settingsMu.Unlock()
		if got {
			t.Fatal("不支持的平台不应持久化 keep_awake=true")
		}
	})
}
