// Command cockpit 启动 Aether Cockpit 本地算力网关。
//
// 单二进制、零外部依赖：编译产物直接可运行，不需要 Node、不需要数据库、
// 不需要任何运行时。管理界面已内嵌在二进制里。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"aether-cockpit/internal/core/apppaths"
	"aether-cockpit/internal/core/gateway"
	"aether-cockpit/internal/core/logging"
	"aether-cockpit/internal/core/scheduler"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/core/transport"
	"aether-cockpit/internal/provider"

	// 渠道插件通过匿名导入完成自注册。
	// 新增渠道时只需在这里加一行，内核与前端代码零改动。
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
	_ "aether-cockpit/internal/providers/trae"
	_ "aether-cockpit/internal/providers/typesafe"
	_ "aether-cockpit/internal/providers/vertex"
	_ "aether-cockpit/internal/providers/windsurf"
	_ "aether-cockpit/internal/providers/workbuddy"
	_ "aether-cockpit/internal/providers/zcode"
	_ "aether-cockpit/internal/providers/zed"
	// 逆向渠道骨架：协议参数由 configs/specs/*.json 提供。
	_ "aether-cockpit/internal/providers/reverse"
)

func main() {
	// 子命令先于 flag 解析：`aether-cockpit-cli status` 这类调用不该因为
	// 缺少网关必需参数而报错，它们只是打本机 /api/*（回环免鉴权）。
	if runCLI(os.Args[1:]) {
		return
	}
	registerDevMock()

	var (
		addr             = flag.String("addr", "127.0.0.1:7860", "监听地址")
		configPath       = flag.String("config", "", "配置文件路径（JSON，可选）")
		apiKey           = flag.String("key", "", "API Key（留空且仅监听本机时免鉴权）")
		proxy            = flag.String("proxy", "", "全局代理，如 socks5://127.0.0.1:7890")
		snapshot         = flag.String("snapshot", apppaths.Data("state.json"), "会话快照落盘路径（留空禁用）")
		stateDB          = flag.String("state-db", apppaths.Data("state.db"), "SQLite 会话状态路径（留空退回 JSON 快照）")
		accounts         = flag.String("accounts", apppaths.Data("accounts.json"), "账号落盘路径（留空则重启即丢）")
		checkinState     = flag.String("checkin-state", apppaths.Data("checkin.json"), "签到状态落盘路径")
		specDir          = flag.String("spec-dir", "configs/specs", "渠道参数 spec 目录（支持热更新）")
		specRemote       = flag.String("spec-remote", "", "远程 spec 地址（HTTPS），留空则只用本地")
		tokenRefresh     = flag.Int("token-refresh", 10, "凭证保活检查间隔（分钟），负数禁用")
		tokenRefreshLead = flag.Int("token-refresh-lead", 30, "提前多久刷新即将过期的凭证（分钟）")
		showVersion      = flag.Bool("version", false, "打印版本与构建时间后退出")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("Aether Cockpit %s (build %s)\n", gateway.BuildVersion, gateway.BuildTime)
		return
	}

	if cleanup, err := logging.Setup(logging.DefaultPath()); err == nil {
		defer cleanup()
	} else {
		log.Printf("[WARN] 日志文件不可写，仅控制台输出: %v", err)
	}

	cfg := gateway.Config{
		Addr:                    *addr,
		APIKey:                  *apiKey,
		GlobalProxy:             *proxy,
		SnapshotPath:            *snapshot,
		SQLitePath:              *stateDB,
		AccountStorePath:        *accounts,
		SpecDir:                 *specDir,
		SpecRemoteURL:           *specRemote,
		TokenRefreshMinutes:     *tokenRefresh,
		TokenRefreshLeadMinutes: *tokenRefreshLead,
		StateTTL:                2 * time.Hour,
		MaxRecords:              2000,
		FirstByteTimeout:        60 * time.Second,
		MaxAttempts:             5,
	}

	if *configPath != "" {
		if err := loadConfig(*configPath, &cfg); err != nil {
			log.Fatalf("[FATAL] 读取配置失败: %v", err)
		}
	}

	printBanner(cfg)

	gw := gateway.New(cfg)
	gw.RestoreLAN()
	gw.StartModelCatalog()
	stopSave := gw.StartAutoSave(20 * time.Second)
	defer stopSave()
	stopAccSave := gw.Pool().StartAutoSave(10 * time.Second)
	defer stopAccSave()
	stopUsage := gw.StartUsageAutoSave(15 * time.Second)
	defer stopUsage()

	// spec 远程热更新：上游把版本号/端点/签名盐值一改，这里拉一次就恢复可用，
	// 不需要重新编译与分发。
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	specInterval := 10 * time.Minute
	if cfg.SpecUpdateMinutes > 0 {
		specInterval = time.Duration(cfg.SpecUpdateMinutes) * time.Minute
	}
	stopSpec := gw.StartSpecAutoUpdate(rootCtx, specInterval)
	defer stopSpec()

	// 凭证保活：逆向渠道的 token 往往只有几小时有效期，过期前自动刷新。
	tokenInterval := 10 * time.Minute
	switch {
	case cfg.TokenRefreshMinutes < 0:
		tokenInterval = 0
	case cfg.TokenRefreshMinutes > 0:
		tokenInterval = time.Duration(cfg.TokenRefreshMinutes) * time.Minute
	}
	tokenLead := 30 * time.Minute
	if cfg.TokenRefreshLeadMinutes > 0 {
		tokenLead = time.Duration(cfg.TokenRefreshLeadMinutes) * time.Minute
	}
	stopToken := gw.StartTokenRefresh(rootCtx, tokenInterval, tokenLead)
	defer stopToken()
	// 渠道健康巡检：账号全灭自动下线，额度恢复后再自动上线。
	stopHealth := gw.StartProviderHealthWatch(rootCtx, time.Minute)
	defer stopHealth()
	// 额度提醒巡检：窗口快到期但还剩很多时提醒（花钱买的额度别作废），
	// 以及"快用完了"的告警。5 分钟一轮，落盘去重。
	stopQuotaAlerts := gw.StartQuotaAlertWatch(rootCtx, 5*time.Minute)
	defer stopQuotaAlerts()
	// 渠道连接预热：给跨境/中转渠道定期打一个不花额度的探针，把 TCP+TLS
	// 连接保持在热态——否则用户隔一会儿提问，先白等一次 0.2~1.5s 握手。
	stopChannelWarm := gw.StartChannelWarm(rootCtx, 15*time.Second)
	// 边缘自动切换：默认每 30s 检查一次是否需要重测（每域名自己的间隔在设置里）。
	stopEdgeAuto := gw.StartEdgeAuto(rootCtx, 30*time.Second)
	defer stopEdgeAuto()
	defer stopChannelWarm()

	// WebDAV 备份自动推送：未配置或 auto_push_minutes<=0 时是空操作，可无条件启动。
	stopWebDAVPush := gw.StartWebDAVAutoPush(rootCtx, gw.WebDAVAutoPushInterval())
	defer stopWebDAVPush()

	if st := spec.Default().Status(); st.Count > 0 || st.RemoteURL != "" {
		log.Printf("[INFO] 渠道参数: %d 条已加载，远程更新=%v", st.Count, st.RemoteURL != "")
	}

	// 签到调度：Cron 定时 + 系统唤醒补签 + 手动触发，三者共用同一个串行抖动队列。
	schedOpts := scheduler.DefaultOptions()
	schedOpts.StatePath = *checkinState
	if cfg.QuotaRefreshMinutes > 0 {
		schedOpts.QuotaRefresh = time.Duration(cfg.QuotaRefreshMinutes) * time.Minute
	} else if cfg.QuotaRefreshMinutes < 0 {
		schedOpts.QuotaRefresh = 0 // 显式禁用
	}
	sched := scheduler.New(schedOpts, gw.Pool(), log.Printf)
	gw.SetScheduler(sched)
	sched.Start()
	defer sched.Stop()

	// 网络环境自检：TUN 模式下国内渠道的"强制直连"实际不生效，
	// 这是最容易让人误以为"已经在直连"的坑，必须在启动时就明确告知。
	if st := transport.DetectTUN(); st.Detected {
		log.Printf("[WARN] %s", st.Warning)
	}

	log.Printf("[INFO] 已注册渠道: %v", provider.IDs())
	log.Printf("[INFO] 管理界面: http://%s/", displayAddr(cfg.Addr))
	log.Printf("[INFO] OpenAI 兼容: http://%s/v1   Anthropic: http://%s", displayAddr(cfg.Addr), displayAddr(cfg.Addr))

	// 显式监听：端口可以在运行中切换（settings/port），先起新监听再关旧的。
	ln, err := net.Listen("tcp", gw.ListenAddr())
	if err != nil {
		log.Fatalf("[FATAL] 监听失败: %v", err)
	}
	gw.SetPublicAddr(ln.Addr().String())
	srv := &http.Server{
		Handler:           gw,
		ReadHeaderTimeout: 20 * time.Second,
		// 不设 WriteTimeout：流式响应需要长时间保持。
		IdleTimeout: 120 * time.Second,
	}
	var srvMu sync.Mutex
	gw.SetPortSwitcher(func(addr string) (string, error) {
		newLn, err := net.Listen("tcp", addr)
		if err != nil {
			return "", err
		}
		actual := newLn.Addr().String()
		newSrv := &http.Server{Handler: gw, ReadHeaderTimeout: 20 * time.Second, IdleTimeout: 120 * time.Second}
		go func() {
			if err := newSrv.Serve(newLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("[WARN] 新监听退出: %v", err)
			}
		}()
		srvMu.Lock()
		old := srv
		srv = newSrv
		srvMu.Unlock()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			_ = old.Shutdown(ctx)
		}()
		log.Printf("[INFO] 网关端口已切换: %s", actual)
		return actual, nil
	})

	// 优雅退出：先停止接受新请求，再把会话快照落盘。
	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		log.Fatalf("[FATAL] 监听失败: %v", err)
	case sig := <-sigCh:
		log.Printf("[INFO] 收到信号 %v，正在关闭…", sig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srvMu.Lock()
	cur := srv
	srvMu.Unlock()
	_ = cur.Shutdown(ctx)
	stopSave()
	log.Printf("[INFO] 已退出")
}

// loadConfig 读取 JSON 配置，覆盖命令行默认值。
func loadConfig(path string, cfg *gateway.Config) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// 记事本 / Windows PowerShell 写出的 JSON 常带 UTF-8 BOM；
	// 严格解析器会报 "invalid character '\ufeff'"，而用户看不出区别。
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	// 用一个中间结构承接，避免 Duration 的反序列化歧义。
	var raw struct {
		Addr                    *string                    `json:"addr"`
		APIKey                  *string                    `json:"api_key"`
		GlobalProxy             *string                    `json:"global_proxy"`
		SnapshotPath            *string                    `json:"snapshot_path"`
		SQLitePath              *string                    `json:"sqlite_path"`
		AccountStorePath        *string                    `json:"account_store_path"`
		StateTTLMinutes         *int                       `json:"state_ttl_minutes"`
		MaxRecords              *int                       `json:"max_records"`
		FirstByteTimeoutSeconds *int                       `json:"first_byte_timeout_seconds"`
		MaxAttempts             *int                       `json:"max_attempts"`
		ModelGroups             map[string]json.RawMessage `json:"model_groups"`
		SettingsPath            *string                    `json:"settings_path"`
		UsagePath               *string                    `json:"usage_path"`
		QuotaRefreshMinutes     *int                       `json:"quota_refresh_minutes"`
		TokenRefreshMinutes     *int                       `json:"token_refresh_minutes"`
		TokenRefreshLeadMinutes *int                       `json:"token_refresh_lead_minutes"`
		SpecDir                 *string                    `json:"spec_dir"`
		SpecRemoteURL           *string                    `json:"spec_remote_url"`
		SpecUpdateMinutes       *int                       `json:"spec_update_minutes"`
		DisableFastPath         *bool                      `json:"disable_fast_path"`
		IntentClassifyModel     *string                    `json:"intent_classify_model"`
		IntentTimeoutSeconds    *int                       `json:"intent_timeout_seconds"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if raw.Addr != nil {
		cfg.Addr = *raw.Addr
	}
	if raw.APIKey != nil {
		cfg.APIKey = *raw.APIKey
	}
	if raw.GlobalProxy != nil {
		cfg.GlobalProxy = *raw.GlobalProxy
	}
	if raw.SnapshotPath != nil {
		cfg.SnapshotPath = *raw.SnapshotPath
	}
	if raw.SQLitePath != nil {
		cfg.SQLitePath = *raw.SQLitePath
	}
	if raw.AccountStorePath != nil {
		cfg.AccountStorePath = *raw.AccountStorePath
	}
	if raw.StateTTLMinutes != nil && *raw.StateTTLMinutes > 0 {
		cfg.StateTTL = time.Duration(*raw.StateTTLMinutes) * time.Minute
	}
	if raw.MaxRecords != nil && *raw.MaxRecords > 0 {
		cfg.MaxRecords = *raw.MaxRecords
	}
	if raw.FirstByteTimeoutSeconds != nil && *raw.FirstByteTimeoutSeconds > 0 {
		cfg.FirstByteTimeout = time.Duration(*raw.FirstByteTimeoutSeconds) * time.Second
	}
	if raw.MaxAttempts != nil && *raw.MaxAttempts > 0 {
		cfg.MaxAttempts = *raw.MaxAttempts
	}
	if raw.ModelGroups != nil {
		cfg.ModelGroups = raw.ModelGroups
	}
	if raw.SettingsPath != nil {
		cfg.SettingsPath = *raw.SettingsPath
	}
	if raw.UsagePath != nil {
		cfg.UsagePath = *raw.UsagePath
	}
	if raw.QuotaRefreshMinutes != nil {
		cfg.QuotaRefreshMinutes = *raw.QuotaRefreshMinutes
	}
	if raw.TokenRefreshMinutes != nil {
		cfg.TokenRefreshMinutes = *raw.TokenRefreshMinutes
	}
	if raw.TokenRefreshLeadMinutes != nil {
		cfg.TokenRefreshLeadMinutes = *raw.TokenRefreshLeadMinutes
	}
	if raw.SpecDir != nil {
		cfg.SpecDir = *raw.SpecDir
	}
	if raw.SpecRemoteURL != nil {
		cfg.SpecRemoteURL = *raw.SpecRemoteURL
	}
	if raw.SpecUpdateMinutes != nil {
		cfg.SpecUpdateMinutes = *raw.SpecUpdateMinutes
	}
	if raw.DisableFastPath != nil {
		cfg.DisableFastPath = *raw.DisableFastPath
	}
	if raw.IntentClassifyModel != nil {
		cfg.IntentClassifyModel = *raw.IntentClassifyModel
	}
	if raw.IntentTimeoutSeconds != nil {
		cfg.IntentTimeoutSeconds = *raw.IntentTimeoutSeconds
	}
	return nil
}

func printBanner(cfg gateway.Config) {
	log.Printf("┌──────────────────────────────────────────────┐")
	log.Printf("│  Aether Cockpit · AI 算力聚合网关            │")
	log.Printf("│  微内核 + 能力 ISP + 状态缝合 + 协议转换      │")
	log.Printf("└──────────────────────────────────────────────┘")
	if strings.HasPrefix(cfg.Addr, "0.0.0.0") || strings.HasPrefix(cfg.Addr, ":") {
		log.Printf("[WARN] 当前监听所有网卡。若为自用请改回 127.0.0.1，")
		log.Printf("[WARN] 否则同网段的任何人都能使用你的账号额度。")
		if cfg.APIKey == "" {
			log.Printf("[WARN] 且未设置 API Key，等于完全开放。")
		}
	}
}

func displayAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr
	}
	if strings.HasPrefix(addr, "0.0.0.0:") {
		return "127.0.0.1:" + strings.TrimPrefix(addr, "0.0.0.0:")
	}
	return addr
}
