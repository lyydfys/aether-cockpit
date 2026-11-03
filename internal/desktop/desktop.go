// Package desktop 是 Wails 桌面端入口的内核部分。
//
// 为什么单独抽成包：Wails CLI 默认在项目根目录寻找 main 包并从那里生成绑定。
// 把桌面端逻辑放在 internal/desktop 后，根目录的 main.go 与 cmd/desktop/main.go
// 都可以是一个几行的壳，既能用 `go build ./cmd/desktop`，也能用 `wails build`。
package desktop

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"aether-cockpit/internal/core/apppaths"
	"aether-cockpit/internal/core/appstart"
	"aether-cockpit/internal/core/gateway"
	"aether-cockpit/internal/core/logging"
	"aether-cockpit/internal/core/scheduler"
	"aether-cockpit/internal/core/transport"
	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/web"

	_ "aether-cockpit/internal/providers/antigravity"
	_ "aether-cockpit/internal/providers/antigravityide"
	_ "aether-cockpit/internal/providers/claude"
	_ "aether-cockpit/internal/providers/codebuddy"
	_ "aether-cockpit/internal/providers/cursor"
	_ "aether-cockpit/internal/providers/devin"
	_ "aether-cockpit/internal/providers/geminicompat"
	_ "aether-cockpit/internal/providers/githubcopilot"
	_ "aether-cockpit/internal/providers/kiro"
	_ "aether-cockpit/internal/providers/minimax"
	_ "aether-cockpit/internal/providers/openaicompat"
	_ "aether-cockpit/internal/providers/qoder"
	_ "aether-cockpit/internal/providers/reverse"
	_ "aether-cockpit/internal/providers/trae"
	_ "aether-cockpit/internal/providers/typesafe"
	_ "aether-cockpit/internal/providers/vertex"
	_ "aether-cockpit/internal/providers/windsurf"
	_ "aether-cockpit/internal/providers/workbuddy"
	_ "aether-cockpit/internal/providers/zcode"
	_ "aether-cockpit/internal/providers/zed"
)

// App 是 Wails 的应用对象，同时也是托盘菜单的操作入口。
type App struct {
	ctx context.Context
	gw  *gateway.Gateway
	srv *http.Server
	ln  net.Listener

	// 供前端读取的运行时信息（端口热切换会改 addr，读写加锁）
	addrMu sync.Mutex
	addr   string
	cfg    gateway.Config
	sched  *scheduler.Scheduler
}

// currentAddr 返回当前监听地址（端口可热切换）。
func (a *App) currentAddr() string {
	a.addrMu.Lock()
	defer a.addrMu.Unlock()
	return a.addr
}

// switchPort 在新端口先监听、再关旧监听；返回实际地址。
func (a *App) switchPort(addr string) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("监听 %s 失败: %w", addr, err)
	}
	actual := ln.Addr().String()
	srv := &http.Server{Handler: a.gw, ReadHeaderTimeout: 20 * time.Second, IdleTimeout: 120 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("[WARN] 网关新监听退出: %v", err)
		}
	}()
	a.addrMu.Lock()
	old := a.srv
	a.srv, a.ln, a.addr = srv, ln, actual
	a.addrMu.Unlock()
	if old != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			_ = old.Shutdown(ctx)
		}()
	}
	log.Printf("[INFO] 网关端口已切换: %s", actual)
	return actual, nil
}

// Run 启动桌面端，阻塞直到用户退出。
func Run() {
	// 必须在 logging/apppaths 读取路径之前完成：macOS .app 的工作目录是
	// /，不能沿用 Windows 便携版的 ./data。
	preparePlatform()
	if cleanup, err := logging.Setup(logging.DefaultPath()); err == nil {
		defer cleanup()
	} else {
		log.Printf("[WARN] 日志文件不可写，仅控制台输出: %v", err)
	}
	// 安装目录变化后修正自启指向（仅在已开启时生效）。
	if err := appstart.EnsureCurrent(); err != nil {
		log.Printf("[WARN] 修正开机自启路径失败: %v", err)
	}
	if !acquireSingleInstance(`Local\AetherCockpit`) {
		log.Printf("[INFO] 已有 Aether Cockpit 实例在运行，本次启动退出")
		showAlreadyRunning("Aether Cockpit")
		return
	}
	cfg := gateway.Config{
		Addr:             "127.0.0.1:7860",
		DesktopApp:       true,
		SnapshotPath:     apppaths.Data("state.json"),
		SQLitePath:       apppaths.Data("state.db"),
		AccountStorePath: apppaths.Data("accounts.json"),
		SpecDir:          "configs/specs",
		StateTTL:         2 * time.Hour,
		MaxRecords:       2000,
		FirstByteTimeout: 60 * time.Second,
		MaxAttempts:      5,
	}

	app := &App{cfg: cfg}

	// ① 启动网关（与 headless 完全同一套代码）
	gw := gateway.New(cfg)
	gw.RestoreLAN()
	gw.StartModelCatalog()
	app.gw = gw

	schedOpts := scheduler.DefaultOptions()
	schedOpts.StatePath = apppaths.Data("checkin.json")
	sched := scheduler.New(schedOpts, gw.Pool(), log.Printf)
	gw.SetScheduler(sched)
	sched.Start()
	app.sched = sched

	// ② 监听端口。端口被占用时自动换一个，避免第二个实例起不来。
	ln, actualAddr := listenWithFallback(gw.ListenAddr())
	app.ln = ln
	app.addr = actualAddr
	// 端口冲突时 cfg.Addr 与实际监听不同，接入片段必须用实际地址。
	gw.SetPublicAddr(actualAddr)

	app.srv = &http.Server{
		Handler:           gw,
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	gw.SetPortSwitcher(app.switchPort)
	go func() {
		if err := app.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("[WARN] 网关服务退出: %v", err)
		}
	}()

	// ③ spec 远程热更新 + 状态/账号自动落盘 + 凭证保活
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()
	stopSpec := gw.StartSpecAutoUpdate(rootCtx, 10*time.Minute)
	defer stopSpec()
	stopSave := gw.StartAutoSave(20 * time.Second)
	defer stopSave()
	stopAccSave := gw.Pool().StartAutoSave(10 * time.Second)
	stopUsage := gw.StartUsageAutoSave(15 * time.Second)
	defer stopAccSave()
	stopToken := gw.StartTokenRefresh(rootCtx, 10*time.Minute, 30*time.Minute)
	defer stopToken()
	// 渠道健康巡检：账号全灭自动下线，额度恢复后再自动上线。
	stopHealth := gw.StartProviderHealthWatch(rootCtx, time.Minute)
	defer stopHealth()

	// WebDAV 备份自动推送：未配置或 auto_push_minutes<=0 时是空操作，可无条件启动。
	stopWebDAVPush := gw.StartWebDAVAutoPush(rootCtx, gw.WebDAVAutoPushInterval())
	defer stopWebDAVPush()

	if st := transport.DetectTUN(); st.Detected {
		log.Printf("[WARN] %s", st.Warning)
	}

	// ④ 托管前端：静态资源 + 网关 API 走同一个 handler
	host := buildAssetHandler(app)

	err := wails.Run(&options.App{
		Title: "Aether Cockpit",
		// 125% 缩放时 1320×860 逻辑像素会超过 1080p 工作区高度，默认窗口顶部会被挤出屏幕。
		Width:     1280,
		Height:    800,
		MinWidth:  980,
		MinHeight: 640,
		// 无边框 + 自绘标题栏：Windows 上是 44px 一体式标题栏（拖拽区 + 窗口控制），
		// 原生菜单由标题栏「⋯」与托盘菜单接管，避免标题栏下面再压一条菜单条。
		Frameless: true,
		Menu:      platformMenu(app),
		AssetServer: &assetserver.Options{
			Handler: host,
		},
		OnBeforeClose: func(ctx context.Context) bool {
			if runtime.GOOS == "darwin" {
				// macOS 关窗只隐藏，网关和菜单栏继续运行；真正退出走 ⌘Q/托盘。
				wailsruntime.WindowHide(ctx)
				return true
			}
			return false
		},
		OnStartup: func(ctx context.Context) {
			app.ctx = ctx
			// 无边框窗口的首帧尺寸包含不可见边框，Wails 的初始 WindowCenter 会把它算进屏幕位置，
			// 表现为窗口顶部被推到屏幕外。等窗口稳定后再居中一次。
			go func() {
				time.Sleep(500 * time.Millisecond)
				wailsruntime.WindowCenter(ctx)
			}()
			// 托盘必须等 Wails 的事件循环起来之后再起，
			// 否则点击菜单项时 runtime.* 调用会因为还没有 ctx 而空转。
			go app.runTray()
			wailsruntime.LogInfo(ctx, "Aether Cockpit 已启动，网关监听 "+app.currentAddr())
		},
		OnShutdown: func(ctx context.Context) {
			app.stopTray()
			sched.Stop()
			stopUsage()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = app.srv.Shutdown(shutdownCtx)
			_ = gw.Store().Close()
		},
	})
	if err != nil {
		log.Fatalf("[FATAL] 桌面端启动失败: %v", err)
	}
}

// buildAssetHandler 组合"静态资源"与"网关 API"。
//
// 为什么要把两者放一个 handler：桌面端前端与后端同源，
// 因此前端可以直接 fetch('/api/accounts')，不需要处理跨域或动态端口。
func buildAssetHandler(app *App) http.Handler {
	// 静态资源统一走 web.Handler：安全头、目录 404、缓存策略与 headless 版一致。
	static := web.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		// API 与协议端点交给网关
		if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/v1/") {
			app.gw.ServeHTTP(w, r)
			return
		}
		static.ServeHTTP(w, r)
	})
}

// listenWithFallback 监听端口，被占用时往后找可用端口。
//
// 桌面端常见的冲突场景：用户先开了 headless 版，再开桌面版。
// 与其报错让用户自己改配置，不如自动错开并在托盘提示。
func listenWithFallback(preferred string) (net.Listener, string) {
	host, portStr, err := net.SplitHostPort(preferred)
	if err != nil {
		host, portStr = "127.0.0.1", "7860"
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	if port <= 0 {
		port = 7860
	}

	for i := 0; i < 20; i++ {
		addr := fmt.Sprintf("%s:%d", host, port+i)
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, addr
		}
	}
	// 全部占用则交给系统分配
	ln, _ := net.Listen("tcp", host+":0")
	return ln, ln.Addr().String()
}

// 保证 provider 包被引用（渠道注册依赖它的 init，这里显式导一次便于阅读）。
var _ = provider.IDs
