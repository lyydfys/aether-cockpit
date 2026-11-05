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

// 监听 host 白名单：回环（默认，安全）与 0.0.0.0（局域网共享）。
// 两者之外一律拒绝——手机端不做"绑某一块具体网卡"，也不做公网绑定。
const (
	LoopbackHost = "127.0.0.1"
	LanHost      = "0.0.0.0"
)

func listenPort(addr string) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return n
}

func splitLanHints(hint string) []string {
	return strings.FieldsFunc(hint, func(r rune) bool {
		return r == ',' || r == ';' || r == '|' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
}

// lanShareURLs 组装"别的设备该填什么地址"。
//
// 两条来源合并：
//  1. 宿主注入的 hint（Android 上这是唯一可靠来源：Go 进程在 SELinux
//     untrusted_app 域里拿不到 NETLINK_ROUTE，`net.Interfaces()` 会失败）；
//  2. 网关自己的 `LANURLsFor` 枚举（桌面或 hint 缺失时的兜底）。
//
// 只接受合法 IPv4（非回环 / 非 0.0.0.0 / 非 169.254 链路本地）；端口取自实际
// 监听地址，绝不把 `0.0.0.0` 拼进 URL。
func lanShareURLs(listenAddr, hint string) []string {
	port := listenPort(listenAddr)
	if port == 0 {
		return nil
	}
	out := make([]string, 0, 6)
	seen := map[string]bool{}
	addIP := func(ip net.IP) {
		v4 := ip.To4()
		if v4 == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
			return
		}
		host := v4.String()
		if seen[host] {
			return
		}
		seen[host] = true
		out = append(out, "http://"+net.JoinHostPort(host, strconv.Itoa(port)))
	}
	for _, raw := range splitLanHints(hint) {
		if ip := net.ParseIP(strings.TrimSpace(raw)); ip != nil {
			addIP(ip)
		}
	}
	for _, u := range gateway.LANURLsFor(listenAddr) {
		hostPort := strings.TrimPrefix(strings.TrimPrefix(u, "http://"), "https://")
		if host, _, err := net.SplitHostPort(hostPort); err == nil {
			if ip := net.ParseIP(host); ip != nil {
				addIP(ip)
			}
		}
	}
	return out
}

// RuntimeConfig is the single lifecycle input for the mobile runtime. DataDir
// is <noBackupFilesDir>/aether, CacheDir is <filesDir>/aether/cache and TempDir
// is <cacheDir>/aether/tmp; all are absolute and host-provided. The runtime
// never derives them from the process environment or working directory.
type RuntimeConfig struct {
	DataDir string `json:"dataDir"`
	// CacheDir/TempDir 同前。
	CacheDir string `json:"cacheDir"`
	TempDir  string `json:"tempDir"`
	// ListenHost 决定主监听绑哪：留空或 "127.0.0.1" = 只绑回环（默认，安全）；
	// "0.0.0.0" = 局域网共享——对话接口(/v1)与**管理面(/api)**同时对局域网开放，
	// 鉴权不变（管理面仍强制 `Authorization: Bearer <AdminSession>`）。
	// 其它 host 一律拒绝：本运行时只服务本机/局域网，不做公网绑定。
	ListenHost string `json:"listenHost"`
	Port       int    `json:"port"`
	// LanAddressHint 是宿主探测到的本机局域网 IPv4（逗号分隔），用于在
	// `net.Interfaces()` 不可用的平台（Android 的 SELinux 不给 NETLINK_ROUTE）
	// 上仍能展示 `http://192.168.x.x:<port>`。可选；非法值会被丢弃。
	LanAddressHint string `json:"lanAddressHint,omitempty"`
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
	// LanEnabled 表示主监听绑在 0.0.0.0（同网络设备可达）：此时 /v1 与 /api
	// **都**对该网络开放，管理面鉴权照旧（没有会话凭据仍然 401）。
	// LanURLs 是枚举本机非回环 IPv4 拼出的访问地址（`http://ip:port`），
	// 拿不到时为空，界面显示「未获取到局域网地址」。
	LanEnabled bool     `json:"lanEnabled,omitempty"`
	LanURLs    []string `json:"lanUrls,omitempty"`
}

// StartError carries a stable code so the ABI never exposes a raw error.
type StartError struct {
	Code string
	Err  error
}

func (e *StartError) Error() string { return e.Code + ": " + e.Err.Error() }
func (e *StartError) Unwrap() error { return e.Err }

// Runtime owns the listener and HTTP server for one start. The listener is
// 127.0.0.1 unless the host asked for LAN sharing (0.0.0.0), in which case the
// admin plane is reachable from the LAN too — with the same session auth.
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
	// lanShared 表示本次启动绑的是 0.0.0.0；lanURLs/lanHint 是启动时算出的
	// 可访问地址与宿主提示（Status 每次重算，Wi-Fi 换了地址也能跟上）。
	lanShared bool
	lanURLs   []string
	lanHint   string
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

// Start validates the host-owned path, binds 127.0.0.1 by default (or 0.0.0.0
// when the host explicitly asks for LAN sharing) and returns only after the
// listener is accepting. Port 0 asks the system for a free port.
// iOS is a separate milestone and is rejected until its host exists.
func Start(ctx context.Context, cfg RuntimeConfig) (*Runtime, error) {
	if cfg.DataDir == "" {
		return nil, &StartError{Code: "data_dir_required", Err: errors.New("data dir is required")}
	}
	host := strings.TrimSpace(cfg.ListenHost)
	if host == "" {
		host = LoopbackHost
	}
	// 只允许两种：回环（默认）与 0.0.0.0（局域网共享）。其它地址一律拒绝——
	// 绑定单个具体网卡会让"换 Wi-Fi 后地址变了"这类问题无处排查，而公网绑定
	// 从来不是本运行时的能力。
	lanShared := false
	switch host {
	case LoopbackHost:
	case LanHost:
		lanShared = true
	default:
		return nil, &StartError{
			Code: "listen_host_not_allowed",
			Err:  fmt.Errorf("host %q is not %s or %s", host, LoopbackHost, LanHost),
		}
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

	// 局域网共享时**只监听 IPv4**（tcp4）：Go 的 "tcp" + 0.0.0.0 会拿到双栈
	// `[::]` 监听，等于把管理面同时挂到本机的 IPv6 地址上；不少移动网络是
	// 公网可路由 IPv6，那会把"局域网共享"变成"公网共享"。tcp4 让暴露面
	// 严格等于用户看到的那个 IPv4 地址。
	network := "tcp"
	if lanShared {
		network = "tcp4"
	}
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, network, net.JoinHostPort(host, strconv.Itoa(cfg.Port)))
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

	// 局域网共享（默认关）：host == 0.0.0.0 时主监听已绑所有 IPv4 网卡，
	// /v1 与 /api 对同网络设备同时开放；管理面鉴权**不因绑定而放宽**
	// （`mobileAdminPath` 仍然要求 `Authorization: Bearer <AdminSession>`）。
	//
	// 地址来源有两条：
	//  1. 宿主注入的 lanAddressHint（Android 关键路径：Go 进程在 SELinux
	//     untrusted_app 域里拿不到 NETLINK_ROUTE，`net.Interfaces()` 会失败，
	//     于是 0.0.0.0 绑定成功却枚举不出 wlan0 的 192.168.x.x）；
	//  2. 网关自己的 LANURLsFor 枚举（桌面/有权限平台，或 hint 缺失时的兜底）。
	var lanURLs []string
	lanHint := strings.TrimSpace(cfg.LanAddressHint)
	if lanShared {
		lanURLs = lanShareURLs(ln.Addr().String(), lanHint)
		if len(lanURLs) == 0 {
			log.Printf("[WARN] 局域网共享已绑定 %s，但没算出可展示地址: hint=%q（设备可能未联网）", ln.Addr().String(), lanHint)
		} else {
			log.Printf("局域网共享地址: hint=%q -> %v", lanHint, lanURLs)
		}
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
		lanShared: lanShared,
		lanURLs:   lanURLs,
		lanHint:   lanHint,
	}
	go func() {
		defer close(rt.done)
		_ = rt.server.Serve(ln)
	}()
	// 启动日志写明真实监听地址；开启局域网共享时必须点明「管理面也对局域网开放」。
	if lanShared {
		log.Printf(
			"mobile runtime started: listen=%s (0.0.0.0) — 局域网共享已开启："+
				"管理面(/api)与对话接口(/v1)对同网络设备开放，管理面仍需 %s；lanURLs=%v",
			ln.Addr().String(), logFieldAuthorizationHeader, lanURLs,
		)
	} else {
		log.Printf("mobile runtime started: listen=%s — 仅本机回环（局域网共享关闭）", ln.Addr().String())
	}
	return rt, nil
}

// logFieldAuthorizationHeader 只用于日志里指名要求哪种凭据，不含任何 key 值。
const logFieldAuthorizationHeader = "Authorization: Bearer <AdminSession>"

func (r *Runtime) Status() RuntimeStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := RuntimeStatus{
		State:        r.state,
		Addr:         r.addr,
		StartedAt:    r.startedAt,
		ErrorCode:    r.errorCode,
		AdminSession: r.adminKey,
		CallerKey:    r.callerKey,
		LanEnabled:   r.lanShared && r.state == StateRunning,
	}
	// 每次现算：Wi-Fi 换了网段/重连后，列表不会停留在旧地址上。
	if r.lanShared && r.state == StateRunning {
		st.LanURLs = lanShareURLs(r.addr, r.lanHint)
	}
	return st
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
	r.lanShared = false
	r.lanURLs = nil
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
