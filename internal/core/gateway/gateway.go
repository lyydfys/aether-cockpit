// Package gateway 是入站网关层。
//
// 它把三种下游协议收敛到同一套调度逻辑上，并把结果再翻译回下游协议。
// 关键约束：流式请求一旦吐出首字节，就绝不换渠道重试——
// 因为上下文、思考签名、工具调用状态在渠道之间都不通用，切过去只会产生
// 语义错乱的响应。重试只发生在拿到第一个事件之前。

package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aether-cockpit/internal/core/apppaths"
	"aether-cockpit/internal/core/appstart"
	"aether-cockpit/internal/core/auth"
	"aether-cockpit/internal/core/clients"
	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/core/gateway/inbound"
	"aether-cockpit/internal/core/gateway/transducer"
	"aether-cockpit/internal/core/hooks"
	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/library"
	"aether-cockpit/internal/core/mw"
	"aether-cockpit/internal/core/pipeline"
	"aether-cockpit/internal/core/pool"
	"aether-cockpit/internal/core/redact"
	"aether-cockpit/internal/core/rtstats"
	"aether-cockpit/internal/core/scheduler"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/core/state"
	"aether-cockpit/internal/core/transport"
	"aether-cockpit/internal/provider"
)

// maxBodyBytes 请求体上限 32MB（含 base64 图片）。
const maxBodyBytes = 32 << 20

// Config 网关配置。
type Config struct {
	Addr string `json:"addr"`
	// DesktopApp 表示当前进程是桌面端：仅桌面端暴露"开机自启"等壳能力。
	DesktopApp bool `json:"desktop_app,omitempty"`
	// APIKey 为空表示本地免鉴权（仅监听 127.0.0.1 时的合理默认）。
	APIKey      string `json:"api_key"`
	GlobalProxy string `json:"global_proxy"`
	// AdminSessionKey 非空时启用移动管理会话：管理面（/api/*、/mcp、
	// /metrics、/readyz、/version）必须携带 Authorization: Bearer <key>，
	// 回环来源不再自动放行。只存内存，绝不落盘或写日志。
	AdminSessionKey string `json:"-"`
	// RequireCallerKey 为真时，模型协议 /v1* 必须携带独立的 CallerKey。
	// 移动运行时始终开启；桌面零值保持原有本机免鉴权行为。
	RequireCallerKey bool          `json:"-"`
	CallerKey        string        `json:"-"`
	StateTTL         time.Duration `json:"state_ttl"`
	MaxRecords       int           `json:"max_records"`
	// SnapshotPath 会话快照落盘路径（SQLite 不可用时的降级方案）。
	SnapshotPath string `json:"snapshot_path"`
	// SQLitePath 会话状态持久化路径。非空时优先于 SnapshotPath。
	SQLitePath string `json:"sqlite_path"`
	// MiddlewarePath 请求改写规则的存储路径。
	MiddlewarePath string `json:"middleware_path"`
	// AccountStorePath 账号落盘路径。留空则纯内存（重启即丢账号）。
	AccountStorePath string `json:"account_store_path"`
	// CredentialKey 非空时账号凭证使用 envelope v2（AES-GCM + AAD）。
	// 桌面留空保持 simplecrypt 行为；移动端由 Android Keystore 解封后注入。
	CredentialKey []byte `json:"-"`
	// LibraryPath prompts / MCP / skills 库的落盘路径。留空用 data/library.json。
	LibraryPath string `json:"library_path,omitempty"`
	// CustomClientsPath 自定义客户端定义（默认 data/clients.json）。
	CustomClientsPath string `json:"custom_clients_path,omitempty"`
	// ModelsDir 上游实时模型目录缓存（默认 data/models）。
	ModelsDir string `json:"models_dir,omitempty"`
	// FirstByteTimeout 首字节超时，超时即换账号重试。
	FirstByteTimeout time.Duration `json:"first_byte_timeout"`
	// MaxAttempts 单次请求最多尝试几个账号。
	MaxAttempts int `json:"max_attempts"`
	// QueueWait 是所有账号都到并发上限时，请求最多排队等槽位多久
	// （0 用默认 5 秒；负数表示不排队，立刻 429）。
	QueueWait time.Duration `json:"queue_wait"`
	// QueueMaxWaiters 每个渠道最多允许多少个请求排队（0 用默认 64；负数表示不限）。
	// 超过上限直接 429：过载时快速失败，比让所有人一起轮询更稳。
	QueueMaxWaiters int `json:"queue_max_waiters"`
	// MaxConcurrentPerAccount 每账号并发上限。并发占位覆盖整条流（首字节不释放）。
	// 0 用默认 8；负数表示不限。
	MaxConcurrentPerAccount int `json:"max_concurrent_per_account"`
	// RPMPerAccount 每账号每分钟请求上限。0（默认）表示不限。
	RPMPerAccount int `json:"rpm_per_account"`
	// ModelGroups 虚拟模型聚合组：对外模型名 -> 渠道链（原始 JSON）。
	//
	// 兼容两种形态：
	//   ["kiro", "trae-intl"]                          固定优先级链（fallback）
	//   {"chain": ["kiro","trae-intl"], "strategy":"rotate"}  指定排序策略
	// 策略：priority = 按配置顺序；rotate = 每次请求轮转起点，均摊负载。
	// 客户端固定用组名，网关按策略排序后依次尝试，
	// 某渠道额度耗尽或 429 时自动切到下一家——客户端不需要改配置。
	ModelGroups map[string]json.RawMessage `json:"model_groups"`
	// SettingsPath 运行时设置文件（隐私遮蔽 / 价格表 / 虚拟组覆盖）。
	// 留空用 data/settings.json。
	SettingsPath string `json:"settings_path"`
	// UsagePath 用量聚合落盘路径。留空用 data/usage.json；设为 "-" 禁用统计。
	UsagePath string `json:"usage_path"`
	// QuotaRefreshMinutes 额度自动刷新间隔（分钟）。0 表示禁用。
	QuotaRefreshMinutes int `json:"quota_refresh_minutes"`
	// TokenRefreshMinutes 凭证保活检查间隔（分钟），0 用默认 10 分钟，负数禁用。
	TokenRefreshMinutes int `json:"token_refresh_minutes"`
	// TokenRefreshLeadMinutes 提前多久刷新即将过期的凭证，0 用默认 30 分钟。
	TokenRefreshLeadMinutes int `json:"token_refresh_lead_minutes"`
	// DisableFastPath 关闭 Responses 协议零损直通。
	//
	// 刻意用反向开关，而不加 EnableFastPath：bool 零值是 false。
	// 加 Enable 会导致未配置时不生效（"= 关闭"），与"默认应该开启"的期望相反。
	DisableFastPath bool `json:"disable_fast_path"`
	// SpecDir 渠道参数目录，支持改 JSON 即时生效。
	SpecDir string `json:"spec_dir"`
	// SpecRemoteURL 远程 spec 地址（必须 HTTPS，localhost 例外）。
	SpecRemoteURL string `json:"spec_remote_url"`
	// SpecUpdateMinutes 远程 spec 拉取间隔（分钟）。0 用默认 10 分钟，负数禁用。
	SpecUpdateMinutes int `json:"spec_update_minutes"`
	// IntentClassifyModel 意图路由的分类模型（必须是网关可解析的模型 ID，如
	// "auto/free" 或 "openai-compat/xxx"）。为空时 smart 策略自动退化为
	// priority（不分类）。分类请求经网关自身 loopback HTTP 调用。
	// 走完整的路由/渠道/用量链路；带 X-Aether-Intent 头防止分类请求
	// 再次触发分类的死循环。
	IntentClassifyModel string `json:"intent_classify_model"`
	// IntentTimeoutSeconds 分类调用超时。0 用默认 8 秒。
	IntentTimeoutSeconds int `json:"intent_timeout_seconds"`
	// LifecycleContext 取消时后台循环退出；nil 等价 context.Background()。
	LifecycleContext context.Context `json:"-"`
	// Platform 声明宿主平台能力；nil = 桌面默认（全部开启）。
	Platform *PlatformCapabilities `json:"-"`
	// 以下仅测试注入；nil 时使用真实实现。
	wslWarm        func()
	sessionsWarm   func()
	keepAwakeApply func(bool) error
}

// Gateway 是网关主体。
type Gateway struct {
	cfg  Config
	pool *pool.Manager
	// accountStoreErr 记录账号文件加载失败；移动端据此拒绝启动。
	accountStoreErr error
	store           *state.Store
	models          *ModelRouter
	// catalog 是上游实时模型目录（data/models/<provider>.json）。
	catalog *modelCatalog
	// intent 是 smart 策略的意图分类器（见 intent.go）。
	// 未配置 IntentClassifyModel 时它存在但 classify 恒返回 0。
	intent *intentClassifier
	tr     *transport.Factory
	deps   pipeline.Deps
	mux    *http.ServeMux

	// 生命周期：New 启动的后台循环统一登记，Close 取消并等待（见 lifecycle.go）。
	ctx       context.Context
	cancel    context.CancelFunc
	bg        sync.WaitGroup
	bgMu      sync.Mutex
	bgClosed  bool
	closeOnce sync.Once
	// storeCloseOnce 在后台循环退出后关闭 SQLite 句柄。
	storeCloseOnce sync.Once

	// platform 在 New 时解析；移动端传零值关闭桌面集成。
	platform PlatformCapabilities
	// keepAwakeOn 记录本网关是否真的开了保持唤醒，Close 时成对关闭。
	keepAwakeOn atomic.Bool
	// 测试注入点；nil 走真实实现。
	keepAwakeApply func(bool) error
	wslWarm        func()
	sessionsWarm   func()

	// handlers 缓存每个渠道组装好的管道（base 固定，中间件由 Manifest 决定）。
	handlers sync.Map // providerID -> pipeline.StreamHandler

	// sched 是签到调度器，启动后注入。用原子指针是因为它在 routes 注册后才可用。
	sched atomic.Pointer[scheduler.Scheduler]

	// logs 是请求审计日志（最近 500 条，前移覆盖）。
	logs *LogBuffer
	// identities 是 Codex 下游身份观测环（请求身份对照）。
	identities *identityRing

	// otlp 是请求审计的 OTLP/JSON 导出器（懒启动，见 otlp.go）。
	otlp *otlpExporter

	// 渠道监控（手动触发，结果留最近一次与历史，见 channel_monitor.go）。
	monitorMu      sync.Mutex
	monitor        map[string]*channelProbe
	monitorReading bool
	monitorAt      int64

	// 2FA 验证码库的读写锁（文件存储，条目数很少，见 twofa_api.go）。
	twofaMu sync.Mutex

	// 开机自启的读写钩子（测试可替换；见 autostart_api.go）。
	autoStartGet func() appstart.Status
	autoStartSet func(bool) error

	// 唤醒任务（账号级定时最小请求，见 wake.go）。
	wakeMu      sync.Mutex
	wakeTasks   []*wakeTask
	wakeRunning map[string]bool
	wakePath    string
	wakeSem     chan struct{}

	// 本机账号自动导入（见 autoimport.go）。
	autoImp autoImportRuntime

	// 异步图像任务（内存保留 1 小时，见 images_api.go）。
	imageTasksMu sync.Mutex
	imageTasks   map[string]*imageTask

	// MCP 健康检查结果缓存（5 分钟，fresh 强制刷新，见 mcp_check_cache.go）。
	mcpCache mcpCheckCache

	// MCP OAuth 登录：令牌存储（0600）与进行中的授权流程（见 mcp_signin.go）。
	mcpAuth     *mcpAuthStore
	mcpAuthOnce sync.Once
	signinFlows mcpSigninFlows

	// 桌面目录选择器的注入点（测试用；nil 时走系统对话框）。
	pickFolder func(context.Context) (string, error)

	// MCP Registry 市场缓存（10 分钟，见 library_market.go）。
	marketCache mcpMarketCache

	// skills.sh 市场与 GitHub 技能路径缓存（见 library_market_skills.go）。
	skillCache  skillMarketCache
	githubTrees githubTreeCache

	// 技能更新检查状态（见 library_skills_update.go）。
	skillCheckMu sync.Mutex
	skillChecks  map[string]map[string]any

	// 资源库服务器→Agent 的缓存（按全局配置 mtime/size 失效，见 library_agents_cache.go）。
	srvAgents srvAgentsCache

	// 会话用量解析缓存（按文件 mtime/size 失效，见 sessions_usage_api.go）。
	sessUseMu    sync.Mutex
	sessUseCache map[string]cachedSessionUsage

	// authC 是认证协调器（PKCE / 设备码），懒初始化。
	authC    *auth.Coordinator
	authOnce sync.Once

	// 脱敏引擎（懒初始化）。
	redactE    *redact.Engine
	redactOnce sync.Once

	// 请求改写规则引擎（懒初始化）。
	rewriteE    *mw.Engine
	rewriteOnce sync.Once

	// 配置档：一份 Agent 模型配置的快照。
	profMu   sync.Mutex
	profList []Profile

	// publicAddr 是实际监听地址。桌面端端口冲突时会与 cfg.Addr 不同。
	// 接入片段与诊断必须使用实际地址。
	addrMu     sync.RWMutex
	publicAddr string

	// settingsMu 保护 settings；privacy 是按设置编译的遮蔽引擎（nil = 未启用）。
	settingsMu   sync.Mutex
	settings     runtimeSettings
	settingsPath string
	privacy      atomic.Pointer[privacyEngine]
	// privacyErr 记录最近一次引擎重建失败的原因（nil = 无错误）。
	privacyErr atomic.Pointer[string]

	// usage 用量聚合存储（nil = 禁用统计）。
	usage *usageStore
	// keyQuota 是网关密钥的用量计数器（内存增量 + 在途预留）。
	// 限额判定走它而不是逐次翻账本：账本单日有条数上限，会漏算。
	keyQuota     *quotaTracker
	keyQuotaOnce sync.Once
	quotaHist    *quotaHistStore
	portSwitch   atomic.Pointer[func(addr string) (string, error)]
	mmLogins     *miniMaxLoginStore

	// wsActive 是当前打开的 Responses WebSocket 连接数（上限见 responses_ws.go）。
	wsActive atomic.Int32
	// wsKeyActive 是每个调用方密钥的 WS 连接数（受 Key 配额，见 responses_ws.go）。
	wsKeyMu     sync.Mutex
	wsKeyActive map[string]int

	// 远程实例额度的缓存与在途刷新（见 remote_quotas.go）。
	remoteMu    sync.Mutex
	remoteCache map[string]remoteQuotaEntry
	remoteBusy  map[string]bool

	// 会话回收站（惰性创建，见 sessions_trash.go）。
	trash     *sessionTrash
	trashOnce sync.Once

	// 请求管线插件钩子（见 pipeline_hooks.go）。
	hooks     *hooks.Registry
	hooksOnce sync.Once

	// 渠道网络档位与在途闸门（见 channel_net.go）。
	netProfiles *channelNetStore
	netOnce     sync.Once
	gate        *inflightGate
	gateOnce    sync.Once

	// 边缘 IP 固定表（见 netedge.go）：只作用于网关内的渠道，不写系统 hosts。
	hostPin     *hostPinStore
	hostPinOnce sync.Once

	// 边缘自动切换用到的探测函数；正常路径指向 probeEdgePath / scanEdgesPath，
	// 单测可替换成假实现。
	edgeProbe edgeProbeFunc
	edgeScan  edgeScanFunc

	// customIDs 是当前由"自定义渠道"注册的 provider id（见 custom_channels.go）。
	// 只放内存：启动时会按设置重新注册一遍，不需要落盘。
	customIDs []string

	// 压缩载荷 → 账号绑定（见 compact_pins.go）。
	compactPin     *compactPinStore
	compactPinOnce sync.Once

	// quota 额度卡片的后台补读去重（懒初始化）。
	quotaOnce sync.Once
	quota     *quotaRefreshState

	// traces 是路由页的逐请求轨迹环形缓冲。
	traces *traceStore

	// 正在等待并发槽位的请求数，按渠道计（/api/lanes 的 waiting）。
	laneWaitMu sync.Mutex
	laneWait   map[string]int
	// ledger 是逐请求账本（用量页「请求」标签）。
	ledger *ledgerStore
	// callerKeys 是调用方网关密钥（远程身份 + 计量 + 限额）。
	callerKeys *callerKeyStore
	// lan 是局域网共享监听（默认关闭）。
	lan lanState

	// 本机客户端配置注入（懒初始化）。
	clientsOnce sync.Once
	clientMgr   *clients.Manager

	// WSL 专用监听（按需开启；见 wsl_api.go）。
	wslListen wslListener

	// 库：prompts / MCP 服务器 / skills 的统一管理与一键写入
	// （internal/core/library）。
	library *library.Store

	startedAt time.Time
}

// SetPublicAddr 记录实际监听地址（桌面端端口错开时调用）。
func (g *Gateway) SetPublicAddr(addr string) {
	g.addrMu.Lock()
	defer g.addrMu.Unlock()
	g.publicAddr = addr
}

// PublicAddr 返回实际监听地址；未设置时退回配置值。
func (g *Gateway) PublicAddr() string {
	g.addrMu.RLock()
	defer g.addrMu.RUnlock()
	if g.publicAddr != "" {
		return g.publicAddr
	}
	return g.cfg.Addr
}

// SetScheduler 注入签到调度器，并把渠道级签到开关接上。
func (g *Gateway) SetScheduler(s *scheduler.Scheduler) {
	g.sched.Store(s)
	if s != nil {
		s.SetCheckinOn(g.checkinEnabled)
	}
}

// Scheduler 返回当前的调度器（可能为 nil）。
func (g *Gateway) Scheduler() *scheduler.Scheduler { return g.sched.Load() }

// New 创建网关。
// AccountStoreError 返回本次启动时账号文件加载失败的原因（无失败为 nil）。
func (g *Gateway) AccountStoreError() error { return g.accountStoreErr }

func New(cfg Config) *Gateway {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.FirstByteTimeout <= 0 {
		cfg.FirstByteTimeout = 60 * time.Second
	}
	st := state.New(state.Options{
		MaxRecords:   cfg.MaxRecords,
		TTL:          cfg.StateTTL,
		SnapshotPath: cfg.SnapshotPath,
		SQLitePath:   cfg.SQLitePath,
	})

	lifecycleCtx := cfg.LifecycleContext
	if lifecycleCtx == nil {
		lifecycleCtx = context.Background()
	}
	lifecycleCtx, cancel := context.WithCancel(lifecycleCtx)

	g := &Gateway{
		cfg:            cfg,
		autoStartGet:   appstart.Get,
		autoStartSet:   appstart.Set,
		pool:           pool.New(),
		store:          st,
		models:         NewModelRouter(),
		tr:             transport.NewFactory(cfg.GlobalProxy),
		logs:           NewLogBuffer(500),
		identities:     &identityRing{},
		traces:         newTraceStore(),
		mmLogins:       newMiniMaxLoginStore(),
		settingsPath:   cfg.SettingsPath,
		startedAt:      time.Now(),
		ctx:            lifecycleCtx,
		cancel:         cancel,
		platform:       resolvePlatformCapabilities(cfg.Platform),
		keepAwakeApply: cfg.keepAwakeApply,
		wslWarm:        cfg.wslWarm,
		sessionsWarm:   cfg.sessionsWarm,
	}
	if g.settingsPath == "" {
		g.settingsPath = apppaths.Data("settings.json")
	}
	g.loadSettings()
	// 模型重定向是路由层的显式虚拟名，启动时先注入；保存设置后会立即更新。
	g.models.SetModelRedirects(g.modelRedirects())
	g.syncWSLEnabled()
	g.warmSessions()
	if g.platform.WSL && !g.settings.WSLDisabled {
		// 预热 WSL 检测：wsl.exe 要起进程，第一次别卡在界面首个请求上。
		// 关掉 WSL 接入时这里一步都不跑（用户就是不想让网关碰 WSL）。
		g.goBackground(g.warmWSL)
	}
	if g.platform.KeepAwake && g.settings.KeepAwake {
		if err := g.applyKeepAwake(true); err != nil {
			log.Printf("[WARN] 恢复保持唤醒失败: %v", err)
		} else {
			g.keepAwakeOn.Store(true)
		}
	}
	if v := strings.TrimSpace(g.settings.ListenAddr); v != "" {
		g.cfg.Addr = v
	}
	if p := strings.TrimSpace(g.settings.GlobalProxy); p != "" {
		if err := g.tr.SetGlobalProxy(p); err != nil {
			log.Printf("[WARN] 已保存的全局代理无效，沿用启动参数: %v", err)
		} else {
			g.cfg.GlobalProxy = p
		}
	}
	g.syncRedactPrefs()
	// 请求管线插件钩子：与脱敏一样是设置驱动的，启动时装载一次。
	g.syncHooks()
	// 渠道网络档位与边缘固定表：启动时从设置装载。
	g.syncChannelNet()
	// 自定义渠道：按设置把它们注册进 provider 注册表。
	g.syncCustomChannels()
	// 链路优化：DoH 解析 + 多 IP 竞速。跨境渠道的不同边缘 IP 质量差异明显
	// （实测同一域名两个 Cloudflare 边缘差 100ms+），竞速能稳定挑到当前最优
	// 的那个；DoH 保证拿到的是真实地址而不是被污染的答案。
	//
	// 只替换建连逻辑：TLS/SNI/证书校验仍按域名完成，走代理时对端是 IP 字面量、
	// 优化器直接放行，不产生额外开销。
	g.tr.Optimizer = transport.NewOptimizer(defaultDoHServer, true)
	// 渠道边缘优选：用户扫描后钉住的 IP 优先于 DNS 结果。
	g.tr.Optimizer.LookupPins = func(host string) []string { return g.hostPins().Get(host) }
	// 边缘自动切换：默认用真实探测，测试可覆盖。
	g.edgeProbe = probeEdgePath
	g.edgeScan = scanEdgesPath
	g.callerKeys = newCallerKeyStore(filepath.Join(filepath.Dir(g.settingsPath), "caller-keys.json"))
	// 渠道页的 on/off 参与模型解析（闭包读实时设置，无需重建）。
	g.models.SetEnabledFilter(g.providerIsEnabled)
	// OTLP 导出器：读配置经 settingsMu；配置变更作废钩子由 settings.go 注册。
	g.otlp = gatewayOTLPExporter(g)
	g.rebuildPrivacyEngine()

	// 用量统计。设为 "-" 显式禁用。
	if cfg.UsagePath != "-" {
		p := cfg.UsagePath
		if p == "" {
			p = apppaths.Data("usage.json")
		}
		g.usage = newUsageStore(p)
		// 账本按天落盘在用量文件旁的 ledger/ 目录；UsagePath="-" 时一起禁用。
		g.ledger = newLedgerStore(filepath.Join(filepath.Dir(p), "ledger"))
		// 额度读数历史：曲线上要能看到昨天的读数，落盘在同目录。
		g.quotaHist = newQuotaHistory(filepath.Join(filepath.Dir(p), "quota-history.json"))
	}
	// 密钥限额计数器始终存在：即使禁用统计，限额也要能判（禁用时没有账本，
	// 就用纯内存计数，重启后清零——这是"没有历史可种"的必然结果）。
	g.keyQuota = newQuotaTracker()
	g.deps = pipeline.Deps{State: st, Redact: g.redactEngine(), Rewrite: g.rewriteEngine()}

	// 库存储。留空沿用桌面便携默认 data/library.json；移动端注入沙箱路径。
	libraryPath := cfg.LibraryPath
	if libraryPath == "" {
		libraryPath = apppaths.Data("library.json")
	}
	g.library = library.NewStore(libraryPath)
	// 已登录的远程 MCP：下发给 Agent 的配置改写成本机中继地址。
	g.library.SetMCPApplyHook(g.rewriteMCPForRelay)

	// 把账号池的额度视图接进路由框架，供 pace 等策略在渠道之间排序。
	// 取该渠道可用账号中剩余额度最大者的配额快照（含过期时刻）；
	// 无账号/无额度数据时返回 ok=false，策略会把该渠道排在队尾——
	// 查不到额度绝不影响正常路由（fail-open）。
	g.models.SetQuotaLookup(func(providerID string) (int, int64, bool) {
		best := -1
		var bestReset int64
		for _, env := range g.pool.ListByProvider(providerID) {
			if !env.Available() {
				continue
			}
			q := env.Quota()
			if q == nil || q.Remaining <= best {
				continue
			}
			best = q.Remaining
			bestReset = q.ExpiresAt
		}
		if best < 0 {
			return 0, 0, false
		}
		return best, bestReset, true
	})

	// 把账号池的时延估计接进路由框架，供 fast 策略排序。
	// 无样本时返回 ok=false，策略把该渠道排在已知样本之后（fail-open）。
	g.models.SetLatencyLookup(g.pool.LatencyFor)

	// 渠道权重：取该渠道下**账号权重之和**。
	//
	// 为什么求和而不是取最大：账号权重（accounts.json 的 weight）本来就是
	// "这个号能承担多少请求"的相对份额，一个渠道有两个权重 1 的号，它整体
	// 就该拿到两份份额。没有账号时返回 0，weight 策略按 1 处理。
	g.models.SetWeightLookup(func(providerID string) int {
		total := 0
		for _, env := range g.pool.All() {
			if env.ProviderID != providerID {
				continue
			}
			if w := env.Weight(); w > 0 {
				total += w
			}
		}
		return total
	})

	// 意图路由分类器：smart 策略的"大脑"。未配置 IntentClassifyModel 时
	// classify 恒返回 0，smart 自动退化为 priority——不配置就零开销。
	// 分类请求经网关自身 loopback HTTP 走完整链路（路由/渠道/用量/日志），
	// 分类模型的调用量在用量面板里与普通请求一样可见。
	g.intent = newIntentClassifier(g)
	g.models.SetIntentClassifier(g.intent.classify)

	// 注册虚拟模型聚合组：基线 + settings 覆盖 + 自动发现，统一重建。
	// 引用了未注册渠道的条目会被跳过，而不是整体失败——渠道分批接入时很常见。
	g.rebuildRoutingGroups()

	// 渠道注册必须早于账号恢复。
	//
	// 账号池按 provider_id 还原账号：`provider.Get(pa.ProviderID)` 查不到就进不了池
	// （只留在 pool 的 preserved 里，概览数不到）。所以**所有**能注册渠道的步骤
	// 都必须在 `SetStorePath` 之前完成：
	//   1. 自定义渠道 —— 上面 syncCustomChannels() 已注册；
	//   2. spec 声明的 reverse 渠道 —— 原先排在账号恢复之后，导致这些渠道下的账号
	//      每次重启都落在"未就绪"，用户看到的就是"接入的渠道清零了"。
	//
	// 顺序有讲究：先给"编译期已注册的渠道"拍快照，再加载 spec，
	// 之后 spec 里声明的 reverse 渠道才能注册，且不会被允许覆盖内置渠道。
	// （SnapshotRegistry 只在注册表发生变化时才有差别；这里与原先的位置之间没有
	//   任何注册动作，所以提前调用不改变快照内容。）
	provider.SnapshotRegistry()
	specStore := spec.Default()
	if cfg.SpecDir != "" {
		if err := specStore.LoadDir(cfg.SpecDir); err != nil {
			log.Printf("[WARN] spec 目录加载失败: %v", err)
		}
	}
	if cfg.SpecRemoteURL != "" {
		if err := specStore.SetRemote(cfg.SpecRemoteURL); err != nil {
			log.Printf("[WARN] 远程 spec 地址被拒绝: %v", err)
		}
	}
	// spec 里声明了 extra.reverse_profile 的条目会在这里变成真实渠道。
	// 失败只记日志，不让一个写错的 spec 拖垮启动。
	provider.ReconcileSpecChannels()

	// 账号恢复必须在 routes() 之前完成，否则第一批管理请求会看到空池。
	if len(cfg.CredentialKey) > 0 {
		g.pool.SetCredentialKey(cfg.CredentialKey)
	}
	if cfg.AccountStorePath != "" {
		if err := g.pool.SetStorePath(cfg.AccountStorePath, g.tr); err != nil {
			// 加载失败时账号池保持只读，避免后续空池回写覆盖原文件。
			g.accountStoreErr = err
			log.Printf("[WARN] 账号文件加载失败: %v", err)
		}
		// 响应 id 族统计与账号同目录：重启后官转率曲线不中断。
		// 读失败只影响历史曲线，不影响网关启动（见 rtstats.SetPath）。
		rtstats.SetPath(filepath.Join(filepath.Dir(cfg.AccountStorePath), "rtstats.json"))
	}
	// 兜底：万一仍有账号因渠道未就绪而没进池（例如 spec 目录本次读失败），
	// 只要渠道在当前注册表里可用，就把它们提升回池——概览才不会少报。
	if promoted, err := g.pool.PromoteUnloaded(g.tr); err != nil {
		log.Printf("[WARN] 提升未就绪账号失败: %v", err)
	} else if promoted > 0 {
		log.Printf("启动时把 %d 个未就绪账号提升回池", promoted)
	}
	// 账号恢复后把渠道级自定义请求头套上（保存时已套过的账号会幂等重套）。
	g.applyProviderTransportAll()
	// 渠道级"不校验证书"：恢复出来的客户端是工厂统铸的，要按渠道开关重建。
	for _, env := range g.pool.All() {
		if g.providerInsecureTLS(env.ProviderID) {
			if err := g.rebuildAccountClient(env); err != nil {
				log.Printf("[WARN] 重建 %s 客户端失败: %v", env.AccountID, err)
			}
		}
	}

	// 上游实时模型目录：加载磁盘缓存，并在路由表重建前注入模型来源。
	// 静态 Manifest 仍是兜底；没有缓存的渠道行为完全不变。
	g.catalog = newModelCatalog(cfg.ModelsDir, g)
	g.models.SetModelSource(g.catalogModels)
	g.models.SetModelAliaser(func(pid, modelID string) []string {
		if g.catalog == nil {
			return nil
		}
		return g.catalog.aliasesFor(pid, modelID)
	})
	g.models.SetProviderReady(func(pid string) bool {
		for _, env := range g.pool.ListByProvider(pid) {
			if env != nil && !env.Disabled() && env.Available() {
				return true
			}
		}
		return false
	})

	// spec 里的 models / middlewares 会改写 Manifest，必须在加载后重建路由表。
	g.models.Rebuild()
	specStore.SetOnChange(func() {
		// spec 里声明的 reverse 渠道要跟着上线/下线，再重建路由表，
		// 否则新渠道不会出现在候选里、删掉的还会被路由到。
		provider.ReconcileSpecChannels()
		g.models.Rebuild()
		// 管道是按 Manifest 里的中间件清单组装后缓存的，spec 改了 middlewares
		// 就得整表作废；不做这一步的话，新清单要等进程重启才生效。
		// 只改 base_url / 请求头这类参数不用动缓存（它们每请求现读，见 handlerFor）。
		g.invalidateHandlers()
		eventbus.Emit(eventbus.TopicSpec+".updated", specStore.Status())
	})

	g.routes()
	g.goBackground(g.monitorLoop)
	if g.platform.WindowsAutoImport {
		g.initAutoImport()
	}
	g.initWake()
	return g
}

// StartSpecAutoUpdate 启动远程 spec 定时拉取，返回停止函数。
func (g *Gateway) StartSpecAutoUpdate(ctx context.Context, interval time.Duration) func() {
	if g.cfg.SpecUpdateMinutes < 0 || interval <= 0 {
		return func() {}
	}
	return spec.Default().StartAutoUpdate(ctx, interval)
}

// Pool 暴露账号池（管理 API 需要）。
func (g *Gateway) Pool() *pool.Manager { return g.pool }

// Store 暴露状态存储。
func (g *Gateway) Store() *state.Store { return g.store }

// Models 暴露模型路由表。
func (g *Gateway) Models() *ModelRouter { return g.models }

// Transport 暴露网络工厂。
func (g *Gateway) Transport() *transport.Factory { return g.tr }

// StartAutoSave 启动状态快照。
func (g *Gateway) StartAutoSave(d time.Duration) func() { return g.store.StartAutoSave(d) }

// StartUsageAutoSave 启动用量聚合的周期落盘，返回停止函数（nil 安全）。
func (g *Gateway) StartUsageAutoSave(d time.Duration) func() {
	if g.usage == nil {
		return func() {}
	}
	return g.usage.StartAutoSave(d)
}

// ServeHTTP 实现 http.Handler。
//
// 管理 API 额外做一层"本机或 API Key"保护。
// 协议端点可能被外部客户端调用（用户主动监听 0.0.0.0 时），但管理 API
// 能增删账号、读日志，不能只依赖"用户应该设 API Key"这条口头约束。
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 处理器 panic 时先把栈写进日志（产品日志落盘），再交给 net/http 断连。
	defer func() {
		if p := recover(); p != nil {
			log.Printf("[PANIC] %s %s: %v\n%s", r.Method, r.URL.Path, p, debug.Stack())
			panic(p)
		}
	}()
	// CORS 预检必须在这里应答：路由是 "POST /v1/..." 这种带方法的模式，
	// OPTIONS 匹配不到任何路由，Go 的 ServeMux 会直接 404，浏览器
	// 的跨域调用因此永远失败。预检只回 CORS 头与 204，不泄露数据，
	// 也不代表放行后续请求（真正的 POST 仍要过鉴权与 IP 白名单）。
	if r.Method == http.MethodOptions &&
		(strings.HasPrefix(r.URL.Path, "/v1") || strings.HasPrefix(r.URL.Path, "/v1beta") || strings.HasPrefix(r.URL.Path, "/api/")) {
		g.setCORS(w, r)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if g.cfg.AdminSessionKey != "" {
		// 移动模式：回环不再等于可信。管理面统一要求内存会话凭据，
		// 且只认 Authorization 头——不接受 query 传 key。
		if g.mobileAdminPath(r.URL.Path) && !g.mobileAdminAllowed(r) {
			g.setCORS(w, r)
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": "管理接口需要移动会话凭据",
				"code":  "admin_auth_required",
			})
			return
		}
	} else if strings.HasPrefix(r.URL.Path, "/api/") &&
		r.URL.Path != "/api/health" &&
		!g.adminAllowed(r) {
		g.setCORS(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		status := http.StatusUnauthorized
		msg := "管理接口需要 API Key（本机访问默认放行）"
		if g.cfg.APIKey == "" {
			status = http.StatusForbidden
			msg = "管理接口仅限本机访问；如需远程管理请设置 api_key"
		}
		writeJSON(w, status, map[string]any{"error": msg})
		return
	}
	g.mux.ServeHTTP(w, r)
}

// adminAllowed 判断当前请求是否允许访问管理 API。
func (g *Gateway) adminAllowed(r *http.Request) bool {
	// Wails 的 AssetServer.Handler 在进程内调用，可能没有 RemoteAddr。
	// 这种请求没有跨进程边界，按本机处理。
	//
	// Wails v2.17 起会为合成请求填 RemoteAddr = "192.0.2.1:1234"
	// （RFC 5737 文档专用地址，不可路由，仅为了让 net/http 校验通过）。
	// 不能把它当远程来源拒掉——否则桌面端整个管理界面都会 403（实测踩过）。
	// RemoteAddr 来自内核网络栈的连接对端，外部请求无法伪造成这个值。
	if strings.TrimSpace(r.RemoteAddr) == "" || r.RemoteAddr == "192.0.2.1:1234" {
		return true
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	loopback := ip != nil && ip.IsLoopback()

	// 本机浏览器 / Wails webview / 本地 CLI 永远放行。
	// 它们与网关同机，不引入新的信任边界。
	if loopback {
		return true
	}
	// WSL 发行版走宿主侧的 WSL 虚拟网卡（host-only），同样与网关同机：
	// 该监听只绑在 WSL 网卡地址上，局域网请求的 LocalAddr 不是它。
	if g.servedOnWSL(r) {
		return true
	}
	// 远程管理同样受 IP 白名单约束（空名单 = 不限）。
	if !g.allowIPAllowed(clientIP(r)) {
		return false
	}
	if g.cfg.APIKey == "" {
		return false
	}
	token := extractToken(r)
	if token == "" {
		// EventSource 不能自定义请求头，允许 query 参数传递（仅本机或 HTTPS 场景推荐）。
		token = r.URL.Query().Get("key")
	}
	return token == g.cfg.APIKey
}

// routes 注册全部端点。
func (g *Gateway) routes() {
	m := http.NewServeMux()

	// --- 下游协议端点 ---
	m.HandleFunc("POST /v1/chat/completions", g.auth(g.handleChat))
	m.HandleFunc("POST /v1/responses", g.auth(g.handleResponses))
	m.HandleFunc("POST /v1/responses/compact", g.auth(g.handleResponsesCompact))
	m.HandleFunc("POST /responses/compact", g.auth(g.handleResponsesCompact))
	m.HandleFunc("GET /v1/responses", g.auth(g.handleResponsesWebSocket))
	m.HandleFunc("GET /v1/responses/{id}", g.auth(g.handleGetResponse))
	m.HandleFunc("DELETE /v1/responses/{id}", g.auth(g.handleDeleteResponse))
	m.HandleFunc("GET /v1/responses/{id}/input_items", g.auth(g.handleInputItems))
	m.HandleFunc("POST /v1/messages", g.auth(g.handleMessages))
	m.HandleFunc("POST /v1/systemone", g.auth(g.handleSystemOne))
	m.HandleFunc("POST /v1/messages/count_tokens", g.auth(g.handleCountTokens))
	m.HandleFunc("GET /v1/models", g.auth(g.handleModels))

	// --- 裸路径别名：Base URL 少写 /v1 时也能用 ---
	// 各家客户端对 Base URL 的拼法不一致：有的把 /chat/completions 直接拼在
	// 用户填的地址后面，用户填 http://host:7860 就会打到 /chat/completions。
	// 补一份别名，省掉"到底要不要带 /v1"的猜谜（/responses/compact 早已这么做过）。
	m.HandleFunc("POST /chat/completions", g.auth(g.handleChat))
	m.HandleFunc("POST /responses", g.auth(g.handleResponses))
	m.HandleFunc("POST /messages", g.auth(g.handleMessages))
	m.HandleFunc("GET /models", g.auth(g.handleModels))

	// --- Gemini 协议端点（/v1beta） ---
	// {target...} 通配段：模型名可能自带渠道前缀斜杠（如 kiro/claude-sonnet-4-6）。
	m.HandleFunc("POST /v1beta/models/{target...}", g.auth(g.handleGemini))
	m.HandleFunc("GET /v1beta/models", g.auth(g.handleGeminiModels))
	m.HandleFunc("GET /v1beta/models/{target...}", g.auth(g.handleGeminiGetModel))

	// --- 管理 API（供内嵌 Web UI 使用） ---
	m.HandleFunc("GET /api/health", g.handleHealth)
	// Prometheus 文本指标（企业级观测；不输出提示词/密钥/会话内容）。
	m.HandleFunc("GET /metrics", g.handleMetricsHTTP)
	// 运维探针与构建信息（部署/排障标准面）。
	m.HandleFunc("GET /healthz", g.handleHealthz)
	m.HandleFunc("GET /readyz", g.handleReadyz)
	m.HandleFunc("GET /version", g.handleVersion)
	m.HandleFunc("GET /api/providers", g.handleProviders)
	// WorkBuddy 出网身份（运行中生效值 + 可选 saved）：手机端设置页读取，
	// 避免客户端镜像 UA/版本常量。
	m.HandleFunc("GET /api/providers/workbuddy/identity", g.handleWorkBuddyIdentity)
	// 模型定价：官方目录 + 用户覆盖 + 展示币种。
	m.HandleFunc("GET /api/pricing", g.handlePricing)
	m.HandleFunc("POST /api/pricing/save", g.handlePricingSave)
	// 请求预设：可复用的 Body/Headers，挂在渠道或路由组上。
	m.HandleFunc("GET /api/presets", g.handlePresets)
	// 官转率：按响应 id 前缀统计各渠道的后端族群分布（口径见 rtstats 包注释）。
	m.HandleFunc("GET /api/routing-stats", g.handleRoutingStats)
	m.HandleFunc("POST /api/presets/save", g.handlePresetSave)
	m.HandleFunc("POST /api/presets/delete", g.handlePresetDelete)
	m.HandleFunc("POST /api/presets/attach", g.handlePresetAttach)
	// 酒馆预设导入预览：纯解析、只读；写入仍走 /api/presets/save（见 preset_import.go）。
	m.HandleFunc("POST /api/presets/import/preview", g.handlePresetImportPreview)
	// 供应商编辑器的保存 / 显钥 / 模型清单 / 连通性测试（见 provider_api.go）。
	m.HandleFunc("POST /api/provider/save", g.handleProviderSave)
	m.HandleFunc("POST /api/provider/windsurf/login", g.handleWindsurfLogin)
	m.HandleFunc("POST /api/provider/trae/device", g.handleTraeDeviceStart)
	m.HandleFunc("GET /api/provider/trae/device/{id}", g.handleTraeDevicePoll)
	m.HandleFunc("POST /api/provider/trae/device/{id}/cancel", g.handleTraeDeviceCancel)
	m.HandleFunc("POST /api/provider/kiro/device", g.handleKiroDeviceStart)
	m.HandleFunc("GET /api/provider/kiro/device/{id}", g.handleKiroDevicePoll)
	m.HandleFunc("POST /api/provider/kiro/device/{id}/cancel", g.handleKiroDeviceCancel)
	m.HandleFunc("POST /api/provider/zed/device", g.handleZedDeviceStart)
	m.HandleFunc("GET /api/provider/zed/device/{id}", g.handleZedDevicePoll)
	m.HandleFunc("POST /api/provider/zed/device/{id}/cancel", g.handleZedDeviceCancel)
	m.HandleFunc("POST /api/provider/codebuddy/device", g.handleCodeBuddyDeviceStart)
	m.HandleFunc("GET /api/provider/codebuddy/device/{id}", g.handleCodeBuddyDevicePoll)
	m.HandleFunc("POST /api/provider/codebuddy/device/{id}/cancel", g.handleCodeBuddyDeviceCancel)
	m.HandleFunc("POST /api/provider/workbuddy/device", g.handleWorkBuddyDeviceStart)
	m.HandleFunc("GET /api/provider/workbuddy/device/{id}", g.handleWorkBuddyDevicePoll)
	m.HandleFunc("POST /api/provider/workbuddy/device/{id}/cancel", g.handleWorkBuddyDeviceCancel)
	m.HandleFunc("POST /api/provider/qoder/device", g.handleQoderDeviceStart)
	m.HandleFunc("GET /api/provider/qoder/device/{id}", g.handleQoderDevicePoll)
	m.HandleFunc("POST /api/provider/qoder/device/{id}/cancel", g.handleQoderDeviceCancel)
	m.HandleFunc("POST /api/provider/minimax/device", g.handleMiniMaxDeviceStart)
	m.HandleFunc("GET /api/provider/minimax/device/{id}", g.handleMiniMaxDevicePoll)
	m.HandleFunc("POST /api/provider/minimax/device/{id}/cancel", g.handleMiniMaxDeviceCancel)
	m.HandleFunc("POST /api/provider/key", g.handleProviderKey)
	m.HandleFunc("POST /api/provider/models", g.handleProviderModelList)
	m.HandleFunc("POST /api/provider/detect", g.handleProviderDetect)
	m.HandleFunc("POST /api/provider/test", g.handleProviderTest)
	m.HandleFunc("POST /api/provider/accountmodels", g.handleProviderAccountModels)
	m.HandleFunc("POST /api/provider/accountmapping", g.handleProviderAccountMapping)
	m.HandleFunc("POST /api/provider/accountconcurrency", g.handleProviderAccountConcurrency)
	// 前端会调、但本产品没有对应真实动作的两个端点：显式 501 / 幂等视图，
	// 不做假成功（见 unsupported_api.go）。
	m.HandleFunc("POST /api/provider/move", g.handleProviderMove)
	m.HandleFunc("POST /api/provider/unfetch", g.handleProviderUnfetch)
	m.HandleFunc("GET /api/lanes", g.handleLanes)
	m.HandleFunc("GET /api/drift", g.handleDrift)
	// 额度提醒：窗口快到期但还剩很多 / 快用完 / 已用尽。
	m.HandleFunc("GET /api/alerts", g.handleQuotaAlerts)
	m.HandleFunc("POST /api/drift/apply", g.handleDriftApply)
	m.HandleFunc("POST /api/provider/on", g.handleProviderSwitch)
	m.HandleFunc("POST /api/provider/off", g.handleProviderSwitch)
	m.HandleFunc("POST /api/providers/arrange", g.handleProvidersArrange)
	m.HandleFunc("POST /api/provider/arrange", g.handleProviderAccountsArrange)
	m.HandleFunc("POST /api/provider/balance", g.handleProviderBalance)
	m.HandleFunc("POST /api/provider/list", g.handleProviderModelList)
	m.HandleFunc("POST /api/window/{action}", g.handleWindowStub)
	// 渠道编辑页的账号/密钥列表动作（aether 的 keyList）。
	m.HandleFunc("POST /api/keys/{action}", g.handleKeysAction)
	m.HandleFunc("GET /api/2fa", g.handle2FAList)
	m.HandleFunc("POST /api/2fa", g.handle2FAAdd)
	m.HandleFunc("DELETE /api/2fa/{id}", g.handle2FADelete)
	m.HandleFunc("GET /api/2fa/codes", g.handle2FACodes)
	m.HandleFunc("GET /api/accounts", g.handleAccounts)
	m.HandleFunc("POST /api/accounts", g.handleAddAccount)
	m.HandleFunc("DELETE /api/accounts/{id}", g.handleDeleteAccount)
	m.HandleFunc("POST /api/accounts/{id}/checkin", g.handleCheckIn)
	m.HandleFunc("GET /api/runtime", g.handleRuntime)
	m.HandleFunc("GET /api/logs", g.handleLogs)
	// 内置工具服务：客户端可直接调用，也可由智能体作为工具使用。
	m.HandleFunc("POST /v1/tools/web_search", g.auth(g.handleToolWebSearch))
	m.HandleFunc("POST /v1/images/generations", g.auth(g.handleImageGenerate))
	m.HandleFunc("POST /v1/images/edits", g.auth(g.handleImageEdit))
	m.HandleFunc("POST /v1/images/generations/async", g.auth(g.handleImageGenerateAsync))
	m.HandleFunc("POST /v1/images/edits/async", g.auth(g.handleImageEditAsync))
	m.HandleFunc("POST /v1/images/batches", g.auth(g.handleImageBatch))
	m.HandleFunc("GET /v1/images/tasks/{id}", g.auth(g.handleImageTaskGet))
	m.HandleFunc("POST /v1/videos/generations", g.auth(g.handleVideoGenerate))
	m.HandleFunc("POST /v1/videos/generations/async", g.auth(g.handleVideoGenerateAsync))
	m.HandleFunc("POST /v1/videos", g.auth(g.handleVideoCreate))
	m.HandleFunc("GET /v1/video-tasks/{id}", g.auth(g.handleVideoTaskGet))
	m.HandleFunc("GET /v1/videos/{id}", g.auth(g.handleVideoGet))
	m.HandleFunc("GET /v1/videos/{id}/content", g.auth(g.handleVideoContent))
	m.HandleFunc("POST /v1/realtime/sessions", g.auth(g.handleRealtimeSession))
	m.HandleFunc("GET /v1/realtime", g.auth(g.handleRealtimeWS))
	m.HandleFunc("POST /v1/audio/transcriptions", g.auth(g.handleAudioProxy("/audio/transcriptions")))
	m.HandleFunc("POST /v1/audio/translations", g.auth(g.handleAudioProxy("/audio/translations")))
	m.HandleFunc("POST /v1/audio/speech", g.auth(g.handleAudioSpeech))
	m.HandleFunc("POST /transcribe", g.auth(g.handleAudioProxy("/audio/transcriptions")))
	m.HandleFunc("POST /api/tools/web-search", g.handleToolWebSearch)
	m.HandleFunc("POST /api/tools/image-test", g.handleImageTest)
	// 配置档案：一键把整套客户端切到某个组合。
	m.HandleFunc("POST /api/profile/{action}", g.handleProfileActionCompat)
	m.HandleFunc("GET /api/profiles", g.handleProfiles)
	m.HandleFunc("POST /api/profiles", g.handleSaveProfile)
	m.HandleFunc("DELETE /api/profiles/{id}", g.handleDeleteProfile)
	m.HandleFunc("POST /api/profiles/{id}/activate", g.handleActivateProfile)
	m.HandleFunc("GET /api/inject", g.handleInject)
	m.HandleFunc("GET /api/settings", g.handleSettings)
	m.HandleFunc("POST /api/settings", g.handleUpdateSettings)
	m.HandleFunc("POST /api/settings/port", g.handlePortSetting)
	m.HandleFunc("POST /api/settings/proxy", g.handleProxySetting)
	m.HandleFunc("POST /api/proxy/check", g.handleProxyCheck)
	m.HandleFunc("POST /api/settings/lan", g.handleLANSettings)
	m.HandleFunc("POST /api/settings/autostart", g.handleAutoStartSetting)
	m.HandleFunc("POST /api/settings/cors", g.handleCORSSettings)
	m.HandleFunc("POST /api/settings/search-quota", g.handleSearchQuota)
	m.HandleFunc("POST /api/settings/remote-quotas", g.handleRemoteQuotas)
	m.HandleFunc("POST /api/settings/remote-quotas/test", g.handleRemoteQuotaTest)
	m.HandleFunc("POST /api/settings/remote-quotas/use-provider", g.handleRemoteQuotaUseAsProvider)
	m.HandleFunc("POST /api/settings/{key}", g.handleUISetting)
	m.HandleFunc("POST /api/settings/allowips", g.handleAllowIPsSettings)
	m.HandleFunc("GET /api/usage", g.handleUsage)
	// 订阅额度卡片（aether 用量页的 subscriptions 区）。
	m.HandleFunc("GET /api/channels/status", g.handleChannelStatus)
	m.HandleFunc("POST /api/channels/check", g.handleChannelCheck)
	m.HandleFunc("GET /api/wake", g.handleWakeList)
	m.HandleFunc("POST /api/wake", g.handleWakeSave)
	m.HandleFunc("POST /api/wake/run", g.handleWakeRunAccount)
	m.HandleFunc("POST /api/wake/{id}", g.handleWakeUpdate)
	m.HandleFunc("POST /api/wake/{id}/run", g.handleWakeRunTask)
	m.HandleFunc("DELETE /api/wake/{id}", g.handleWakeDelete)
	m.HandleFunc("GET /api/settings/channel-monitor", g.handleChannelMonitorSetting)
	m.HandleFunc("POST /api/settings/channel-monitor", g.handleChannelMonitorSetting)
	m.HandleFunc("GET /api/upstream", g.handleUpstream)
	m.HandleFunc("GET /api/gateway/trace", g.handleGatewayTrace)
	m.HandleFunc("GET /api/gateway/history", g.handleGatewayHistory)
	m.HandleFunc("GET /api/gateway/route", g.handleGatewayRoute)
	m.HandleFunc("POST /api/gateway/unrest", g.handleGatewayUnrest)
	// 渠道/客户端图标：按品牌色生成首字母贴片，替代 aether 自带品牌图。
	m.HandleFunc("GET /icons/{file}", g.handleIcon)
	m.HandleFunc("GET /api/agents/cli", g.handleAgentsCLI)
	m.HandleFunc("POST /api/agents/cli", g.handleAgentsCLIRefresh)
	m.HandleFunc("POST /api/agents/cli/{id}", g.handleAgentsCLIUpdate)
	m.HandleFunc("GET /api/agents/install", g.handleAgentsInstall)
	m.HandleFunc("GET /api/agents/preview/{id}", g.handleAgentPreview)
	m.HandleFunc("POST /api/agents/{action}", g.handleAgentAction)
	m.HandleFunc("POST /api/agents/{action}/{id}", g.handleAgentAction)
	m.HandleFunc("GET /api/agent-models/{id}", g.handleAgentModels)
	m.HandleFunc("POST /api/agent-models/{id}", g.handleAgentModels)
	m.HandleFunc("POST /api/agent-fast", g.handleAgentFast)
	m.HandleFunc("GET /api/update", g.handleUpdateCheck)
	m.HandleFunc("POST /api/update/download", g.handleUpdateDownload)
	m.HandleFunc("GET /api/whatsnew", g.handleWhatsNew)
	m.HandleFunc("POST /api/whatsnew/seen", g.handleWhatsNewSeen)
	m.HandleFunc("POST /api/whatsnew/today", g.handleWhatsNewToday)
	m.HandleFunc("GET /api/plugins/updates", g.handlePluginUpdates)
	m.HandleFunc("GET /api/davsync", g.handleDavSync)
	m.HandleFunc("POST /api/davsync/save", g.handleDavSyncSave)
	m.HandleFunc("POST /api/davsync/now", g.handleDavSyncNow)
	m.HandleFunc("POST /api/davsync/auto", g.handleDavSyncAuto)
	m.HandleFunc("POST /api/davsync/restore", g.handleDavSyncRestore)
	m.HandleFunc("POST /api/davsync/off", g.handleDavSyncOff)
	m.HandleFunc("POST /api/davsync/dismiss", g.handleDavSyncDismiss)
	m.HandleFunc("POST /api/davsync/reveal", g.handleDavSyncReveal)
	m.HandleFunc("POST /api/davsync/undo", g.handleDavSyncUndo)
	m.HandleFunc("POST /api/davsync/upload", g.handleDavSyncUpload)
	m.HandleFunc("GET /api/cli", g.handleCLIStatus)
	m.HandleFunc("GET /api/importapps", g.handleImportApps)
	m.HandleFunc("GET /api/importapps/discovery", g.handleImportAppsDiscovery)
	m.HandleFunc("POST /api/importapps", g.handleImportAppsSubmit)
	m.HandleFunc("GET /api/importapps/auto", g.handleAutoImportStatus)
	m.HandleFunc("POST /api/importapps/auto", g.handleAutoImportToggle)
	m.HandleFunc("POST /api/importapps/auto/scan", g.handleAutoImportScan)
	m.HandleFunc("POST /api/importapps/auto/reset", g.handleAutoImportReset)
	m.HandleFunc("GET /api/caller-keys", g.handleCallerKeys)
	m.HandleFunc("GET /api/caller-keys/models", g.handleCallerKeysModels)
	m.HandleFunc("POST /api/caller-keys/{action}", g.handleCallerKeysAction)
	m.HandleFunc("GET /api/usage/quotas", g.handleUsageQuotas)
	m.HandleFunc("POST /api/usage/quotas/refresh", g.handleUsageQuotasRefresh)
	// aether 的 login/usage 契约：user → quota 映射（见 login_usage_api.go）。
	m.HandleFunc("GET /api/login/usage", g.handleLoginUsage)
	m.HandleFunc("GET /api/usage/quotas/history", g.handleUsageQuotasHistory)
	m.HandleFunc("GET /api/usage/quotas/history/holds", g.handleQuotaHistoryHolds)
	m.HandleFunc("GET /api/usage/requests", g.handleLedger)
	m.HandleFunc("GET /api/usage/requests.csv", g.handleLedgerCSV)
	m.HandleFunc("POST /api/usage/requests/export", g.handleLedgerExport)
	m.HandleFunc("GET /api/usage/requests/content", g.handleLedgerContent)
	m.HandleFunc("GET /api/usage/heatmap", g.handleLedgerHeatmap)
	m.HandleFunc("POST /api/usage/arrange", g.handleUsageArrange)
	m.HandleFunc("POST /api/usage/codex-reset", g.handleCodexReset)
	m.HandleFunc("POST /api/usage/workbuddy-checkin", g.handleUsageCheckIn("workbuddy"))
	m.HandleFunc("POST /api/usage/trae-checkin", g.handleUsageCheckIn("trae"))
	m.HandleFunc("POST /api/usage/qoder-checkin", g.handleUsageCheckIn("qoder"))
	m.HandleFunc("POST /api/usage/minimax-checkin", g.handleUsageCheckIn("minimax"))
	m.HandleFunc("GET /api/clients", g.handleClients)
	m.HandleFunc("POST /api/clients/{id}/apply", g.handleClientApply)
	m.HandleFunc("POST /api/clients/{id}/restore", g.handleClientRestore)
	m.HandleFunc("GET /api/wsl", g.handleWSL)
	m.HandleFunc("POST /api/wsl/refresh", g.handleWSLRefresh)
	m.HandleFunc("GET /api/launcher", g.handleLauncherInfo)
	m.HandleFunc("POST /api/launcher/launch", g.handleLauncherLaunch)
	m.HandleFunc("POST /api/launcher/desktop/launch", g.handleLauncherDesktopLaunch)
	m.HandleFunc("POST /api/launcher/desktop/instance", g.handleLauncherDesktopInstance)
	m.HandleFunc("POST /api/launcher/{id}/stop", g.handleLauncherStop)
	m.HandleFunc("POST /api/launcher/{id}/cleanup", g.handleLauncherCleanup)

	// --- 库（prompts / MCP 服务器 / skills / agents 统一管理与一键写入）---
	m.HandleFunc("GET /api/library", g.handleLibrary)
	m.HandleFunc("GET /api/library/found/content", g.handleFoundContent)
	m.HandleFunc("POST /api/library/mcp/check", g.handleMCPCheck)
	m.HandleFunc("POST /api/library/mcp/config", g.handleMCPConfig)
	m.HandleFunc("POST /api/library/hooks/save", g.handleHooksSave)
	m.HandleFunc("POST /api/library/hooks/remove", g.handleHooksRemove)
	m.HandleFunc("POST /api/library/mcp-signin", g.handleMCPSigninStart)
	m.HandleFunc("GET /api/library/mcp-signin", g.handleMCPSigninStart)
	m.HandleFunc("GET /api/library/mcp-signin/{id}", g.handleMCPSigninPoll)
	m.HandleFunc("POST /api/library/mcp-signin/{id}/cancel", g.handleMCPSigninCancel)
	m.HandleFunc("POST /api/library/mcp-signout", g.handleMCPSignout)
	m.HandleFunc("POST /mcp", g.handleMCPServer)
	m.HandleFunc("POST /mcp/{name}", g.handleMCPRelay)
	m.HandleFunc("GET /mcp/{name}", g.handleMCPRelay)
	m.HandleFunc("DELETE /mcp/{name}", g.handleMCPRelay)
	m.HandleFunc("GET /api/library/market/{path...}", g.handleLibraryMarket)
	m.HandleFunc("POST /api/library/market/{path...}", g.handleLibraryMarket)
	// 资料库页的其余端点：空对象兜底。
	for _, p := range []string{
		"library/market", "library/market/about",
	} {
		m.HandleFunc("GET /api/"+p, g.handleLibraryStub)
		m.HandleFunc("POST /api/"+p, g.handleLibraryStub)
	}
	m.HandleFunc("POST /api/library/prompts", g.handleSavePrompt)
	m.HandleFunc("DELETE /api/library/prompts/{id}", g.handleDeletePrompt)
	m.HandleFunc("POST /api/library/prompts/{id}/apply", g.handleApplyPrompt)
	m.HandleFunc("POST /api/library/mcp", g.handleSaveMCP)
	m.HandleFunc("DELETE /api/library/mcp/{id}", g.handleDeleteMCP)
	m.HandleFunc("POST /api/library/mcp/{id}/apply", g.handleApplyMCP)
	m.HandleFunc("POST /api/library/skills", g.handleSaveSkill)
	m.HandleFunc("POST /api/library/skills/how", g.handleSkillHow)
	m.HandleFunc("POST /api/library/skills/group", g.handleSkillGroup)
	m.HandleFunc("POST /api/library/skills/ungroup", g.handleSkillUngroup)
	m.HandleFunc("POST /api/library/skills/remove-gone", g.handleSkillsRemoveGone)
	m.HandleFunc("POST /api/library/skills/probe", g.handleSkillsProbe)
	m.HandleFunc("POST /api/library/skills/install", g.handleSkillsInstall)
	m.HandleFunc("POST /api/library/skills/check", g.handleSkillsCheck)
	m.HandleFunc("POST /api/library/skills/update", g.handleSkillsUpdate)
	m.HandleFunc("POST /api/library/skills/update-all", g.handleSkillsUpdateBulk)
	m.HandleFunc("POST /api/library/skills/update-some", g.handleSkillsUpdateBulk)
	m.HandleFunc("POST /api/library/skills/regroup", g.handleSkillsRegroup)
	m.HandleFunc("POST /api/library/skills/{action}", g.handleFoundSkillAction)
	m.HandleFunc("DELETE /api/library/skills/{id}", g.handleDeleteSkill)
	m.HandleFunc("POST /api/library/skills/{id}/apply", g.handleApplySkill)
	m.HandleFunc("POST /api/library/instructions/save", g.handleLibraryInstructionsSave)
	m.HandleFunc("POST /api/library/reveal", g.handleLibraryReveal)
	m.HandleFunc("POST /api/library/projects/{action}", g.handleLibraryProjectAction)
	m.HandleFunc("POST /api/library/projects/choose", g.handleProjectChoose)
	m.HandleFunc("POST /api/library/servers/save", g.handleLibraryServerSave)
	m.HandleFunc("POST /api/library/servers/tools", g.handleMCPToolsSave)
	m.HandleFunc("POST /api/library/servers/{action}", g.handleFoundServerAction)
	m.HandleFunc("POST /api/library/servers/remove", g.handleLibraryRemoveByName("servers"))
	m.HandleFunc("POST /api/library/subagents/save", g.handleSubagentSave)
	m.HandleFunc("POST /api/library/subagents/{action}", g.handleFoundSubagentAction)
	for _, kind := range []string{"servers", "skills", "hooks", "subagents"} {
		m.HandleFunc("POST /api/library/"+kind+"/agents", g.handleApplyAgents(kind))
		m.HandleFunc("POST /api/library/"+kind+"/agents-some", g.handleApplyAgentsSome(kind))
		m.HandleFunc("POST /api/library/"+kind+"/agents-all", g.handleApplyAgentsAll(kind))
		m.HandleFunc("POST /api/library/"+kind+"/remove-all", g.handleRemoveAllKind(kind))
	}
	m.HandleFunc("POST /api/library/skills/remove", g.handleLibraryRemoveByName("skills"))
	m.HandleFunc("POST /api/library/subagents/remove", g.handleLibraryRemoveByName("subagents"))
	m.HandleFunc("GET /api/library/rtk", g.handleLibraryRTK)
	m.HandleFunc("POST /api/library/rtk", g.handleRTKUnsupported)
	m.HandleFunc("POST /api/library/rtk/install", g.handleRTKUnsupported)
	m.HandleFunc("POST /api/library/rtk/upgrade", g.handleRTKUnsupported)
	m.HandleFunc("POST /api/library/rtk/path", g.handleRTKUnsupported)
	m.HandleFunc("GET /api/library/skill", g.handleLibrarySkillShow)
	m.HandleFunc("POST /api/library/agents", g.handleSaveAgent)
	m.HandleFunc("DELETE /api/library/agents/{id}", g.handleDeleteAgent)
	m.HandleFunc("POST /api/library/agents/{id}/apply", g.handleApplyAgent)
	m.HandleFunc("POST /api/accounts/{id}/tags", g.handleSetAccountTags)
	m.HandleFunc("POST /api/accounts/{id}/note", g.handleSetAccountNote)
	m.HandleFunc("POST /api/accounts/{id}/settings", g.handleAccountSettings)
	m.HandleFunc("POST /api/accounts/batch", g.handleAccountBatch)
	m.HandleFunc("GET /api/accounts/export", g.handleExportAccounts)
	m.HandleFunc("POST /api/accounts/export/bundle", g.handleAccountsBundleExport)
	m.HandleFunc("POST /api/accounts/import/bundle", g.handleAccountsBundleImport)
	m.HandleFunc("GET /api/backup/export", g.handleBackupExport)
	m.HandleFunc("POST /api/backup/export", g.handleBackupExportFile)
	m.HandleFunc("POST /api/backup/import", g.handleBackupImport)
	m.HandleFunc("POST /api/backup/push", g.handleBackupPush)
	m.HandleFunc("POST /api/backup/pull", g.handleBackupPull)

	// --- 签到调度 ---
	m.HandleFunc("GET /api/scheduler", g.handleScheduler)
	m.HandleFunc("POST /api/scheduler/run", g.handleSchedulerRun)
	m.HandleFunc("POST /api/scheduler/run/{id}", g.handleSchedulerRunProvider)
	m.HandleFunc("GET /api/scheduler/checkin-summary", g.handleSchedulerSummary)
	m.HandleFunc("POST /api/quota/refresh", g.handleQuotaRefresh)

	// --- 渠道参数热更新 ---
	m.HandleFunc("GET /api/specs", g.handleSpecs)
	m.HandleFunc("POST /api/specs/reload", g.handleSpecReload)

	// --- 自检 ---
	m.HandleFunc("GET /api/diagnose", g.handleDiagnose)

	// --- 事件流（实时推送）---
	m.HandleFunc("GET /api/events", g.handleEvents)

	// --- 认证中心（OAuth PKCE / 设备码 / 本机导入） ---
	m.HandleFunc("POST /api/auth/start", g.handleAuthStart)
	m.HandleFunc("GET /api/auth/{id}", g.handleAuthGet)
	m.HandleFunc("POST /api/auth/{id}/callback", g.handleAuthCallback)
	m.HandleFunc("POST /api/auth/{id}/cancel", g.handleAuthCancel)
	m.HandleFunc("POST /api/auth/{id}/commit", g.handleAuthCommit)
	m.HandleFunc("POST /api/providers/{id}/local-scan", g.handleLocalScan)
	m.HandleFunc("POST /api/providers/{id}/local-import", g.handleLocalImport)
	m.HandleFunc("POST /api/providers/{id}/models/refresh", g.handleRefreshProviderModels)
	m.HandleFunc("POST /api/providers/{id}/models/extra", g.handleProviderExtraModel)
	m.HandleFunc("POST /api/providers/models/refresh-all", g.handleRefreshAllProviderModels)
	m.HandleFunc("GET /api/models/resolve", g.handleResolveModel)
	m.HandleFunc("GET /api/model-redirects", g.handleModelRedirects)
	m.HandleFunc("POST /api/model-redirects", g.handleSaveModelRedirects)
	m.HandleFunc("POST /api/accounts/{id}/refresh", g.handleTokenRefresh)
	m.HandleFunc("POST /api/accounts/{id}/inject", g.handleInjectAccount)
	m.HandleFunc("GET /api/accounts/{id}/binding", g.handleAccountBinding)
	m.HandleFunc("POST /api/accounts/{id}/official/pause", g.handleOfficialPause)
	m.HandleFunc("POST /api/accounts/{id}/official/resume", g.handleOfficialResume)
	m.HandleFunc("POST /api/accounts/{id}/official/check", g.handleOfficialCheck)
	m.HandleFunc("GET /api/providers/codex/identity", g.handleCodexIdentity)
	m.HandleFunc("GET /api/import/alma", g.handleAlmaImportList)
	m.HandleFunc("POST /api/import/alma", g.handleAlmaImport)

	// --- aether 风格前端的兼容 API ---
	m.HandleFunc("GET /api/groups", g.handleGroups)
	m.HandleFunc("GET /api/plugins", g.handlePlugins)
	m.HandleFunc("POST /api/plugins", g.handlePluginSave)
	m.HandleFunc("GET /api/plugins/builtin", g.handleBuiltinPlugins)
	// 插件页的其余端点：空对象兜底
	for _, p := range []string{
		"plugin-pages", "plugin-pages/options", "plugin-pages/read",
		"plugin-panels", "plugins/listings", "plugins/check",
		"plugins/choose", "plugins/git", "plugins/github", "plugins/mirror",
	} {
		m.HandleFunc("GET /api/"+p, g.handlePluginStub)
		m.HandleFunc("POST /api/"+p, g.handlePluginStub)
	}
	m.HandleFunc("DELETE /api/plugins/{id}", g.handlePluginDelete)
	m.HandleFunc("GET /api/sessions", g.handleSessions)
	m.HandleFunc("GET /api/sessions/one", g.handleSessionOne)
	m.HandleFunc("GET /api/sessions/manage", g.handleSessionsManage)
	m.HandleFunc("GET /api/sessions/search", g.handleSessionsSearch)
	m.HandleFunc("GET /api/sessions/markdown", g.handleSessionsMarkdown)
	m.HandleFunc("GET /api/sessions/usage", g.handleSessionsUsageSummary)
	m.HandleFunc("GET /api/sessions/usage.csv", g.handleSessionsUsageCSV)
	m.HandleFunc("POST /api/sessions/export", g.handleSessionsExport)
	m.HandleFunc("GET /api/sessions/transcript", g.handleSessionsTranscript)
	m.HandleFunc("GET /api/masking/summary", g.handleMaskingSummary)
	m.HandleFunc("GET /api/masking/values", g.handleMaskingValues)
	m.HandleFunc("POST /api/masking/values", g.handleMaskingValues)
	m.HandleFunc("GET /api/masking/value", g.handleMaskingValue)
	m.HandleFunc("GET /api/masking/records", g.handleMaskingRecords)
	m.HandleFunc("GET /api/masking/record", g.handleMaskingRecord)
	m.HandleFunc("GET /api/masking/sent", g.handleMaskingSent)
	m.HandleFunc("POST /api/masking/preview", g.handleMaskingPreview)
	m.HandleFunc("POST /api/masking/clear", g.handleMaskingClear)
	m.HandleFunc("GET /api/settings/redact-actions", g.handleRedactSettings)
	m.HandleFunc("POST /api/settings/redact-actions", g.handleRedactSettings)
	m.HandleFunc("POST /api/settings/redact-rules", g.handleRedactRules)
	m.HandleFunc("POST /api/settings/redact-rule", g.handleRedactRule)
	m.HandleFunc("POST /api/settings/redact-allow", g.handleRedactAllow)
	m.HandleFunc("POST /api/settings/reveal", g.handleRevealSettings)
	m.HandleFunc("GET /api/privacy-model", g.handlePrivacyModel)
	m.HandleFunc("POST /api/icons", g.handleIcons)
	m.HandleFunc("POST /api/icons/favicon", g.handleIconFavicon)
	m.HandleFunc("POST /api/import/icon", g.handleImportIcon)
	m.HandleFunc("GET /api/icons/{name}", g.handleIconGet)
	m.HandleFunc("POST /api/privacy-model", g.handlePrivacyModel)
	m.HandleFunc("GET /api/sessions/stats", g.handleSessionsStats)
	// 会话「在终端重开」：生成 resume 命令并在新终端窗口里跑起来。
	m.HandleFunc("POST /api/sessions/terminal", g.handleSessionTerminal)
	// 请求管线插件钩子清单（确认配的钩子真的装上了）。
	m.HandleFunc("GET /api/pipeline-hooks", g.handlePipelineHooks)
	// 渠道网络档位与在途数（慢链路优化的可观测入口）。
	m.HandleFunc("GET /api/channels/net", g.handleChannelNet)
	m.HandleFunc("POST /api/channels/net", g.handleChannelNetSave)
	// 自定义渠道：运行时注册任意 OpenAI/Anthropic 兼容端点。
	m.HandleFunc("GET /api/custom-channels", g.handleCustomChannels)
	m.HandleFunc("POST /api/custom-channels", g.handleCustomChannelSave)
	m.HandleFunc("DELETE /api/custom-channels/{id}", g.handleCustomChannelDelete)
	m.HandleFunc("POST /api/custom-channels/probe", g.handleCustomChannelProbe)
	// 网络诊断：把"卡在哪一环"摊开（DNS / TCP / TLS / 首字节）。
	m.HandleFunc("GET /api/network/diag", g.handleNetDiag)
	// 边缘优选：扫描候选 IP、测机房与延迟，可钉到该渠道（不写 hosts）。
	m.HandleFunc("GET /api/network/edge-scan", g.handleEdgeScan)
	m.HandleFunc("POST /api/network/edge-pin", g.handleEdgePin)
	m.HandleFunc("GET /api/network/edge-pins", g.handleEdgePins)
	m.HandleFunc("GET /api/network/edge-auto", g.handleEdgeAuto)
	m.HandleFunc("POST /api/network/edge-auto", g.handleEdgeAuto)
	m.HandleFunc("GET /api/context", g.handleContextEmpty)
	m.HandleFunc("GET /api/tune", g.handleTuneEmpty)
	m.HandleFunc("POST /api/tune/{path...}", g.handleTuneAction)
	m.HandleFunc("GET /api/sessions/overview", g.handleSessionsOverview)
	// 会话页的其余子端点：返回空对象而不是 404
	for _, p := range []string{
		"sessions/progress",
		"sessions/terminal",
		"sessions/recording", "sessions/carry", "sessions/codex-provider",
	} {
		m.HandleFunc("GET /api/"+p, g.handleSessionsStub)
	}
	// 会话删除是"可撤销的删除"：进回收站、能恢复、能彻底清除。
	// 其余写入类操作（迁移等）仍保持只读并返回 501。
	m.HandleFunc("POST /api/sessions/delete", g.handleSessionsDelete)
	m.HandleFunc("POST /api/sessions/restore", g.handleSessionsRestore)
	m.HandleFunc("POST /api/sessions/purge", g.handleSessionsPurge)
	m.HandleFunc("GET /api/sessions/trash", g.handleSessionsTrash)
	for _, p := range []string{
		"sessions/relocate-claude",
	} {
		m.HandleFunc("POST /api/"+p, g.handleSessionsWriteReadOnly)
	}
	m.HandleFunc("POST /api/groups", g.handleGroups)
	m.HandleFunc("POST /api/groups/{action}", g.handleGroupAction)

	m.HandleFunc("GET /api/state", g.handleAetherState)
	m.HandleFunc("POST /api/state", g.handleAetherState)
	m.HandleFunc("POST /api/set", g.handleAetherSet)
	m.HandleFunc("POST /api/signin", g.handleSigninCompat)
	m.HandleFunc("GET /api/signin/{id}", g.handleSigninGetCompat)
	m.HandleFunc("POST /api/signin/{id}/cancel", g.handleSigninCancelCompat)
	m.HandleFunc("POST /api/signin/import", g.handleSigninImportCompat)
	m.HandleFunc("POST /api/sync", g.handleAetherSync)
	m.HandleFunc("POST /api/open", g.handleOpen)

	// --- 内嵌 UI ---
	g.registerUI(m)

	g.mux = m
}

// auth 提供轻量鉴权。
//
// 自用场景下默认不鉴权（只监听 127.0.0.1）。一旦配置了 APIKey，
// 则同时接受 OpenAI 风格（Authorization: Bearer）与 Anthropic 风格（x-api-key）。
func (g *Gateway) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		g.setCORS(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if g.cfg.RequireCallerKey {
			// 移动模式：模型协议与管理会话使用两把互不通用的 key。
			// caller key 由运行时从数据目录的持久化文件读出（重启不变，客户端可长期持有）；
			// 管理会话 key 每次启动随机生成、只在本机内存。只认请求头，绝不读取 query。
			if !g.mobileCallerAllowed(r) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{
					"error": "模型接口需要 caller key",
					"code":  "caller_auth_required",
				})
				return
			}
			next(w, r)
			return
		}
		// 远程来源先过 IP 白名单（本机回环不受限）。
		if !g.localRequest(r) && !g.allowIPAllowed(clientIP(r)) {
			ae := ir.NewAPIError(ir.ErrCodeUpstreamAuth, "来源地址不在允许列表里").WithStatus(http.StatusForbidden)
			transducer.WriteError(w, ir.ProtocolOpenAIChat, ae)
			return
		}
		token := extractToken(r)
		// 网关密钥（任何来源都可用）：归因 + 限额。
		if k, ok := g.callerKeys.match(token); ok {
			key := g.callerKeys.get(k.ID)
			// 限额判定：先预留额度再放行。
			// 只判"已用 ≥ 上限"是不够的——100 个并发请求会同时看到同一个
			// "未超限"快照，然后一起烧掉上限的 100 倍。预留把在途请求也算进
			// "已用"，并发突发因此不会穿透硬上限。
			res, allowed, why := g.reserveCallerKey(k.ID, key.Limit, r)
			if !allowed {
				// 被拒不写账本的话，"今天被限流拒了多少次"根本查不到。
				g.noteRejectedRequest(k.ID, why, r)
				ae := ir.NewAPIError(ir.ErrCodeUpstreamRateLimit, why).WithStatus(http.StatusTooManyRequests)
				transducer.WriteError(w, ir.ProtocolOpenAIChat, ae)
				return
			}
			if res != nil {
				// 成功路径由用量结算 settle（用实际值替换预留）；
				// 这里兜底 release，覆盖"没走到结算就返回"的所有分支。
				defer g.keyQuota.release(res)
			}
			// 每分钟请求数上限：滑窗 60 秒，与 token/费用窗口独立。
			if key.Limit != nil && key.Limit.RPM > 0 && !g.callerKeys.allowRPM(k.ID, key.Limit.RPM, time.Now()) {
				g.noteRejectedRequest(k.ID, "超过每分钟请求数上限", r)
				ae := ir.NewAPIError(ir.ErrCodeUpstreamRateLimit,
					fmt.Sprintf("密钥每分钟请求数超过上限（%d/分钟）", key.Limit.RPM)).WithStatus(http.StatusTooManyRequests)
				transducer.WriteError(w, ir.ProtocolOpenAIChat, ae)
				return
			}
			ctx := withCallerKey(r.Context(), k)
			ctx = withReservation(ctx, res)
			next(w, r.WithContext(ctx))
			return
		}
		// 本机回环受信任（与 aether 一致）；带密钥的请求已在上面归因。
		if g.localRequest(r) {
			next(w, r)
			return
		}
		// 远程：必须有主 API Key 或启用的网关密钥。
		if g.cfg.APIKey != "" && token == g.cfg.APIKey {
			next(w, r)
			return
		}
		transducer.WriteError(w, ir.ProtocolOpenAIChat,
			ir.NewAPIError(ir.ErrCodeUpstreamAuth, "远程访问需要 API Key 或网关密钥").WithStatus(http.StatusUnauthorized))
	}
}

// localRequest 判断请求是否与网关同一信任边界：本机回环，或从宿主侧
// WSL 网卡专用监听进来的请求（见 wsl_api.go）。
func (g *Gateway) localRequest(r *http.Request) bool {
	return isLocalRequest(r) || g.servedOnWSL(r)
}

// isLocalRequest 判断请求是否来自本机回环（含 Wails 进程内合成请求）。
// 带转发头的请求按远程处理：隧道/反代把外部流量送进来时 RemoteAddr 也是
// 127.0.0.1，只看回环会让"暴露到隧道"这条路径绕过密钥。
func isLocalRequest(r *http.Request) bool {
	for _, h := range []string{"X-Forwarded-For", "X-Real-IP", "CF-Connecting-IP"} {
		if strings.TrimSpace(r.Header.Get(h)) != "" {
			return false
		}
	}
	addr := strings.TrimSpace(r.RemoteAddr)
	if addr == "" || addr == "192.0.2.1:1234" {
		return true
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func extractToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if strings.HasPrefix(strings.ToLower(h), "bearer ") {
			return strings.TrimSpace(h[7:])
		}
		return strings.TrimSpace(h)
	}
	if h := r.Header.Get("x-api-key"); h != "" {
		return strings.TrimSpace(h)
	}
	// Google Gemini 客户端（Gemini CLI / google-genai SDK）把 Key 放这里。
	if h := r.Header.Get("x-goog-api-key"); h != "" {
		return strings.TrimSpace(h)
	}
	if h := r.Header.Get("api-key"); h != "" {
		return strings.TrimSpace(h)
	}
	return ""
}

func (g *Gateway) setCORS(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	// 设置页配置了来源白名单时只回显匹配的 Origin；空名单沿用 *。
	if origin := g.corsOriginFor(r.Header.Get("Origin")); origin != "" {
		h.Set("Access-Control-Allow-Origin", origin)
		if origin != "*" {
			h.Add("Vary", "Origin")
		}
	}
	h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, x-api-key, x-goog-api-key, x-aether-had, anthropic-version, anthropic-beta")
	h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
}

// --- 核心调度 ---

// dispatchResult 是一次成功打开的上游流。
type dispatchResult struct {
	env *provider.AccountEnv
	ch  <-chan ir.UnifiedStreamEvent
	// providerID 实际命中的渠道。
	providerID string
}

// serve 是三个协议共用的主流程。
func (g *Gateway) serve(w http.ResponseWriter, r *http.Request, proto ir.Protocol, parse func([]byte, string) (*ir.UnifiedChatRequest, error)) {
	tm := newRequestTimer()
	client := clientAgent(r)
	entry := &RequestLog{
		Time:     time.Now().Unix(),
		Protocol: string(proto),
	}
	// 逐请求账本：从读体开始记，快路径与早失败也留痕。
	var trace *traceRoute
	ck := callerKeyFrom(r.Context())
	callerKeyID := ""
	if ck != nil {
		callerKeyID = ck.ID
	}
	led := g.newLedgerPending(client, "", "", nil)
	if led != nil && ck != nil {
		led.rec.CallerKey = ck.ID
		led.rec.CallerKeyName = ck.Name
	}
	ledStatus, ledFail, ledErr := http.StatusOK, "", ""
	defer func() {
		entry.DurationMS = tm.totalMS()
		entry.FirstByteMS = tm.firstByteMS()
		// 真实请求的首 token 时间用来校准线路健康基线（只取成功请求）：
		// 探针只能反映网络往返，上游变慢只有真实请求看得见。
		if entry.Status == "ok" && entry.FirstByteMS > 0 && entry.ProviderID != "" {
			g.noteEdgeLiveTTFT(entry.ProviderID, entry.FirstByteMS)
		}
		// 先结算账本，把 token/费用回填到日志，再入环形缓冲；
		// 否则日志会被 Push 在 finalize 之前，详情抽屉永远缺 token。
		if led != nil {
			led.finalize(g, entry, trace, ledStatus, ledFail, ledErr)
		}
		g.logs.Push(*entry)
		// OTLP 导出：懒启动 worker，未配置 endpoint 时零开销。
		if g.otlp != nil {
			g.otlp.enqueue(*entry)
		}
		// Fast-Path 的字节流不经过 IR，拿不到 usage 事件，只记请求数。
		if entry.FastPath && g.usage != nil {
			g.usage.record(usageCell{
				Date: usageDate(time.Now()), ProviderID: entry.ProviderID,
				Model: entry.Model, AccountID: entry.AccountID, Client: client, CallerKey: callerKeyID,
				Requests: 1, DurationMS: entry.DurationMS, FirstByteMS: entry.FirstByteMS,
			})
		}
		// 发布到事件总线：Wails 前端据此做实时推送，
		// 日志面板、告警、统计都只是订阅者，不需要改发布方。
		eventbus.Emit(eventbus.TopicRequest+".completed", entry)
	}()

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		entry.Status = "error"
		entry.ErrCode = ir.ErrCodeBadRequest
		entry.ErrMsg = "读取请求体失败"
		transducer.WriteError(w, proto, ir.NewAPIError(ir.ErrCodeBadRequest, "读取请求体失败").WithCause(err))
		return
	}

	respID := newResponseID(proto)

	// Fast-Path：下游与上游同为 Responses 且为流式时，原样透传字节流。
	// 放在 parse 之前是有意的——直通的价值之一就是连解析都省掉。
	if g.tryFastPath(w, r, proto, body, entry, tm) {
		if led != nil {
			led.rec.FastPath = true
			if entry.Status == "error" {
				ledStatus = http.StatusBadGateway
				ledFail = "other"
				ledErr = entry.ErrMsg
			}
		}
		return
	}

	req, err := parse(body, respID)
	if err != nil {
		ae := ir.AsAPIError(err)
		entry.Status = "error"
		entry.ErrCode = ae.Code
		entry.ErrMsg = ae.Message
		ledStatus, ledFail = traceFailCode(ae)
		ledErr = ae.Message
		transducer.WriteError(w, proto, err)
		return
	}
	if req.Meta == nil {
		req.Meta = make(map[string]any, 4)
	}
	req.Meta["response_id"] = respID

	entry.Model = req.Model
	if led != nil {
		led.rec.Req = req.Model
		led.rec.Kind = string(proto)
	}
	// 网关密钥的模型白名单：不在名单里的模型直接拒绝。
	if ck != nil {
		if key := g.callerKeys.get(ck.ID); key != nil && !key.servesModel(req.Model) {
			ae := ir.NewAPIError(ir.ErrCodeBadRequest, "该网关密钥不允许使用模型 "+req.Model).WithStatus(http.StatusForbidden)
			entry.Status = "error"
			entry.ErrCode = ae.Code
			entry.ErrMsg = ae.Message
			ledStatus, ledFail = traceFailCode(ae)
			ledErr = ae.Message
			transducer.WriteError(w, proto, ae)
			return
		}
	}
	// 推理档位先按模型目录收窄：不支持的值映射到最近档，避免上游 400。
	// "没指定时按请求难度自适应"放在 dispatch 里做——组规则可能强制档位
	// （UseEffort），必须等规则跑完再决定，否则规则永远被自适应抢先覆盖。
	req.ReasoningEffort = g.clampReasoningEffort(req.Model, req.ReasoningEffort)

	entry.Stream = req.Stream
	entry.Items = len(req.Items)
	entry.PreviousID = req.PreviousResponseID != ""

	// 会话指纹必须在调度之前算好：粘性路由要用它选账号，而账号选择发生在
	// 中间件之前。指纹只取首条 + 最后一条用户文本，不含账号——
	// 含账号的键无法在候选账号之间比较，等于让粘性永远不命中。
	if req.SessionKey == "" {
		req.SessionKey = pool.SessionFingerprint("", req)
	}
	// 第三级会话来源：客户端没自报会话 ID、也没有可用文本时（例如只有
	// 工具结果的续轮），用"来源 IP + UA + 凭证"兜底，保证同一客户端仍然粘住同一账号。
	if req.Meta != nil {
		if _, has := req.Meta["client_fp"]; !has {
			req.Meta["client_fp"] = clientFingerprint(r)
		}
		if _, has := req.Meta["client_agent"]; !has {
			req.Meta["client_agent"] = maskAgentID(clientAgent(r))
		}
	}

	// 隐私遮蔽：发往上游之前把敏感串替换成占位符。
	// 映射表只存在于本请求的内存里，回复中出现占位符时原样还原。
	//
	// fail-closed：开关打开但引擎建不出来（规则写错 / 密钥生成失败）时拒绝发送。
	// 宁可这一次调用失败，也不能把用户以为已遮蔽的内容明文送到上游。
	var unmasker *privacyUnmasker
	if required, why := g.privacyState(); required {
		eng := g.currentPrivacyEngine()
		if eng == nil {
			reason := "隐私遮蔽已启用但规则引擎不可用"
			if why != "" {
				reason += "：" + why
			}
			apiErr := ir.NewAPIError(ir.ErrCodePrivacyUnavailable, reason)
			entry.Status = "error"
			entry.ErrCode = apiErr.Code
			entry.ErrMsg = apiErr.Message
			ledStatus, ledFail = traceFailCode(apiErr)
			ledErr = apiErr.Message
			transducer.WriteError(w, proto, apiErr)
			return
		}
		var maskErr error
		unmasker, maskErr = eng.maskRequest(req)
		if maskErr != nil {
			apiErr := ir.NewAPIError(ir.ErrCodePrivacyUnavailable,
				"隐私遮蔽失败，已拒绝发送（fail-closed）："+maskErr.Error())
			entry.Status = "error"
			entry.ErrCode = apiErr.Code
			entry.ErrMsg = apiErr.Message
			transducer.WriteError(w, proto, apiErr)
			return
		}
	}

	ctx := withDownstreamUA(r.Context(), r.UserAgent())
	// 路由组预设：只有请求命中虚拟路由组时才带；普通模型名不套路由预设。
	ctx = withPresetRoute(ctx, g.presetRouteFor(req.Model))
	// 意图路由防递归：X-Aether-Intent 头表示这是网关自己发出的分类请求。
	// 分类模型的请求同样走 dispatch，如果不加识别，smart 策略会为它再发起
	// 一次分类——分类模型配在虚拟组里时无限套娃。fromRouter=false 才允许
	// 注入 RouteContext.Intent。
	fromRouter := r.Header.Get(intentHeader) == "1"
	// 路由轨迹从"确定模型"开始记：页面要能点开每个请求看它试了谁。
	if g.traces != nil {
		trace = g.traces.begin(client, req.Model, req.SessionKey)
		defer trace.finish()
	}
	if led != nil {
		led.rec.Session = shortSession(req.SessionKey)
		if trace != nil {
			led.rec.RouteID = trace.ID
		}
		if fromRouter {
			led.rec.Purpose = "aether"
		}
		// 发出前先估算输入量，事后拿上游的 prompt_tokens 一比就知道有没有被截断。
		led.noteEstimate(ir.EstimateTokens(req))
	}
	res, apiErr := g.dispatch(ctx, req, entry, tm, fromRouter, trace)
	if apiErr != nil {
		entry.Status = "error"
		entry.ErrCode = apiErr.Code
		entry.ErrMsg = apiErr.Message
		ledStatus, ledFail = traceFailCode(apiErr)
		ledErr = apiErr.Message
		if g.usage != nil {
			g.usage.record(usageCell{
				Date: usageDate(time.Now()), ProviderID: entry.ProviderID,
				Model: req.Model, AccountID: entry.AccountID, Client: client, CallerKey: callerKeyID,
				Requests: 1, Errors: 1, DurationMS: tm.totalMS(),
			})
		}
		transducer.WriteError(w, proto, apiErr)
		return
	}

	entry.Status = "ok"

	// 已拿到首事件，此后不允许再换渠道。
	if unmasker != nil {
		res.ch = unmasker.wrapStreams(ctx, res.ch)
	}
	// 插件钩子 onEvent：可丢弃/改写每个流事件。放在脱敏之后，
	// 避免钩子看到还没还原的占位符。
	res.ch = g.withEventHooks(ctx, res.ch)
	// 流内错误（例如 Trae 在 SSE 里报额度耗尽）也要惩罚账号：
	// 首事件之后的错误由转接器/聚合器写回，不经过 dispatch 的错误分支。
	res.ch = g.watchStreamErrors(res.ch, res.env)
	if g.usage != nil || res.env != nil {
		res.ch = g.withUsageRecorder(ctx, res.ch, usageCell{
			Date: usageDate(time.Now()), ProviderID: res.providerID,
			Model: req.Model, AccountID: res.env.AccountID, Client: client, CallerKey: callerKeyID,
		}, res.env, req.SessionKey, led)
	}
	if req.Stream {
		g.streamOut(w, proto, res, req, respID)
		return
	}
	if outErr := g.collectOut(w, proto, res, req, respID); outErr != nil {
		// 上游在 200 流里报错（Qoder 的流内 envelope 就是这种）时，
		// 客户端拿到的是本函数写的 5xx；日志/台账也必须记成失败。
		entry.Status = "error"
		entry.ErrCode = outErr.Code
		entry.ErrMsg = outErr.Message
		ledStatus, ledFail = traceFailCode(outErr)
		ledErr = outErr.Message
	}
}

// dispatch 依次尝试候选渠道，直到有一个成功吐出不携带错误的首事件。
// fromRouter 为 true 表示本次请求来自网关自身的意图分类调用，
// ResolveSmart 会因此关闭再次分类（防递归，见 intent.go）。
func (g *Gateway) dispatch(ctx context.Context, req *ir.UnifiedChatRequest, entry *RequestLog, tm *requestTimer, fromRouter bool, trace *traceRoute) (*dispatchResult, *ir.APIError) {
	// 意图路由输入：最后一轮用户文本。sessionKey 取首条用户文本的哈希
	// （跨轮稳定，见 conversationFingerprint），分类器据此实现
	// "短追问直接继承上一轮意图"。非 smart 组 / 空文本时这些输入
	// 会被框架忽略，行为与原来的 Resolve 完全一致。
	//
	// 传 req.ReasoningEffort 进去是为了让组规则能按"请求要求的推理档"出规则
	// （例如高强度推理只走贵模型）。RouteOutcome.EffortOut 是规则强制指定的
	// 档位，非空时覆盖请求——只在客户端没有显式指定时生效，用户意图优先。
	bindings, outcome := g.models.ResolveSmartEffortSignals(req.Model, req.LastUserText(),
		conversationFingerprint(req), req.ReasoningEffort, fromRouter, routeSignalsFor(req))
	if outcome.EffortOut != "" && req.ReasoningEffort == "" {
		req.ReasoningEffort = g.clampReasoningEffort(req.Model, outcome.EffortOut)
	}
	// 规则没指定、客户端也没指定：按请求难度自适应（短问题 low、改代码/长上下文 high）。
	// 放在这里而不是入口处，是为了让组规则的 UseEffort 优先——两条都不能省。
	if req.ReasoningEffort == "" {
		req.ReasoningEffort = g.autoEffortFor(req)
	}
	if len(bindings) == 0 {
		avail := g.models.AvailableModelIDs(12)
		hint := strings.Join(avail, ", ")
		if hint == "" {
			hint = "（当前没有任何已登记的模型）"
		}
		return nil, ir.NewAPIErrorf(ir.ErrCodeNoProvider,
			"没有渠道提供模型 %q。已登记的模型：%s", req.Model, hint)
	}

	// previous_response_id 的强绑定渠道优先，避免跨渠道导致会话丢失。
	if pinned, ok := req.Meta["pinned_provider"].(string); ok && pinned != "" {
		bindings = moveToFront(bindings, pinned)
	}

	// 渠道级兜底：把每个候选所属渠道的 fallback 插在它后面。
	bindings = g.expandFallbacks(bindings)

	exclude := make(map[string]bool, 4)
	var lastErr *ir.APIError
	// 被跳过的"非 Chat 渠道"（cursor/windsurf/zed 这类只有账号与额度的通道）。
	// 只用于最终错误文案，不改变候选顺序与跳过逻辑。
	var nonChatChannels []string
	attempts := 0

	// 轨迹的候选顺序：每个绑定渠道的可用账号先铺开。
	// 页面才能把"没试过的"放在 left 里，而不是只画出已经试过的。
	if trace != nil {
		for _, b := range bindings {
			name := b.ProviderID
			if p, ok := provider.Get(b.ProviderID); ok {
				name = p.Manifest().DisplayName
			}
			envs := g.pool.ListByProvider(b.ProviderID)
			if len(envs) == 0 {
				trace.addSeat(traceSeat{ID: b.ProviderID, Kind: "provider", Provider: b.ProviderID, Name: name, Model: b.UpstreamModel})
				continue
			}
			for _, e := range envs {
				trace.addSeat(traceSeat{
					ID: e.AccountID, Kind: "account", Provider: b.ProviderID,
					Name: name, Who: e.Label, Model: b.UpstreamModel,
				})
			}
		}
	}

	// 容量等待：所有候选都被"账号并发上限"挡住时排队等槽位，而不是立刻 429。
	// 每个渠道的等待预算可以单独覆盖（渠道编辑器 高级 → Queue wait）；
	// 未设置时用全局预算（默认 5 秒，负数=不排队），客户端断开即放弃。
	waitSince := map[string]time.Time{}
	blocked := map[string]bool{}
	for {
		fullThisPass := map[string]bool{}
		fullProviders := map[string]bool{}
		sawConcurrency := false
		attemptedThisPass := false
		for _, b := range bindings {
			if blocked[b.ProviderID] {
				continue
			}
			// 风控冷静期内的渠道直接跳过：继续打只会把"可疑"升级成"确认滥用"。
			if !riskPauseUntil(b.ProviderID).IsZero() {
				continue
			}
			if attempts >= g.cfg.MaxAttempts {
				break
			}

			p, ok := provider.Get(b.ProviderID)
			if !ok {
				continue
			}
			// 插件钩子 onProvider：允许跳过某个渠道（成本太高、临时限流…）。
			if g.hookRegistry().RunProvider(ctx, req, b.ProviderID) {
				continue
			}
			chat, ok := provider.AsChat(p)
			if !ok {
				nonChatChannels = append(nonChatChannels, b.ProviderID)
				continue
			}

			h := g.handlerFor(p, chat)

			work := req.Clone()
			work.ProviderID = b.ProviderID
			work.UpstreamModel = b.UpstreamModel
			if work.Meta == nil {
				work.Meta = make(map[string]any, 4)
			}
			work.Meta["response_id"] = req.Meta["response_id"]
			work.Meta["upstream_session"] = req.Meta["upstream_session"]

			pinnedAccount, _ := req.Meta["pinned_account"].(string)
			// 压缩载荷绑定优先于 previous_response_id：密文只有签发它的
			// 账号能解开，换号会直接被上游拒绝（invalid_encrypted_content）。
			if acc := g.pinnedAccountFor(work.Items); acc != "" {
				pinnedAccount = acc
			}

			excludeFor := exclude
			if len(fullThisPass) > 0 {
				excludeFor = make(map[string]bool, len(exclude)+len(fullThisPass))
				for k := range exclude {
					excludeFor[k] = true
				}
				for k := range fullThisPass {
					excludeFor[k] = true
				}
			}
			route := g.providerRoute(b.ProviderID)
			env, selErr := g.pool.Select(b.ProviderID, pool.SelectOptions{
				PinnedAccount: pinnedAccount,
				SessionKey:    work.SessionKey,
				Exclude:       excludeFor,
				Model:         b.UpstreamModel,
				Mode:          g.selectMode(b.ProviderID, route.Routing),
				Affinity:      route.Affinity,
			})
			if selErr != nil {
				// 已尝试过的账号会被 exclude 掉；这里不要用"没有可用账号"
				// 盖掉真正的上游错误（单账号渠道失败时尤其重要）。
				if lastErr == nil {
					lastErr = selErr
				}
				continue
			}
			// 插件钩子 onAccount：允许跳过某个账号（被封、余额为 0…）。
			// 跳过时把该账号排除，让下一次 Select 换一个，而不是整条渠道放弃。
			if g.hookRegistry().RunAccount(ctx, req, env) {
				exclude[env.AccountID] = true
				continue
			}

			// 账号级模型映射（高并发网关 的 model_mapping）：精确名或通配。
			// 把路由解析出的上游模型名改写成该账号真正的上游名。
			if mapped := env.MapModel(work.UpstreamModel); mapped != "" {
				work.UpstreamModel = mapped
			}

			// 闸门一：每账号 RPM 令牌桶。
			// 闸门二：每账号并发槽位（占位覆盖整条流，首字节不释放）。
			// 两者都只代表本账号此刻没有容量：换下一个候选，
			// 绝不能当成上游故障去惩罚账号。
			if rpm := g.rpmFor(env); !env.AllowRequest(rpm, time.Now()) {
				lastErr = ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit,
					"账号 %s 达到每分钟请求上限（%d），已换下一个候选", env.AccountID, rpm)
				fullThisPass[env.AccountID] = true
				if trace != nil {
					trace.addTry(env, b.UpstreamModel, time.Now(), http.StatusTooManyRequests, "rate", false)
				}
				continue
			}
			maxConc := g.concurrencyFor(env)
			release, ok := env.TryAcquire(maxConc)
			if !ok {
				if _, seen := waitSince[b.ProviderID]; !seen {
					waitSince[b.ProviderID] = time.Now()
				}
				lastErr = ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit,
					"账号 %s 并发已满（上限 %d），已换下一个候选", env.AccountID, maxConc)
				fullThisPass[env.AccountID] = true
				fullProviders[b.ProviderID] = true
				sawConcurrency = true
				if trace != nil {
					trace.addTry(env, b.UpstreamModel, time.Now(), http.StatusTooManyRequests, "rate", false)
				}
				continue
			}

			work.Meta["account_id"] = env.AccountID
			exclude[env.AccountID] = true
			attempts++
			attemptedThisPass = true
			tryStart := time.Now()
			ch, err := g.openStream(ctx, h, env, work, tm)

			// 上下文超限自动续接：同一账号裁掉较早的对话后重试一次。
			// Fast-Path 有同样逻辑（context_continue.go）；这里补 IR 路径，
			// 覆盖 /v1/messages、Chat 与走 IR 的 Responses。
			if err != nil {
				ae := ir.AsAPIError(err).WithProvider(b.ProviderID)
				if isContextOverflowAPIError(ae) {
					if trimmed, dropped, ok := shrinkUnifiedRequest(work, contextKeepTailItems); ok {
						trimmed.Meta["context_trimmed"] = true
						if retryCh, retryErr := g.openStream(ctx, h, env, trimmed, tm); retryErr == nil {
							log.Printf("[CONTINUE] %s 上下文超限，裁剪 %d 条较早消息后重试成功", b.ProviderID, dropped)
							ch, err = retryCh, nil
							work = trimmed
							entry.TrimmedItems = dropped
						} else {
							err = retryErr
						}
					}
				}
			}

			if err != nil {
				release()
				ae := ir.AsAPIError(err).WithProvider(b.ProviderID)
				lastErr = ae
				// 风控信号优先：暂停整个渠道，不重试、不罚账号（不是凭证问题）。
				if isRiskSignal(ae) {
					pauseRisk(b.ProviderID)
					lastErr = ir.NewAPIError(ir.ErrCodeUpstreamRateLimit,
						"上游判定为异常行为（风控），已暂停该渠道 10 分钟；请勿频繁重试，并尽量使用官方客户端").
						WithProvider(b.ProviderID)
					entry.ProviderID = b.ProviderID
					entry.AccountID = env.AccountID
					return nil, lastErr
				}
				g.penalizeModel(env, ae, b.UpstreamModel)
				if trace != nil {
					status, fail := traceFailCode(ae)
					trace.addTry(env, b.UpstreamModel, tryStart, status, fail, false)
				}
				if !retryable(ae) {
					entry.ProviderID = b.ProviderID
					entry.AccountID = env.AccountID
					return nil, ae
				}
				continue
			}

			if trace != nil {
				trace.addTry(env, b.UpstreamModel, tryStart, http.StatusOK, "", true)
			}

			// 成功：并发槽位绑定到流生命周期上，流关闭或客户端断开退排空后才释放。
			ch = releaseOnClose(ctx, ch, release)

			env.MarkSuccess()
			if work.SessionKey != "" {
				g.pool.BindSticky(work.SessionKey, env.AccountID)
			}
			// 记录实际命中的渠道与账号，方便定位"这次请求走到了哪"。
			entry.ProviderID = b.ProviderID
			entry.AccountID = env.AccountID
			return &dispatchResult{env: env, ch: ch, providerID: b.ProviderID}, nil
		}
		// 这一轮真正发过请求且失败：只要还有尝试额度，就换下一个账号再来一轮
		// （账号级故障转移；exclude 已把失败的账号排掉）。
		if attemptedThisPass {
			if attempts >= g.cfg.MaxAttempts {
				break
			}
			continue
		}
		// 这一轮没有真正发请求，只是都被并发上限挡住：按各渠道自己的等待预算
		// 决定还能不能等；全部超预算就放弃（保留最后一次 429 的原因）。
		if !sawConcurrency {
			break
		}
		anyWaiting := false
		for pid := range fullProviders {
			since, seen := waitSince[pid]
			budget := g.queueWaitFor(pid)
			// budget <= 0 表示"不排队"（全局 QueueWait<0），立刻放弃。
			if !seen || budget <= 0 || time.Since(since) >= budget {
				blocked[pid] = true
				continue
			}
			anyWaiting = true
		}
		if !anyWaiting {
			break
		}
		registered := make([]string, 0, len(fullProviders))
		tooMany := false
		for pid := range fullProviders {
			if g.tryAddLaneWaiter(pid) {
				registered = append(registered, pid)
			} else {
				tooMany = true
				break
			}
		}
		if tooMany {
			for _, pid := range registered {
				g.addLaneWaiter(pid, -1)
			}
			break
		}
		select {
		case <-ctx.Done():
			for _, pid := range registered {
				g.addLaneWaiter(pid, -1)
			}
			return nil, lastErr
		case <-time.After(25 * time.Millisecond):
			for _, pid := range registered {
				g.addLaneWaiter(pid, -1)
			}
		}
	}

	if lastErr == nil {
		if len(nonChatChannels) > 0 {
			// 有候选渠道、但没有一个能对话：这是能力缺口，不是"没有渠道"。
			// 用 400 capability_unsupported 说清楚是哪几个渠道，避免用户只看到
			// "所有候选渠道均不可用" 去猜。
			lastErr = ir.NewAPIError(ir.ErrCodeUnsupported, nonChatChannelsMessage(nonChatChannels))
		} else {
			lastErr = ir.NewAPIError(ir.ErrCodeNoProvider, "所有候选渠道均不可用")
		}
	}
	return nil, g.attachResetHint(lastErr, bindings)
}

// nonChatChannelsMessage 生成"非 Chat 渠道"的错误文案：
// 去重、保留出现顺序、最多列 3 个，其余用「等 N 个」收口。
func nonChatChannelsMessage(ids []string) string {
	seen := make(map[string]bool, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	shown := unique
	if len(unique) > 3 {
		shown = unique[:3]
		return "渠道 " + strings.Join(shown, "、") + fmt.Sprintf(" 等 %d 个不支持对话（仅账号/额度）", len(unique))
	}
	return "渠道 " + strings.Join(shown, "、") + " 不支持对话（仅账号/额度）"
}

// attachResetHint 在全池冷却/耗尽时，把最近一次恢复时间作为 Retry-After。
// 调用方不必盲目重试，和账号冷却用同一窗口。
//
// 上限 120 秒：带自动重试的 agent 会把 Retry-After 当成硬等待，而额度恢复
// 可能是几小时后（实测出现过 85538 秒 ≈ 23.8 小时），那期间客户端会一直挂在
// "正在重试(1/10)"上，既不会换渠道也不会回落。给个短上限，让它到期重试——
// 这段时间里用户往往已经充值、加号或者启用了别的渠道。
const maxResetHint = 120 * time.Second

func (g *Gateway) attachResetHint(ae *ir.APIError, bindings []modelBinding) *ir.APIError {
	if ae == nil || ae.RetryAfter > 0 || g.pool == nil {
		return ae
	}
	status := ae.StatusCode()
	if status != http.StatusTooManyRequests && status != http.StatusServiceUnavailable && ae.Code != ir.ErrCodeNoAccount {
		return ae
	}
	var best time.Duration
	for _, b := range bindings {
		if d := g.pool.EarliestReset(b.ProviderID); d > 0 && (best == 0 || d < best) {
			best = d
		}
	}
	if best <= 0 {
		return ae
	}
	if best > maxResetHint {
		best = maxResetHint
	}
	return ae.WithRetryAfter(best)
}

// handlerFor 为渠道组装并缓存中间件管道。
func (g *Gateway) handlerFor(p provider.BaseProvider, chat provider.ChatCapability) pipeline.StreamHandler {
	id := p.Manifest().ID
	if v, ok := g.handlers.Load(id); ok {
		return v.(pipeline.StreamHandler)
	}
	base := pipeline.BuildBase(chat)
	// spec_overrides 必须排在 system_role_merge 之前：先注入 spec 声明的系统提示词，
	// 再让 system_role_merge 把 Instructions 折进首条 user 消息，顺序反了就注不进去。
	// 中间件每次请求都读 spec.Load(req.ProviderID)，所以改 spec 里的 base_url / 请求头
	// 这类参数即改即生效，缓存无需失效。
	// 但"管道由哪些中间件组成"是这里就定死的（spec.middlewares 会并进
	// Manifest.RequiredMiddlewares），改清单必须调 invalidateHandlers 让缓存作废，
	// 否则热更新的中间件清单永远不会被用上。
	// redact 排在最外层：先脱敏再让其他中间件动内容，
	// 这样 spec 注入的系统提示词也会被一并脱敏。
	names := append([]string{"redact", "rewrite", "spec_overrides"}, p.Manifest().RequiredMiddlewares...)
	h, _ := pipeline.Build(base, g.deps, names)
	g.handlers.Store(id, h)
	return h
}

// invalidateHandlers 丢弃全部已组装的管道缓存。
//
// 只在"中间件清单可能变了"时调用（目前是 spec 热更新）。sync.Map 没有 Clear，
// 逐个删即可：旧管道对象不可变，删到一半被并发 Load 到最多多用一次旧清单，
// 不会出错；而且这条路径不在请求热路径上。
func (g *Gateway) invalidateHandlers() {
	g.handlers.Range(func(k, _ any) bool {
		g.handlers.Delete(k)
		return true
	})
}

// openStream 调用上游并窥探首个事件，确保"首字节之前"才允许重试。
func (g *Gateway) openStream(ctx context.Context, h pipeline.StreamHandler, env *provider.AccountEnv, req *ir.UnifiedChatRequest, tm *requestTimer) (<-chan ir.UnifiedStreamEvent, error) {
	// 派生一个可取消的 ctx。
	//
	// 为什么必须这样做：一旦我们放弃这条流（超时 / 首事件即错误 / 换账号重试），
	// 就没有任何消费者再读 channel 了。上游 provider 的 goroutine 会阻塞在
	// "往 channel 发送"上，直到 channel 缓冲填满 —— 而如果外层 ctx 还活着
	// （客户端还连着），它会一直挂在那里。每超时一次泄漏一个 goroutine。
	// 长时间跑下来就是内存与连接双泄漏。
	// 注意：上游 ctx 必须**脱离**客户端的取消（WithoutCancel），否则客户端一断开
	// 上游请求立刻被取消，后面的 usage 事件根本读不到——排空抓用量就成了空话。
	// 客户端断开后的收尾由下面的 AfterFunc（宽限期后 cancelUp）与首字节前的
	// ctx.Done 分支负责，资源不会无限占用。
	upCtx, cancelUp := context.WithCancel(context.WithoutCancel(ctx))

	// 渠道网络档位：在途闸门。慢链路上放任并发只会互相拖死，这里按渠道
	// 限制同时在跑的流数。拿不到槽位最多等 300ms，然后照样放行——
	// 宁可慢一点，也绝不把请求卡死。槽位随流结束释放。
	var releaseSlot func()
	if env != nil {
		if prof := g.channelNet().Get(env.ProviderID); prof.MaxInflight > 0 {
			releaseSlot = g.channelGate().acquire(env.ProviderID, prof.MaxInflight, 300*time.Millisecond)
		}
	}
	releaseNow := func() {
		if releaseSlot != nil {
			releaseSlot()
			releaseSlot = nil
		}
	}

	ch, err := h(upCtx, env, req)
	if err != nil {
		releaseNow()
		cancelUp()
		return nil, err
	}
	if releaseSlot != nil {
		ch = releaseWhenClosed(ch, releaseNow)
	}

	timer := time.NewTimer(g.cfg.FirstByteTimeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		cancelUp()
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "客户端已取消").WithCause(ctx.Err())

	case <-timer.C:
		cancelUp()
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "上游首字节超时").
			WithProvider(env.ProviderID).WithRetryable(true)

	case ev, ok := <-ch:
		if !ok {
			cancelUp()
			return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "上游立即关闭了连接").
				WithProvider(env.ProviderID).WithRetryable(true)
		}
		if ev.Type == ir.EventError {
			cancelUp()
			err := ev.Err
			if err == nil {
				err = ir.NewAPIError(ir.ErrCodeUpstreamFailed, "上游返回未知错误").WithProvider(env.ProviderID)
			}
			return nil, err
		}

		// 成功：取消函数推迟到断连宽限期之后再执行。
		//
		// 客户端断开不等于上游该立刻停：usage 事件与收尾状态都出现在流末尾，
		// 立刻断开等于把这些数据一起丢掉（高并发网关 / aether 同样选择读完）。
		// 但必须有上限，否则客户端一断开上游就白跑一整轮。
		context.AfterFunc(ctx, func() {
			time.AfterFunc(streamDrainGrace, cancelUp)
		})

		if tm != nil {
			tm.markFirstByte()
		}
		return prepend(upCtx, ev, ch), nil
	}
}

// clientFingerprint 由来源信息派生一个稳定短哈希（IP + UA + 凭证）。
// 只用于会话粘性，不落盘、不参与鉴权。
func clientFingerprint(r *http.Request) string {
	h := sha256.New()
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host == "" {
		host = r.RemoteAddr
	}
	h.Write([]byte(host))
	h.Write([]byte{0})
	h.Write([]byte(r.Header.Get("User-Agent")))
	h.Write([]byte{0})
	h.Write([]byte(r.Header.Get("Authorization")))
	h.Write([]byte{0})
	h.Write([]byte(r.Header.Get("x-api-key")))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// addLaneWaiter 调整某渠道的等待数（可为负）。
func (g *Gateway) addLaneWaiter(providerID string, delta int) {
	g.laneWaitMu.Lock()
	if g.laneWait == nil {
		g.laneWait = map[string]int{}
	}
	g.laneWait[providerID] += delta
	if g.laneWait[providerID] <= 0 {
		delete(g.laneWait, providerID)
	}
	g.laneWaitMu.Unlock()
}

// tryAddLaneWaiter 在渠道排队数未达上限时登记一个等待者。
func (g *Gateway) tryAddLaneWaiter(providerID string) bool {
	g.laneWaitMu.Lock()
	defer g.laneWaitMu.Unlock()
	if g.laneWait == nil {
		g.laneWait = map[string]int{}
	}
	if max := g.queueMaxWaitersFor(providerID); max > 0 && g.laneWait[providerID] >= max {
		return false
	}
	g.laneWait[providerID]++
	return true
}

// queueMaxWaitersFor 返回某渠道允许的排队请求数（渠道覆盖 > 全局；0/负=不限）。
func (g *Gateway) queueMaxWaitersFor(providerID string) int {
	if v, ok := g.providerQueueLimit(providerID); ok {
		return v
	}
	return g.queueMaxWaiters()
}

// queueWaitFor 返回某渠道的排队等待预算（渠道覆盖 > 全局；<=0 视为不排队）。
func (g *Gateway) queueWaitFor(providerID string) time.Duration {
	if v, ok := g.providerQueueWait(providerID); ok {
		return time.Duration(v) * time.Second
	}
	return g.queueWait()
}

func (g *Gateway) queueMaxWaiters() int {
	if g.cfg.QueueMaxWaiters == 0 {
		return 64
	}
	return g.cfg.QueueMaxWaiters
}

// laneWaiting 返回某渠道此刻排队的请求数。
func (g *Gateway) laneWaiting(providerID string) int {
	g.laneWaitMu.Lock()
	defer g.laneWaitMu.Unlock()
	return g.laneWait[providerID]
}

// queueWait 返回容量排队预算（默认 5 秒；负数=不排队）。
func (g *Gateway) queueWait() time.Duration {
	if g.cfg.QueueWait < 0 {
		return 0
	}
	if g.cfg.QueueWait == 0 {
		return 5 * time.Second
	}
	return g.cfg.QueueWait
}

// quotaExhaustedHint 识别"额度耗尽"类文案（各渠道措辞不同）。
func quotaExhaustedHint(msg string) bool {
	m := strings.ToLower(msg)
	for _, sig := range []string{
		"exceeded the quota", "exceeded your current quota", "insufficient_quota",
		"quota exceeded", "quota exhausted", "free usage limit reached",
		"freeusagelimiterror", "usage limit exceeded", "gousagelimiterror",
		"额度已用尽", "额度已耗尽", "积分已用尽",
		"insufficient account funds", "insufficient funds", "payment required", "余额不足",
	} {
		if strings.Contains(m, strings.ToLower(sig)) {
			return true
		}
	}
	return false
}

// resetHintDuration 从上游文案里读多久后重置（形如 Resets in 145h10m54s，
// reset in 2h 30m）。读不到返回 0；超过 30 天按 30 天算（防脏数据）。
var resetHintRe = regexp.MustCompile(`(?i)resets?\s+in\s+(?:(\d+)\s*h)?\s*(?:(\d+)\s*m)?\s*(?:(\d+)\s*s)?`)
var resetHintCNRe = regexp.MustCompile(`(\d+)\s*小时\s*(?:(\d+)\s*分钟)?\s*后\s*重置`)

func resetHintDuration(msg string) time.Duration {
	parse := func(m []string, hIdx, mIdx, sIdx int) time.Duration {
		var d time.Duration
		get := func(i int) int {
			if i < 0 || i >= len(m) || m[i] == "" {
				return 0
			}
			n, _ := strconv.Atoi(m[i])
			return n
		}
		d = time.Duration(get(hIdx))*time.Hour + time.Duration(get(mIdx))*time.Minute + time.Duration(get(sIdx))*time.Second
		if d > 30*24*time.Hour {
			d = 30 * 24 * time.Hour
		}
		return d
	}
	if m := resetHintRe.FindStringSubmatch(msg); m != nil {
		return parse([]string{"", m[1], m[2], m[3]}, 1, 2, 3)
	}
	if m := resetHintCNRe.FindStringSubmatch(msg); m != nil {
		return parse([]string{"", m[1], m[2], ""}, 1, 2, 3)
	}
	return 0
}

// concurrencyFor 解析账号此刻生效的并发上限：
// 账号级覆盖 > 渠道级覆盖 > 全局设置（0 一律表示不限制）。
func (g *Gateway) concurrencyFor(env *provider.AccountEnv) int {
	if env != nil {
		if v, ok := env.ConcurrencyOverride(); ok {
			return v
		}
		if v, ok := g.providerConcurrency(env.ProviderID); ok {
			return v
		}
	}
	return g.maxConcurrentPerAccount()
}

// rpmFor 解析账号此刻生效的每分钟请求上限：
// 渠道级覆盖 > 全局设置（0 表示不限制）。
func (g *Gateway) rpmFor(env *provider.AccountEnv) int {
	if env != nil {
		if v, ok := g.providerRPM(env.ProviderID); ok {
			return v
		}
	}
	return g.cfg.RPMPerAccount
}

// maxConcurrentPerAccount 解析并发上限。0 用默认 8，负数表示不限。
func (g *Gateway) maxConcurrentPerAccount() int {
	switch {
	case g.cfg.MaxConcurrentPerAccount == 0:
		return 8
	case g.cfg.MaxConcurrentPerAccount < 0:
		return 0
	default:
		return g.cfg.MaxConcurrentPerAccount
	}
}

// releaseOnClose 把并发槽位的释放绑到流关闭上。
//
// 不能首字节就释放：那样闸门形同虚设，一个账号可以同时挂无限多条流。
// 客户端断开后（ctx.Done）不再下发，但仍把上游读完——上游最多 drainGrace
// 后会被取消，这里随通道关闭释放槽位。
func releaseOnClose(ctx context.Context, ch <-chan ir.UnifiedStreamEvent, release func()) <-chan ir.UnifiedStreamEvent {
	out := make(chan ir.UnifiedStreamEvent, 8)
	go func() {
		defer close(out)
		defer release()
		for {
			ev, ok := <-ch
			if !ok {
				return
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				// 客户端断开：继续把上游读完，但事件要**转发**给下游的用量记录器
				// （它在这条链的下一层排空），否则末尾的 usage 会被丢掉。
				// 注意：进入这里时手上已经读到 ev，必须先把这一条发出去，
				// 不能直接去读下一条——否则恰好卡在断连瞬间的那条事件（很可能
				// 就是末尾的 usage）会被丢掉。
				// 若下游已经退出，最多等 3 秒就丢弃剩余事件，避免阻塞上游。
				for {
					select {
					case out <- ev:
					case <-time.After(3 * time.Second):
						for range ch {
						}
						return
					}
					var ok bool
					ev, ok = <-ch
					if !ok {
						return
					}
				}
			}
		}
	}()
	return out
}

// prepend 把已读出的首事件放回流头。
func prepend(ctx context.Context, first ir.UnifiedStreamEvent, rest <-chan ir.UnifiedStreamEvent) <-chan ir.UnifiedStreamEvent {
	out := make(chan ir.UnifiedStreamEvent, 8)
	out <- first
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-rest:
				if !ok {
					return
				}
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

// penalize 根据错误类型给账号打上相应的惩罚状态。
//
// model 是本次失败请求的上游模型名：限流绝大多数是**模型级**的（同一个号上
// 别的模型还能用），把这些失败记成整个账号冷却会让一个模型 429 就把账号
// 打成"0 可用"。拿到模型名时记模型级；拿不到（媒体端点等）才退回账号级。
func (g *Gateway) penalize(env *provider.AccountEnv, err *ir.APIError) {
	g.penalizeModel(env, err, "")
}

// penalizeModel 是 penalize 的带模型版。
func (g *Gateway) penalizeModel(env *provider.AccountEnv, err *ir.APIError, model string) {
	// 上游明确说"额度耗尽"（不是普通限流）：把账号放进耗尽态，
	// 别每两分钟再撞一次。重置时间上游没给时保守给 1 小时，用户可 Try now。
	if quotaExhaustedHint(err.Message) {
		// 上游给了更长的重置窗口时按窗口走：OpenCode 免费额度的 429
		// 带 Retry-After ≈ 到下一个 UTC 零点，固定 1 小时会过早重撞。
		until := time.Now().Add(time.Hour)
		if err.RetryAfter > time.Hour {
			until = time.Now().Add(err.RetryAfter)
		}
		if d := resetHintDuration(err.Message); d > time.Until(until) {
			until = time.Now().Add(d)
		}
		env.MarkDepleted(until, err.Message)
		eventbus.Emit(eventbus.TopicAccount+".state_changed", env.Snapshot())
		return
	}
	switch err.Code {
	case ir.ErrCodeUpstreamRateLimit:
		// 优先用上游给的窗口（Retry-After / 5h·7d 重置头），没有才退回 2 分钟。
		// 固定冷却会把"5 小时窗口"当成"2 分钟窗口"，账号很快又撞回 429。
		cool := 2 * time.Minute
		if err.RetryAfter > 0 {
			cool = err.RetryAfter
		}
		// 有些上游在正文里给重置时间（Antigravity: "Resets in 145h10m54s"）：
		// 用真实窗口冷却，别每 2 分钟又去撞一次已经没额度的账号。
		if d := resetHintDuration(err.Message); d > cool {
			cool = d
		}
		// 限流同时记两笔：
		//   - 账号级：绝大多数上游的 429 是账号/Key 维度的配额（RPM/TPM），
		//     只记模型级会让同一个号继续被其他模型撞出 429；
		//   - 模型级：留一份细粒度状态，供排序（orderByModelRest）与
		//     "这个号只有某个模型被打满"的场景使用。
		// 过去只有账号级，rest.go 写好了却没接线；现在两笔都记。
		env.MarkCoolingDown(cool, err.Message)
		if strings.TrimSpace(model) != "" {
			env.MarkModelCoolingDown(model, cool, err.Message)
		}
		// Rate limited=Goes to the back：仍有额度的账号排到队尾，冷却结束后
		// 先让别的账号接，负载自然轮转（aether 的 Sink）。
		if g.providerRoute(env.ProviderID).Sink {
			g.pool.SetSink(env.AccountID, true)
		}
		// 把这轮 429 的窗口留下来：渠道没有用量接口时，用量页靠它
		// 显示"这个窗口已打满、何时重置"，比空白卡片诚实。
		env.NoteRateLimit(cool, time.Now())
		if g.quotaHist != nil {
			// 429 观测到的窗口也进历史：没有用量接口的渠道至少有曲线可看。
			g.quotaHist.record(env.ProviderID, env.Label, env.RateWindows(), nil)
		}
	case ir.ErrCodeUpstreamAuth:
		// 上游给了明确的重试窗口（如 WorkBuddy 14015/401 的 30 分钟）：这是
		// 临时过期，不是凭证永久失效——冷却账号（到期自动恢复），不要打进
		// invalid 永久态，否则一次抖动就得人工重新登录。
		if err.RetryAfter > 0 {
			env.MarkCoolingDown(err.RetryAfter, err.Message)
			eventbus.Emit(eventbus.TopicAccount+".state_changed", env.Snapshot())
			return
		}
		env.MarkInvalid(err.Message)
		// 官方订阅通道被上游拒绝授权：单独标记并暂停该通道，
		// 等用户「检查授权 / 恢复」后再参与路由；普通中转账号没有这一层。
		if g.isOfficialAccount(env) {
			env.SetOfficialPause("官方授权被拒：" + err.Message)
			g.saveAccountsNow()
		}
	case ir.ErrCodeNoAccount:
		// 不是账号本身的问题，不计惩罚。
		return
	case ir.ErrCodeBadRequest:
		// 请求构造 / 模型名不对：换账号也一样失败，罚账号只会把好号
		// 一个个撞进熔断。只记模型级休息（模型名可能确实在这个号上不存在）。
		if strings.TrimSpace(model) != "" {
			env.MarkModelCoolingDown(model, provider.ModelRestBackoff(1), err.Message)
		}
		eventbus.Emit(eventbus.TopicAccount+".state_changed", env.Snapshot())
		return
	default:
		env.MarkFailure(5)
	}
	// 状态变化推给前端，避免"账号已经冷却但界面还是绿的"。
	eventbus.Emit(eventbus.TopicAccount+".state_changed", env.Snapshot())
}

// retryable 判断是否值得换账号再试。
func retryable(err *ir.APIError) bool {
	if err == nil {
		return false
	}
	if err.Retryable {
		return true
	}
	switch err.Code {
	case ir.ErrCodeUpstreamRateLimit, ir.ErrCodeUpstreamFailed, ir.ErrCodeNoAccount:
		return true
	case ir.ErrCodeUpstreamAuth:
		// 单个账号的凭证失效不该把整条路由打死：标记该账号 invalid 后
		// 换下一个账号/下一个渠道继续试；全部失效时返回的仍是这条 401。
		return true
	default:
		return false
	}
}

// moveToFront 把指定渠道的绑定移到最前。
func moveToFront(bs []modelBinding, providerID string) []modelBinding {
	out := make([]modelBinding, 0, len(bs))
	for _, b := range bs {
		if b.ProviderID == providerID {
			out = append(out, b)
		}
	}
	for _, b := range bs {
		if b.ProviderID != providerID {
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		return bs
	}
	return out
}

// --- 输出 ---

// streamOut 流式输出。
func (g *Gateway) streamOut(w http.ResponseWriter, proto ir.Protocol, res *dispatchResult, req *ir.UnifiedChatRequest, respID string) {
	switch proto {
	case ir.ProtocolOpenAIResponses:
		t, err := transducer.NewResponsesTransducer(w, respID, req.Model)
		if err != nil {
			transducer.WriteError(w, proto, ir.NewAPIError(ir.ErrCodeInternal, err.Error()))
			return
		}
		_ = t.Stream(res.ch)

	case ir.ProtocolAnthropic:
		t, err := transducer.NewAnthropicTransducer(w, respID, req.Model)
		if err != nil {
			transducer.WriteError(w, proto, ir.NewAPIError(ir.ErrCodeInternal, err.Error()))
			return
		}
		_ = t.Stream(res.ch)

	case ir.ProtocolGemini:
		t, err := transducer.NewGeminiTransducer(w, respID, req.Model)
		if err != nil {
			transducer.WriteError(w, proto, ir.NewAPIError(ir.ErrCodeInternal, err.Error()))
			return
		}
		_ = t.Stream(res.ch)

	default:
		t, err := transducer.NewChatTransducer(w, respID, req.Model)
		if err != nil {
			transducer.WriteError(w, proto, ir.NewAPIError(ir.ErrCodeInternal, err.Error()))
			return
		}
		_ = t.Stream(res.ch)
	}
}

// watchStreamErrors 旁路观察流内错误事件：交给 penalize 后原样转发，
// 不改变事件顺序，也不吞掉事件。
func (g *Gateway) watchStreamErrors(ch <-chan ir.UnifiedStreamEvent, env *provider.AccountEnv) <-chan ir.UnifiedStreamEvent {
	if env == nil {
		return ch
	}
	out := make(chan ir.UnifiedStreamEvent, 8)
	go func() {
		defer close(out)
		for ev := range ch {
			if ev.Type == ir.EventError && ev.Err != nil {
				g.penalize(env, ir.AsAPIError(ev.Err))
			}
			out <- ev
		}
	}()
	return out
}

// collectOut 非流式输出。
func (g *Gateway) collectOut(w http.ResponseWriter, proto ir.Protocol, res *dispatchResult, req *ir.UnifiedChatRequest, respID string) *ir.APIError {
	agg, err := ir.Collect(res.ch)
	if err != nil {
		// 此时还没写 header，可以正常返回错误。
		ae := ir.AsAPIError(err)
		transducer.WriteError(w, proto, ae)
		return ae
	}
	body := transducer.RenderNonStream(proto, agg, respID, req.Model)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(body)
	return nil
}

// newResponseID 按协议生成 response id。
func newResponseID(proto ir.Protocol) string {
	var prefix string
	switch proto {
	case ir.ProtocolOpenAIResponses:
		prefix = "resp_"
	case ir.ProtocolAnthropic:
		prefix = "msg_"
	default:
		prefix = "chatcmpl-"
	}
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return prefix + "fallback" + time.Now().Format("150405")
	}
	return prefix + hex.EncodeToString(b)
}

// --- 协议入口 ---

func (g *Gateway) handleChat(w http.ResponseWriter, r *http.Request) {
	g.serve(w, r, ir.ProtocolOpenAIChat, inbound.ParseChatCompletions)
}

func (g *Gateway) handleResponses(w http.ResponseWriter, r *http.Request) {
	g.recordCodexIdentity(r)
	g.serve(w, r, ir.ProtocolOpenAIResponses, inbound.ParseResponses)
}

func (g *Gateway) handleMessages(w http.ResponseWriter, r *http.Request) {
	g.serve(w, r, ir.ProtocolAnthropic, inbound.ParseAnthropic)
}

// handleGemini 处理 /v1beta/models/{model}:generateContent 与 :streamGenerateContent。
//
// PathValue 拿到的是 "gemini-2.5-flash:generateContent" 这种 "模型:动作" 形式
// （也可能带 models/ 前缀）。流式与否由动作名决定；
// alt=sse 时输出 SSE 帧，否则输出单个 JSON 对象（Gemini CLI 两种都在用）。
func (g *Gateway) handleGemini(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimPrefix(r.PathValue("target"), "models/")
	// 动作后缀一定在最后一个冒号之后（模型名本身不含冒号）。
	idx := strings.LastIndex(target, ":")
	if idx < 0 {
		transducer.WriteError(w, ir.ProtocolGemini,
			ir.NewAPIError(ir.ErrCodeBadRequest, "缺少动作后缀：应为 models/<model>:generateContent 或 :streamGenerateContent").WithStatus(http.StatusBadRequest))
		return
	}
	model, action := target[:idx], target[idx+1:]
	model = strings.TrimSpace(model)

	stream := false
	switch action {
	case "streamGenerateContent":
		stream = r.URL.Query().Get("alt") == "sse"
	case "generateContent":
		stream = false
	case "countTokens":
		g.handleGeminiCountTokens(w, r, model)
		return
	default:
		transducer.WriteError(w, ir.ProtocolGemini,
			ir.NewAPIErrorf(ir.ErrCodeBadRequest, "不支持的动作 %q（支持 generateContent / streamGenerateContent / countTokens）", action).WithStatus(http.StatusBadRequest))
		return
	}

	parse := func(body []byte, requestID string) (*ir.UnifiedChatRequest, error) {
		req, err := inbound.ParseGemini(body, requestID, model)
		if err != nil {
			return nil, err
		}
		req.Stream = stream
		return req, nil
	}
	g.serve(w, r, ir.ProtocolGemini, parse)
}

// handleGeminiGetModel GET /v1beta/models/{id} —— 返回单个模型的元信息。
func (g *Gateway) handleGeminiGetModel(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.PathValue("target"), "models/")
	if g.availableRedirectNames()[name] {
		writeJSON(w, http.StatusOK, map[string]any{
			"name":                       "models/" + name,
			"displayName":                name,
			"supportedGenerationMethods": []string{"generateContent", "countTokens"},
		})
		return
	}
	for _, m := range g.models.KnownModels() {
		if m.ID == name || strings.HasSuffix(m.ID, "/"+name) {
			writeJSON(w, http.StatusOK, g.geminiModelMeta(m))
			return
		}
	}
	transducer.WriteError(w, ir.ProtocolGemini,
		ir.NewAPIError(ir.ErrCodeNoProvider, "模型不存在: "+name).WithStatus(http.StatusNotFound))
}

// handleGeminiCountTokens 粗估 token 数（与 /v1/messages/count_tokens 同一策略）。
func (g *Gateway) handleGeminiCountTokens(w http.ResponseWriter, r *http.Request, model string) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	total := len(body) / 4
	if total < 1 {
		total = 1
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"totalTokens": total,
		"model":       strings.TrimPrefix(model, "models/"),
	})
}

// handleModels 列出全部模型。
func (g *Gateway) handleModels(w http.ResponseWriter, r *http.Request) {
	agent := maskAgentID(clientAgent(r))
	hidden := g.hiddenModelsForAgent(agent)
	order := g.modelOrderForAgent(agent)
	avail := g.availableModelIDs()
	groupsAvail := g.availableGroups()
	redirectsAvail := g.availableRedirectNames()
	metas := make([]ir.ModelMeta, 0, len(g.models.KnownModels()))
	for _, m := range g.models.KnownModels() {
		if !g.modelVisible(m.ID) || hidden[m.ID] || !avail[m.ID] {
			continue
		}
		metas = append(metas, m)
	}
	sort.SliceStable(metas, func(i, j int) bool { return orderLess(order, metas[i].ID, metas[j].ID) })
	data := make([]any, 0, len(metas)+4)
	for _, m := range metas {
		entry := map[string]any{
			"id":       m.ID,
			"object":   "model",
			"created":  g.startedAt.Unix(),
			"owned_by": "aether-cockpit",
		}
		// 让客户端能发现窗口/能力/推理档位（models.dev + 实时目录），
		// 非标准字段，SDK 会忽略。
		if m.ContextWindow > 0 {
			entry["context_window"] = m.ContextWindow
		}
		entry["capabilities"] = map[string]any{
			"tool_call": m.Capabilities.ToolCall,
			"reasoning": m.Capabilities.Reasoning,
			"vision":    m.Capabilities.Vision,
			"streaming": m.Capabilities.Streaming,
		}
		info := g.catalogInfo(m.ID)
		if len(info.Efforts) > 0 {
			entry["efforts"] = info.Efforts
		}
		if info.CostKnown {
			entry["cost"] = map[string]any{
				"input_per_m":      info.CostIn,
				"output_per_m":     info.CostOut,
				"cache_read_per_m": info.CostCacheRead,
			}
		}
		data = append(data, entry)
	}
	groups := make([]string, 0, 8)
	for name := range g.models.VirtualGroups() {
		// 重定向优先于同名路由组（Resolve 也是这个顺序），清单里不重复列。
		if !hidden[name] && groupsAvail[name] && !redirectsAvail[name] {
			groups = append(groups, name)
		}
	}
	sort.SliceStable(groups, func(i, j int) bool { return orderLess(order, groups[i], groups[j]) })
	for _, name := range groups {
		data = append(data, map[string]any{
			"id":       name,
			"object":   "model",
			"created":  g.startedAt.Unix(),
			"owned_by": "aether-cockpit/virtual",
		})
	}
	// 模型重定向的虚拟名也作为可发现的逻辑模型列出。
	redirects := make([]string, 0, 8)
	for name := range redirectsAvail {
		if !hidden[name] {
			redirects = append(redirects, name)
		}
	}
	sort.SliceStable(redirects, func(i, j int) bool { return orderLess(order, redirects[i], redirects[j]) })
	for _, name := range redirects {
		data = append(data, map[string]any{
			"id":       name,
			"object":   "model",
			"created":  g.startedAt.Unix(),
			"owned_by": "aether-cockpit/redirect",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

// modelOrderForAgent 返回 Agents 页为某个 Agent 排的模型顺序。
func (g *Gateway) modelOrderForAgent(agent string) map[string]int {
	if agent == "" {
		return nil
	}
	order := map[string]int{}
	if list, ok := g.agentModelPrefs(agent)["order"].([]any); ok {
		for i, x := range list {
			if s, ok := x.(string); ok {
				order[s] = i
			}
		}
	}
	return order
}

func orderLess(order map[string]int, a, b string) bool {
	if len(order) == 0 {
		return false
	}
	ia, oka := order[a]
	ib, okb := order[b]
	switch {
	case oka && okb:
		return ia < ib
	case oka:
		return true
	case okb:
		return false
	}
	return false
}

// hiddenModelsForAgent 返回某个 Agent 在 Agents 页里隐藏的模型/组。
func (g *Gateway) hiddenModelsForAgent(agent string) map[string]bool {
	if agent == "" {
		return nil
	}
	hidden := stringSet(g.agentModelPrefs(agent)["hidden"])
	if len(hidden) == 0 {
		return nil
	}
	return hidden
}

// modelVisible 报告模型是否对下游可见：
//   - 渠道被停用时它的模型一起隐藏；
//   - 渠道勾了"只在路由分组里使用"（unlisted）时，直接条目也隐藏，
//     但虚拟分组名仍会列出、分组内与显式请求照常可用。
func (g *Gateway) modelVisible(modelID string) bool {
	if i := strings.Index(modelID, "/"); i > 0 {
		pid := modelID[:i]
		if !g.providerIsEnabled(pid) {
			return false
		}
		if g.providerUnlisted(pid) {
			return false
		}
	}
	return true
}

// handleGeminiModels GET /v1beta/models —— Gemini 格式的模型清单。
func (g *Gateway) handleGeminiModels(w http.ResponseWriter, r *http.Request) {
	agent := maskAgentID(clientAgent(r))
	hidden := g.hiddenModelsForAgent(agent)
	order := g.modelOrderForAgent(agent)
	avail := g.availableModelIDs()
	groupsAvail := g.availableGroups()
	redirectsAvail := g.availableRedirectNames()
	metas := make([]ir.ModelMeta, 0, len(g.models.KnownModels()))
	for _, m := range g.models.KnownModels() {
		if !g.modelVisible(m.ID) || hidden[m.ID] || !avail[m.ID] {
			continue
		}
		metas = append(metas, m)
	}
	sort.SliceStable(metas, func(i, j int) bool { return orderLess(order, metas[i].ID, metas[j].ID) })
	out := make([]map[string]any, 0, len(metas)+4)
	for _, m := range metas {
		out = append(out, g.geminiModelMeta(m))
	}
	groups := make([]string, 0, 8)
	for name := range g.models.VirtualGroups() {
		if !hidden[name] && groupsAvail[name] && !redirectsAvail[name] {
			groups = append(groups, name)
		}
	}
	sort.SliceStable(groups, func(i, j int) bool { return orderLess(order, groups[i], groups[j]) })
	for _, name := range groups {
		out = append(out, map[string]any{
			"name":                       "models/" + name,
			"displayName":                name,
			"supportedGenerationMethods": []string{"generateContent", "countTokens"},
		})
	}
	redirects := make([]string, 0, 8)
	for name := range redirectsAvail {
		if !hidden[name] {
			redirects = append(redirects, name)
		}
	}
	sort.SliceStable(redirects, func(i, j int) bool { return orderLess(order, redirects[i], redirects[j]) })
	for _, name := range redirects {
		out = append(out, map[string]any{
			"name":                       "models/" + name,
			"displayName":                name,
			"supportedGenerationMethods": []string{"generateContent", "countTokens"},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": out})
}

// geminiModelMeta 把 IR 模型元信息转成 Gemini 的 models 条目。
// 渠道级的每模型长度覆盖（高级 → Context window / Output）在这里生效：
// inputTokenLimit 取覆盖值，outputTokenLimit 有覆盖时才给出。
func (g *Gateway) geminiModelMeta(m ir.ModelMeta) map[string]any {
	in := m.ContextWindow
	out := 0
	if i := strings.Index(m.ID, "/"); i > 0 {
		cfg := g.lengthFor(m.ID[:i], m.ID[i+1:])
		if cfg.Context > 0 {
			in = cfg.Context
		}
		out = cfg.Output
	}
	meta := map[string]any{
		"name":                       "models/" + m.ID,
		"displayName":                m.DisplayName,
		"description":                m.DisplayName,
		"inputTokenLimit":            in,
		"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent", "countTokens"},
	}
	if out > 0 {
		meta["outputTokenLimit"] = out
	}
	if info := g.catalogInfo(m.ID); len(info.Efforts) > 0 {
		meta["reasoningEfforts"] = info.Efforts
	}
	return meta
}

// handleCountTokens 提供粗略 token 估算。
//
// Claude Code 会在发送前调用它做预算。精确分词对自用价值不大，
// 按"字节数 / 4"的近似即可，误差在可接受范围内。
func (g *Gateway) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))

	var wire struct {
		System   json.RawMessage `json:"system"`
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools json.RawMessage `json:"tools"`
	}
	_ = json.Unmarshal(body, &wire)

	total := len(body) / 4
	if total < 1 {
		total = 1
	}
	writeJSON(w, http.StatusOK, map[string]any{"input_tokens": total})
}

// handleGetResponse 返回保存的响应快照。
func (g *Gateway) handleGetResponse(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rec, ok := g.store.GetResponse(id)
	// Internal 快照是网关为了拼接 previous_response_id 自己记的，
	// 客户端说过 store:false，就不该能读回来。
	if !ok || rec.Internal {
		transducer.WriteError(w, ir.ProtocolOpenAIResponses,
			ir.NewAPIError(ir.ErrCodeBadRequest, "response "+id+" 不存在或已过期").WithStatus(http.StatusNotFound))
		return
	}
	output := rec.Output
	if output == nil {
		output = outputFromItems(rec.AccumulatedItems)
	}
	if output == nil {
		output = []any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":                   rec.ResponseID,
		"object":               "response",
		"created_at":           rec.CreatedAt,
		"status":               "completed",
		"model":                rec.Model,
		"previous_response_id": rec.PreviousID,
		"output":               output,
		"usage":                map[string]any{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0},
	})
}

// outputFromItems 在快照没有存 output 时（IR 路径），用累积的助手消息回放。
func outputFromItems(items []ir.UnifiedItem) []any {
	var out []any
	for _, it := range items {
		if it.Role != "assistant" || strings.TrimSpace(it.Text) == "" {
			continue
		}
		out = append(out, map[string]any{
			"type": "message", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": it.Text}},
		})
	}
	return out
}

// handleDeleteResponse 删除响应快照。
func (g *Gateway) handleDeleteResponse(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g.store.DeleteResponse(id)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "object": "response", "deleted": true})
}

// handleInputItems 返回该响应累积的上下文项。
func (g *Gateway) handleInputItems(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rec, ok := g.store.GetResponse(id)
	if !ok {
		transducer.WriteError(w, ir.ProtocolOpenAIResponses,
			ir.NewAPIError(ir.ErrCodeBadRequest, "response "+id+" 不存在或已过期").WithStatus(http.StatusNotFound))
		return
	}
	data := make([]any, 0, len(rec.AccumulatedItems))
	for _, it := range rec.AccumulatedItems {
		item := map[string]any{
			"id":   it.ID,
			"type": string(it.Type),
			"role": it.Role,
		}
		// 消息项带上正文：OpenAI 客户端读输入项时会取 content 里的文本。
		if strings.TrimSpace(it.Text) != "" {
			part := "input_text"
			if it.Role == "assistant" {
				part = "output_text"
			}
			item["content"] = []any{map[string]any{"type": part, "text": it.Text}}
		}
		data = append(data, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object":   "list",
		"data":     data,
		"has_more": false,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeBody 解析 JSON 请求体（上限 1MB）。
func decodeBody(r *http.Request, v any) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return io.ErrUnexpectedEOF
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return err
	}
	return nil
}
