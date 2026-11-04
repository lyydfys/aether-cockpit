// Package mobile owns the process-wide mobile gateway lifecycle.
//
// This slice serves only the anonymous /healthz endpoint. Gateway wiring,
// persistence and Flush are added by later slices; Flush must not exist as a
// no-op success before there is state to flush.
package mobile

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/apppaths"
	"aether-cockpit/internal/core/gateway"
	"aether-cockpit/internal/core/logging"
	"aether-cockpit/internal/core/scheduler"
)

const (
	StateStarting = "starting"
	StateRunning  = "running"
	StateStopping = "stopping"
	StateStopped  = "stopped"
	StateFailed   = "failed"
)

// RuntimeConfig is the single lifecycle input for the mobile runtime. DataDir
// is <noBackupFilesDir>/aether, CacheDir is <filesDir>/aether/cache and TempDir
// is <cacheDir>/aether/tmp; all are absolute and host-provided. The runtime
// never derives them from the process environment or working directory.
type RuntimeConfig struct {
	DataDir    string `json:"dataDir"`
	CacheDir   string `json:"cacheDir"`
	TempDir    string `json:"tempDir"`
	ListenHost string `json:"listenHost"`
	Port       int    `json:"port"`
	// AdminSessionKey 可选：留空由运行时生成每次启动随机的 256-bit key。
	// 非空仅用于宿主或测试注入；生产应留空，让 key 不经过设置文件。
	AdminSessionKey   string `json:"adminSessionKey,omitempty"`
	CallerKeyRequired bool   `json:"callerKeyRequired,omitempty"`
	Platform          string `json:"platform"`
	// AccountKey 是 Android Keystore 解封后的 32 字节账号密钥（base64）。
	// 只存在于启动配置与进程内存，不落盘、不写日志。
	AccountKey string `json:"accountKey,omitempty"`
}

func decodeAccountKey(value string) ([]byte, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(key) != 32 {
		return nil, errors.New("account key must be base64-encoded 32 bytes")
	}
	return key, nil
}

// RuntimeStatus is the JSON contract shared with the host ABI.
type RuntimeStatus struct {
	State     string `json:"state"`
	Addr      string `json:"addr,omitempty"`
	StartedAt int64  `json:"startedAt,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
	// AdminSession 与 CallerKey 只经 ABI 返回给原生宿主，由宿主保存在
	// 私有内存；不写设置文件、不写日志、不放 URL。两把 key 互不通用。
	AdminSession string `json:"adminSession,omitempty"`
	CallerKey    string `json:"callerKey,omitempty"`
}

// StartError carries a stable code so the ABI never exposes a raw error.
type StartError struct {
	Code string
	Err  error
}

func (e *StartError) Error() string { return e.Code + ": " + e.Err.Error() }
func (e *StartError) Unwrap() error { return e.Err }

// Runtime owns the loopback listener and HTTP server for one start.
type Runtime struct {
	mu        sync.Mutex
	state     string
	addr      string
	startedAt int64
	errorCode string
	server    *http.Server
	done      chan struct{}
	stopDone  chan struct{}
	stopErr   error
	paths     apppaths.Paths
	closeLog  func()
	gw        *gateway.Gateway
	sched     *scheduler.Scheduler
	stopUsage func()
	adminKey  string
	callerKey string
}

// resolveEphemeralKey 优先使用宿主注入值（独立宿主/测试），否则生成
// 每次启动随机的 256-bit key。key 只存在于内存与 ABI 返回值。
func resolveEphemeralKey(provided string) (string, error) {
	if v := strings.TrimSpace(provided); v != "" {
		return v, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Start validates the host-owned path, binds 127.0.0.1 only and returns only
// after the listener is accepting. Port 0 asks the system for a free port.
// iOS is a separate milestone and is rejected until its host exists.
func Start(ctx context.Context, cfg RuntimeConfig) (*Runtime, error) {
	if cfg.DataDir == "" {
		return nil, &StartError{Code: "data_dir_required", Err: errors.New("data dir is required")}
	}
	host := cfg.ListenHost
	if host == "" {
		host = "127.0.0.1"
	}
	if host != "127.0.0.1" {
		return nil, &StartError{Code: "listen_host_must_be_loopback", Err: fmt.Errorf("host %q is not loopback", host)}
	}
	if cfg.Port < 0 || cfg.Port > 65535 {
		return nil, &StartError{Code: "invalid_port", Err: fmt.Errorf("port %d is invalid", cfg.Port)}
	}
	if cfg.Platform != "android" {
		return nil, &StartError{Code: "unsupported_platform", Err: fmt.Errorf("platform %q is not supported", cfg.Platform)}
	}
	paths, err := apppaths.NewPaths(cfg.DataDir, cfg.CacheDir, cfg.TempDir)
	if err != nil {
		return nil, &StartError{Code: "invalid_paths", Err: err}
	}
	for _, dir := range []string{
		paths.DataDir, paths.UsageDir, paths.CacheDir, paths.TempDir,
		paths.ModelsDir, paths.SpecDir,
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, &StartError{Code: "data_dir_failed", Err: err}
		}
	}

	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(cfg.Port)))
	if err != nil {
		return nil, &StartError{Code: "listen_failed", Err: err}
	}

	closeLog, logErr := logging.Setup(paths.LogPath)
	if logErr != nil {
		_ = ln.Close()
		return nil, &StartError{Code: "log_setup_failed", Err: logErr}
	}

	// Gateway 的循环由 Runtime.Stop 经 Close 收口；不绑定 Start 的 ctx，
	// 避免一次带超时的启动参数在返回后误停后台服务。
	adminKey, err := resolveEphemeralKey(cfg.AdminSessionKey)
	if err != nil {
		_ = ln.Close()
		if closeLog != nil {
			closeLog()
		}
		return nil, &StartError{Code: "admin_session_failed", Err: err}
	}
	callerKey, err := resolveEphemeralKey("")
	if err != nil {
		_ = ln.Close()
		if closeLog != nil {
			closeLog()
		}
		return nil, &StartError{Code: "caller_key_failed", Err: err}
	}
	accountKey, err := decodeAccountKey(cfg.AccountKey)
	if err != nil {
		_ = ln.Close()
		if closeLog != nil {
			closeLog()
		}
		return nil, &StartError{Code: "account_key_invalid", Err: err}
	}

	gw := gateway.New(gateway.Config{
		Platform:         &gateway.PlatformCapabilities{},
		CredentialKey:    accountKey,
		LifecycleContext: context.Background(),
		AdminSessionKey:  adminKey,
		RequireCallerKey: true,
		CallerKey:        callerKey,
		// Addr 必须是**真实监听地址**（回环 + 系统分配的端口）。
		// 不传的话 g.cfg.Addr 是空串，`isPublicListen("")` 会把空串当成"监听所有网卡"，
		// 于是自检里出现「监听所有网卡但未设置 API Key」的假错误，/api/settings 的
		// addr 与 /api/runtime 的 publicAddr 也会是空的。
		Addr:              ln.Addr().String(),
		SettingsPath:      paths.SettingsPath,
		AccountStorePath:  paths.AccountsPath,
		SnapshotPath:      paths.SnapshotPath,
		SQLitePath:        paths.StateDB,
		MiddlewarePath:    paths.MiddlewarePath,
		CustomClientsPath: paths.ClientsPath,
		LibraryPath:       paths.LibraryPath,
		UsagePath:         paths.UsagePath,
		ModelsDir:         paths.ModelsDir,
		SpecDir:           paths.SpecDir,
	})
	if err := gw.AccountStoreError(); err != nil {
		_ = ln.Close()
		if closeLog != nil {
			closeLog()
		}
		return nil, &StartError{Code: "account_store_failed", Err: err}
	}

	// 签到调度器：桌面端在 main.go / desktop.go 里接线，移动端以前漏了这一步，
	// 导致 `GET /api/scheduler` 恒 `enabled:false`、`POST /api/scheduler/run` 恒 503
	// ——也就是"签到"在手机端等于不存在。
	//
	// 与桌面端同一套 wiring（同一个构造函数、同一份 Options），差别只有一点：
	// 状态文件必须落在**应用沙箱**内（paths.DataDir），不能跟着 apppaths.Base() 走，
	// 否则 Android 上会写到不可写的相对路径。
	schedOpts := scheduler.DefaultOptions()
	schedOpts.StatePath = filepath.Join(paths.DataDir, "checkin.json")
	sched := scheduler.New(schedOpts, gw.Pool(), nil)
	gw.SetScheduler(sched)
	sched.Start()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 管理面（/api、/mcp、/metrics、/readyz、/version）由 Gateway
		// 的移动会话鉴权把关：回环不等于可信。
		gw.ServeHTTP(w, r)
	})

	rt := &Runtime{
		state:     StateRunning,
		addr:      ln.Addr().String(),
		startedAt: time.Now().Unix(),
		server:    &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second},
		done:      make(chan struct{}),
		stopDone:  make(chan struct{}),
		paths:     paths,
		closeLog:  closeLog,
		gw:        gw,
		sched:     sched,
		stopUsage: gw.StartUsageAutoSave(15 * time.Second),
		adminKey:  adminKey,
		callerKey: callerKey,
	}
	go func() {
		defer close(rt.done)
		_ = rt.server.Serve(ln)
	}()
	log.Printf("mobile runtime started")
	return rt, nil
}

func (r *Runtime) Status() RuntimeStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RuntimeStatus{
		State:        r.state,
		Addr:         r.addr,
		StartedAt:    r.startedAt,
		ErrorCode:    r.errorCode,
		AdminSession: r.adminKey,
		CallerKey:    r.callerKey,
	}
}

// Paths returns the immutable host-provided path set owned by this runtime.
func (r *Runtime) Paths() apppaths.Paths {
	return r.paths
}

// Stop shuts the listener down and waits for Serve to return, so a successful
// Stop means this runtime no longer owns the port. It is idempotent and safe
// while another Stop is already waiting.
func (r *Runtime) Stop(ctx context.Context) error {
	r.mu.Lock()
	switch r.state {
	case StateStopped:
		r.mu.Unlock()
		return nil
	case StateStopping:
		stopDone := r.stopDone
		r.mu.Unlock()
		<-stopDone
		r.mu.Lock()
		err := r.stopErr
		r.mu.Unlock()
		return err
	}
	r.state = StateStopping
	server := r.server
	done := r.done
	stopDone := r.stopDone
	gw := r.gw
	sched := r.sched
	stopUsage := r.stopUsage
	r.stopUsage = nil
	r.mu.Unlock()

	// 先停调度器再关服务：否则它可能在监听器关闭后仍在跑签到请求，
	// 且 Stop 之后还会往 checkin.json 里写状态。
	if sched != nil {
		sched.Stop()
	}
	err := server.Shutdown(ctx)
	if err != nil {
		_ = server.Close()
	}
	<-done
	// 停掉周期落盘并做最后一次 flush，再关网关：进程被杀前退出时
	// 用量聚合与账本行保持一致，重启后用量页不会回到空态。
	if stopUsage != nil {
		stopUsage()
	}
	if gw != nil {
		if closeErr := gw.Close(ctx); err == nil {
			err = closeErr
		}
	}

	r.mu.Lock()
	r.state = StateStopped
	r.addr = ""
	r.startedAt = 0
	r.adminKey = ""
	r.callerKey = ""
	r.stopErr = err
	closeLog := r.closeLog
	r.closeLog = nil
	close(stopDone)
	r.mu.Unlock()

	log.Printf("mobile runtime stopped")
	if closeLog != nil {
		closeLog()
	}
	return err
}
