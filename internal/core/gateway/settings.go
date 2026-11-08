package gateway

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/providers/workbuddy"
)

// 运行时设置 —— 让"隐私遮蔽开关 / 模型价格表"这类高频调整项
// 不必改 cockpit.json 再重启。
//
// 存储在 data/settings.json，与 cockpit.json 的关系：
// cockpit.json 是启动基线，settings.json 里的对应字段优先生效。
// 这样备份/迁移只需要带走 data/ 目录。

// ModelPrice 是模型单价，单位：美元 / 百万 token。
// 键支持精确模型 ID 与前缀（最长前缀命中），"*" 是全局兜底。
type ModelPrice struct {
	InputPerM      float64 `json:"input_per_m"`
	OutputPerM     float64 `json:"output_per_m"`
	CachedReadPerM float64 `json:"cached_read_per_m,omitempty"`
	// CachedWritePerM 是 prompt cache 写入单价（Anthropic 通常为 InputPerM 的 1.25 倍）。
	// 留空按 InputPerM 计。
	CachedWritePerM float64 `json:"cached_write_per_m,omitempty"`
	// Tiers 是上下文档位：输入 token 达到 MinInputTokens 后使用该档价格。
	Tiers []PriceTier `json:"tiers,omitempty"`
	// Peak 是高峰时段单价；基础字段表示平时价。仅 DeepSeek 等分峰谷的模型使用。
	Peak        *PriceRates   `json:"peak,omitempty"`
	PeakWindows []PriceWindow `json:"peak_windows,omitempty"`
	Timezone    string        `json:"timezone,omitempty"`
	Note        string        `json:"note,omitempty"`
}

// runtimeSettings 是 data/settings.json 的形态。
type runtimeSettings struct {
	Privacy PrivacyMaskConfig `json:"privacy_mask"`
	// FirstByteTimeoutSec 是"上游多久没吐第一个字节就换账号"的秒数。
	//
	// 为什么需要调：默认 60 秒对慢上游等于"卡满一分钟再报错"，用户体感就是
	// 请求断了；调小（15~25 秒）能让故障转移在用户失去耐心前发生。
	// 0 = 保持默认（60 秒）。
	FirstByteTimeoutSec int `json:"first_byte_timeout_sec,omitempty"`
	// MaxAttempts 是单次请求最多尝试几个账号（0 = 默认 5）。
	// 账号多、单号额度小的场景调大能显著减少"整体失败"。
	MaxAttempts int `json:"max_attempts,omitempty"`
	// APIKey 是网关鉴权密钥的持久副本。
	//
	// 之所以要存：密钥原本只能由启动参数给定，设置页写入的密钥没有落点，
	// 重启后消失、运行期也不生效。现在写在这里，loadSettings 时恢复到
	// GatewayKey，行为与启动参数一致。
	APIKey string `json:"api_key,omitempty"`
	// ListenAddr 是运行中切换后的监听地址（重启后优先于 cockpit.json 的 addr）。
	ListenAddr string `json:"listen_addr,omitempty"`
	// GlobalProxy 是运行中可切换的全局代理（http/https/socks5）。
	// 空串表示不使用全局代理；cockpit.json 的 global_proxy 是启动默认值。
	GlobalProxy string `json:"global_proxy,omitempty"`
	// ModelPrices 模型价格表，成本统计用。
	ModelPrices map[string]ModelPrice `json:"model_prices,omitempty"`
	// PriceCurrency 展示币种：USD / CNY；内部成本始终按美元存储。
	PriceCurrency string `json:"price_currency,omitempty"`
	// USDCNY 人民币展示汇率。
	USDCNY float64 `json:"usd_cny,omitempty"`
	// ModelGroupsOverride 覆盖 cockpit.json 里的 model_groups。
	// 值是原始 JSON：既兼容 ["a","b"] 数组，也兼容 {"chain":[...],"strategy":"rotate"} 对象。
	ModelGroupsOverride map[string]json.RawMessage `json:"model_groups_override,omitempty"`
	// RoutingGroupOrder 是路由组在页面/模型列表里的顺序。
	RoutingGroupOrder []string `json:"routing_group_order,omitempty"`
	// DeletedGroups 记录被删除的组名（含基线组），重建时不复活。
	DeletedGroups []string `json:"deleted_groups,omitempty"`
	// FoundGroups 控制是否自动发现"多个渠道都提供的同一模型"分组（nil=开）。
	FoundGroups *bool `json:"found_groups,omitempty"`
	// ProviderProxies 渠道级代理："" 跟随全局，"direct" 直连，其余是 URL。
	ProviderProxies map[string]string `json:"provider_proxies,omitempty"`
	// ProviderHeaders 渠道级自定义请求头（渠道编辑器 高级 → 请求头）。
	ProviderHeaders map[string]map[string]string `json:"provider_headers,omitempty"`
	// ProviderExtraBody 渠道级额外请求体字段（渠道编辑器 高级 → 额外请求体）。
	ProviderExtraBody map[string]json.RawMessage `json:"provider_extra_body,omitempty"`
	// ProviderSystemPrompt 渠道级默认系统提示词。
	ProviderSystemPrompt map[string]string `json:"provider_system_prompt,omitempty"`
	// ProviderSystemPromptConcat 是否把渠道提示词与用户提示词连接。
	ProviderSystemPromptConcat map[string]bool `json:"provider_system_prompt_concat,omitempty"`
	// RequestPresets 是可复用的请求预设（Body / Headers）。
	RequestPresets []requestPreset `json:"request_presets,omitempty"`
	// ProviderPresets 是渠道挂载的预设 ID 列表。
	ProviderPresets map[string][]string `json:"provider_presets,omitempty"`
	// RoutePresets 是路由组挂载的预设 ID 列表。
	RoutePresets map[string][]string `json:"route_presets,omitempty"`
	// ProviderPriceRate 渠道级计费倍率（渠道编辑器 高级 → Price rate）。
	ProviderPriceRate map[string]float64 `json:"provider_price_rate,omitempty"`
	// ProviderModels 是渠道级模型选择（上游模型名，不带渠道前缀）。
	// nil = 尚未配置（暴露全部）；空数组 = 一个都不暴露。
	ProviderModels map[string][]string `json:"provider_models,omitempty"`
	// ProviderModelMode 是渠道级模型目录模式：merge（默认）/ fallback / allowlist。
	// 只有非默认值才会落盘，见 model_mode.go。
	ProviderModelMode map[string]string `json:"provider_model_mode,omitempty"`
	// ModelRedirects 是「虚拟模型名 -> 渠道 -> 上游模型名」的重定向表。
	// 客户端只写虚拟名，路由层按渠道顺序生成候选链。见 model_redirect.go。
	ModelRedirects map[string]map[string]string `json:"model_redirects,omitempty"`
	// ProviderExtraModels 是用户手动补进渠道目录的模型（上游 /models 漏报、
	// 或厂商不实现 /models）。所有目录模式下都会追加。见 provider_extra_models.go。
	ProviderExtraModels map[string][]string `json:"provider_extra_models,omitempty"`
	// ProviderModelCredit 是渠道某模型的积分消耗倍率（仅用于展示折算）。
	ProviderModelCredit map[string]map[string]float64 `json:"provider_model_credit,omitempty"`
	// ProviderInsecureTLS 渠道级"跳过证书校验"（高级 → Don't check its certificate）。
	ProviderInsecureTLS map[string]bool `json:"provider_insecure_tls,omitempty"`
	// ProviderLengths 渠道级每模型长度覆盖（高级 → Context window / Output / Compaction）。
	ProviderLengths map[string]map[string]ModelLengthCfg `json:"provider_lengths,omitempty"`
	// ProviderPassUA 渠道级"透传 Agent 的 User-Agent"（高级 → Send the agent's own User-Agent）。
	ProviderPassUA map[string]bool `json:"provider_pass_ua,omitempty"`
	// ProviderUnlisted 渠道级"只在路由分组里使用"（高级 → Only through groups）。
	ProviderUnlisted map[string]bool `json:"provider_unlisted,omitempty"`
	// ProviderFallback 渠道级兜底模型链（渠道编辑器 高级 → Fallback）。
	ProviderFallback map[string][]string `json:"provider_fallback,omitempty"`
	// ProviderLimits 渠道级上限覆盖（渠道编辑器 高级 → Concurrency / RPM）。
	ProviderLimits map[string]ProviderLimitCfg `json:"provider_limits,omitempty"`
	// WorkBuddyIdentity 是 WorkBuddy 渠道的出网身份档位：cli / desktop / quiet。
	// 空值走默认 cli（双段 UA，与参考实现一致）；环境变量
	// AETHER_WORKBUDDY_IDENTITY 作为兜底。
	// 对应 providers/workbuddy/identity.go 的 IdentityProfile。
	WorkBuddyIdentity string `json:"workbuddy_identity,omitempty"`
	// ProviderRouting 渠道级路由设置（渠道编辑器 Routing / Rate limited / Stays）。
	// 键是渠道 id；值为空表示默认（smart / 保持原位 / auto）。
	ProviderRouting map[string]ProviderRouteCfg `json:"provider_routing,omitempty"`
	// AccountProxies 账号级代理，键为 providerID+"\x00"+账号名小写。
	AccountProxies map[string]string `json:"account_proxies,omitempty"`
	// LaunchCommands 各客户端的启动命令模板，支持 {MODEL}/{ORIGIN}/{API_KEY} 占位符。
	// 空条目回落到内置默认（claude/codex/gemini/opencode）。
	LaunchCommands map[string]string `json:"launch_commands,omitempty"`
	// WebDAV 备份云同步配置。Password 不回显到 GET /api/settings（打码）。
	WebDAV *WebDAVConfig `json:"webdav,omitempty"`
	// Profiles 配置档案：一套命名好的客户端组合，激活时逐个写盘。
	Profiles []Profile `json:"profiles,omitempty"`
	// ActiveProfile 最近一次成功激活的档案 ID。
	ActiveProfile string `json:"active_profile,omitempty"`
	// Tools 内置工具服务（网络搜索 / 图像生成）。
	Tools *ToolsConfig `json:"tools,omitempty"`
	// KeepAwake 阻止系统睡眠（Windows：SetThreadExecutionState）。
	KeepAwake bool `json:"keep_awake,omitempty"`
	// WSLDisabled 关掉 WSL 接入：不再枚举发行版、不 stat UNC、不生成 WSL 注入
	// 目标，界面也不显示 WSL 行。给"本机有 WSL 但不想让网关碰它"的场景。
	WSLDisabled bool `json:"wsl_disabled,omitempty"`
	// OTLP 导出配置。endpoint 非空即启用（如 http://127.0.0.1:4318）。
	// Headers 值里若含敏感令牌，GET /api/settings 回显时整个 Headers
	// 打码为 {"___masked___":true}。
	OTLP *OTLPConfig `json:"otlp,omitempty"`
	// Sync 是统一的云同步目标（WebDAV / S3 / GitHub）。WebDAV 仍保留
	// 旧字段兼容既有调用方；两者都为空表示未配置同步。
	Sync *SyncConfig `json:"sync,omitempty"`
	// LibraryProjects 是资源库的项目目录及其放置的技能/服务器。
	LibraryProjects []LibraryProject `json:"library_projects,omitempty"`
	// MonitorMinutes 是渠道监控自动探测间隔（分钟，0=关；最小 5）。
	// 自动探测会真实消耗账号额度，默认关闭。
	MonitorMinutes int `json:"monitor_minutes,omitempty"`
	// LAN 是局域网共享开关；LANAddr 可选（默认主端口 + 1）。
	LAN     bool   `json:"lan,omitempty"`
	LANAddr string `json:"lan_addr,omitempty"`
	// CORSOrigins 非空时只允许这些网页来源调用网关（空 = *）。
	CORSOrigins []string `json:"cors_origins,omitempty"`
	// AllowIPs 非空时远程请求只能来自这些 IP/CIDR（本机不受限）。
	AllowIPs []string `json:"allow_ips,omitempty"`
	// UI 保存前端通用偏好（主题/语言/字号/开关等），原样回显。
	UI map[string]any `json:"ui,omitempty"`
	// UpdateRepo 是检查 GitHub Releases 的仓库（owner/repo），空=默认仓库。
	UpdateRepo string `json:"update_repo,omitempty"`
	// UpdateToken 是私有仓库的访问令牌，GET /api/settings 回显时打码。
	UpdateToken string `json:"update_token,omitempty"`
	// DisabledProviders 是渠道页手动停用的渠道（不参与模型解析）。
	DisabledProviders []string `json:"disabled_providers,omitempty"`
	// AutoDisabledProviders 是"账号全部失效/耗尽后被自动下线"的渠道。
	// 与手动停用分开存：手动的不会被自动恢复，自动的会在账号恢复额度后
	// 由后台巡检清掉（见 provider_health.go）。
	AutoDisabledProviders map[string]AutoDisableState `json:"auto_disabled_providers,omitempty"`
	// QuotaAlerts 是额度提醒的去重状态（每个告警键记录上次的类型与时间）。
	// 落盘是为了重启后不把已经报过的提醒再刷一遍。
	QuotaAlerts map[string]quotaAlertState `json:"quota_alerts,omitempty"`
	// ChannelNet 是按渠道的慢链路优化档位（连接预热 / 在途上限）。
	// 未配置的渠道走内置默认（目前只有跨境/中转渠道默认开）。
	ChannelNet map[string]ChannelNetProfile `json:"channel_net,omitempty"`
	// EdgePins 是按域名固定的上游 IP（Cloudflare 边缘优选的结果）。
	// 只作用于网关内部，不修改系统 hosts。
	EdgePins map[string][]string `json:"edge_pins,omitempty"`
	// EdgeAuto 是"自动切换线路"的配置：定期测当前线路、扫候选、按阈值切换。
	// 与 EdgePins 配合使用（切换结果写进 EdgePins，跨重启保留）。
	EdgeAuto map[string]EdgeAutoConfig `json:"edge_auto,omitempty"`
	// CustomChannels 是用户自建的渠道（任意 OpenAI/Anthropic 兼容端点）。
	// 与内置渠道走同一套实现，只是配置来自这里而不是编译期常量。
	CustomChannels []CustomChannel `json:"custom_channels,omitempty"`
	// ResumeCommands 覆盖/扩展"会话在终端重开"的命令模板（agent → 命令）。
	//
	// 内置只收录了确认过命令形态的三个客户端；其余客户端各家的"续接上次会话"
	// 参数不统一，猜错会给用户一条跑不通的命令。让用户自己填模板，既覆盖了
	// 未知客户端，也不需要我们为每个 agent 发一次版。
	ResumeCommands map[string]string `json:"resume_commands,omitempty"`
	// ProviderOrder 是渠道页的展示/优先级顺序。
	ProviderOrder []string `json:"provider_order,omitempty"`
	// ProviderBalances 是渠道自定义余额接口（aether 的 balanceURL / balancePath）。
	// 用于没有用量 API 的中继：用户给一个 URL 与字段表达式，用量页如实显示余额。
	ProviderBalances map[string]ProviderBalanceConfig `json:"provider_balances,omitempty"`
	// RemoteQuotas 是其它 Aether 实例的用量源（aether 的远程额度）。
	RemoteQuotas []RemoteQuotaSource `json:"remote_quotas,omitempty"`
}

// RemoteQuotaSource 是一台远程 Aether 实例的用量读取配置。
type RemoteQuotaSource struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	KeyEnc  string `json:"key_enc,omitempty"`
	Enabled bool   `json:"enabled"`
}

// ProviderBalanceConfig 是一个渠道的余额读取配置。
// TokenEnc 是本机绑定加密的系统访问令牌，绝不明文落盘。
type ProviderBalanceConfig struct {
	URL      string            `json:"url,omitempty"`
	Path     string            `json:"path,omitempty"`
	Method   string            `json:"method,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	TokenEnc string            `json:"token_enc,omitempty"`
}

// SyncS3Config 是 S3 兼容对象存储（AWS / R2 / MinIO / B2）的同步目标。
type SyncS3Config struct {
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"` // 空 = AWS
	Region    string `json:"region,omitempty"`
	AccessKey string `json:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty"`
	PathStyle bool   `json:"path_style,omitempty"`
}

// SyncGitHubConfig 是 GitHub 仓库同步目标（Contents API）。
type SyncGitHubConfig struct {
	Repo   string `json:"repo"` // owner/repo
	Path   string `json:"path,omitempty"`
	Branch string `json:"branch,omitempty"`
	Token  string `json:"token,omitempty"`
}

// SyncConfig 是三选一的同步目标，外加可选的口令密封。
type SyncConfig struct {
	Kind   string            `json:"kind"` // webdav | s3 | github
	WebDAV *WebDAVConfig     `json:"webdav,omitempty"`
	S3     *SyncS3Config     `json:"s3,omitempty"`
	GitHub *SyncGitHubConfig `json:"github,omitempty"`
	// PassphraseEnc 是口令的 simplecrypt 密文（本机绑定）。
	// 有口令时远端文件是 AES-GCM 密封的，换机还原需要同一个口令。
	PassphraseEnc   string `json:"passphrase_enc,omitempty"`
	AutoPushMinutes int    `json:"auto_push_minutes,omitempty"`
	LastAt          int64  `json:"last_at,omitempty"`
	LastRemote      string `json:"last_remote,omitempty"`
	LastError       string `json:"last_error,omitempty"`
}

// WebDAVConfig 是配置备份的 WebDAV 云同步目标（纯标准库客户端，见 webdav.go）。
type WebDAVConfig struct {
	URL      string `json:"url"`                // 例如 https://dav.jianguoyun.com/dav/backup/
	Username string `json:"username,omitempty"` // 用户名
	Password string `json:"password,omitempty"` // 应用密码（坚果云等用独立应用密码）
	// Filename 远端文件名，默认 aether-backup.json。
	Filename string `json:"filename,omitempty"`
	// AutoPushMinutes >0 时周期自动推送（分钟）；0 关闭。
	AutoPushMinutes int `json:"auto_push_minutes,omitempty"`
}

// OTLP 导出的默认参数（对齐 aether internal/otlp：批 32 条或 5 秒，队列 128 条）。
const (
	defaultOTLPBatchSize      = 32
	defaultOTLPFlushIntervalS = 5
	defaultOTLPQueueMax       = 128
)

// OTLPConfig 是请求审计记录的 OTLP/JSON 导出配置（纯标准库客户端，见 otlp.go）。
type OTLPConfig struct {
	// Endpoint 基地址，不带 /v1/traces；非空即启用（如 http://127.0.0.1:4318）。
	Endpoint string `json:"endpoint,omitempty"`
	// Headers 附加请求头（鉴权头等）。GET 回显时整体打码为 {"___masked___":true}。
	Headers map[string]string `json:"headers,omitempty"`
	// BatchSize 攒批条数，默认 32。
	BatchSize int `json:"batch_size,omitempty"`
	// FlushIntervalS 定时 flush 周期（秒），默认 5。
	FlushIntervalS int `json:"flush_interval_seconds,omitempty"`
	// QueueMax 队列上限（条），默认 128；满则丢弃最旧。
	QueueMax int `json:"queue_max,omitempty"`
}

// webdavPasswordMask 是 GET /api/settings 响应里替代真实密码的占位符。
const webdavPasswordMask = "******"

// otlpHeadersMaskedKey 是 OTLP Headers 打码形态的唯一键：
// GET 回显时非空 Headers 被替换为 {"___masked___":"true"}。
const otlpHeadersMaskedKey = "___masked___"

// maskWebDAVPassword 返回 WebDAV 密码打码后的设置副本（不改到真实值）。
func maskWebDAVPassword(s runtimeSettings) runtimeSettings {
	if s.WebDAV == nil {
		return s
	}
	cp := s
	wc := *s.WebDAV
	if wc.Password != "" {
		wc.Password = webdavPasswordMask
	}
	cp.WebDAV = &wc
	return cp
}

// maskOTLPHeaders 返回 OTLP Headers 打码后的设置副本（不改真实值）。
// Headers 值常含鉴权令牌，与 WebDAV 密码同等对待：只要非空就整体替换为
// 打码形态，避免逐键判断漏掉自定义头里的秘密。
func maskOTLPHeaders(s runtimeSettings) runtimeSettings {
	if s.OTLP == nil {
		return s
	}
	cp := s
	oc := *s.OTLP
	if len(oc.Headers) > 0 {
		oc.Headers = map[string]string{otlpHeadersMaskedKey: "true"}
	}
	cp.OTLP = &oc
	return cp
}

// maskSettings 汇总所有出站打码。GET /api/settings 与设置更新响应共用——
// 后者同样不能把刚提交的明文原样回显。
func maskSettings(s runtimeSettings) runtimeSettings {
	return maskSyncSecrets(maskTools(maskOTLPHeaders(maskWebDAVPassword(maskRemoteQuotaSecrets(maskProviderBalanceSecrets(maskProxySecrets(maskRequestPresetSecrets(maskUpdateToken(s)))))))))
}

// maskUpdateToken 从 GET 回显里去掉 GitHub 访问令牌。
func maskUpdateToken(s runtimeSettings) runtimeSettings {
	if s.UpdateToken != "" {
		s.UpdateToken = webdavPasswordMask
	}
	return s
}

// maskRemoteQuotaSecrets 从 GET 回显里去掉远程实例的访问密钥。
func maskRemoteQuotaSecrets(s runtimeSettings) runtimeSettings {
	if len(s.RemoteQuotas) == 0 {
		return s
	}
	cp := make([]RemoteQuotaSource, len(s.RemoteQuotas))
	for i, src := range s.RemoteQuotas {
		src.KeyEnc = ""
		cp[i] = src
	}
	s.RemoteQuotas = cp
	return s
}

// maskProxySecrets 给全局/渠道/账号代理里的密码打码：
// GET /api/settings 会原样序列化 runtimeSettings，代理 URL 常带 user:pass。
func maskProxySecrets(s runtimeSettings) runtimeSettings {
	if s.GlobalProxy != "" {
		s.GlobalProxy = maskProxyURL(s.GlobalProxy)
	}
	if len(s.ProviderProxies) > 0 {
		cp := make(map[string]string, len(s.ProviderProxies))
		for k, v := range s.ProviderProxies {
			cp[k] = maskProxyURL(v)
		}
		s.ProviderProxies = cp
	}
	if len(s.AccountProxies) > 0 {
		cp := make(map[string]string, len(s.AccountProxies))
		for k, v := range s.AccountProxies {
			cp[k] = maskProxyURL(v)
		}
		s.AccountProxies = cp
	}
	return s
}

// maskProviderBalanceSecrets 把渠道余额令牌从 GET 回显里去掉：
// "已保存"状态走 /api/providers 的 balanceToken.set，不回传密文。
func maskProviderBalanceSecrets(s runtimeSettings) runtimeSettings {
	if len(s.ProviderBalances) == 0 {
		return s
	}
	cp := make(map[string]ProviderBalanceConfig, len(s.ProviderBalances))
	for k, v := range s.ProviderBalances {
		v.TokenEnc = ""
		cp[k] = v
	}
	s.ProviderBalances = cp
	return s
}

// maskSyncSecrets 移除同步目标的密钥与口令：GET 只告诉前端"设置了"，
// 绝不回显 Secret / token / 口令密文。
func maskSyncSecrets(s runtimeSettings) runtimeSettings {
	if s.Sync == nil {
		return s
	}
	cp := *s.Sync
	cp.PassphraseEnc = ""
	if cp.S3 != nil {
		s3 := *cp.S3
		if s3.SecretKey != "" {
			s3.SecretKey = webdavPasswordMask
		}
		cp.S3 = &s3
	}
	if cp.GitHub != nil {
		gh := *cp.GitHub
		if gh.Token != "" {
			gh.Token = webdavPasswordMask
		}
		cp.GitHub = &gh
	}
	if cp.WebDAV != nil {
		wd := *cp.WebDAV
		if wd.Password != "" {
			wd.Password = webdavPasswordMask
		}
		cp.WebDAV = &wd
	}
	s.Sync = &cp
	return s
}

// isMaskedOTLPHeaders 判断 Headers 是否为 GET 回显的打码形态。
func isMaskedOTLPHeaders(h map[string]string) bool {
	if len(h) != 1 {
		return false
	}
	_, ok := h[otlpHeadersMaskedKey]
	return ok
}

// defaultSettingsPath 默认落盘位置（相对工作目录，与其它 data/ 文件一致）。

// loadSettings 从磁盘加载设置。文件不存在或损坏时返回空配置——
// 损坏的设置文件不应阻止网关启动。
func (g *Gateway) loadSettings() {
	b, err := os.ReadFile(g.settingsPath)
	if err != nil {
		return
	}
	var s runtimeSettings
	if err := json.Unmarshal(b, &s); err != nil {
		log.Printf("[WARN] 设置文件解析失败（忽略）: %v", err)
		return
	}
	g.settings = s
	// 持久化的密钥优先于启动参数：用户在设置页改过的应当生效。
	if key := strings.TrimSpace(s.APIKey); key != "" {
		g.SetGatewayKey(key)
	}
	g.applyWorkBuddyIdentity()
	g.applyFailoverSettings()
}

// applyFailoverSettings 把"故障转移"相关设置应用到运行中的网关。
//
// 这两个值直接决定用户体感：首字节超时越长，"卡住"越久；重试次数越少，
// "一个号没额度导致整体失败"的概率越高。以前它们只能编译期写死。
func (g *Gateway) applyFailoverSettings() {
	if sec := g.settings.FirstByteTimeoutSec; sec >= 5 && sec <= 300 {
		g.cfg.FirstByteTimeout = time.Duration(sec) * time.Second
	}
	if n := g.settings.MaxAttempts; n >= 1 && n <= 20 {
		g.cfg.MaxAttempts = n
	}
}

// applyWorkBuddyIdentity 把 settings.json 的 workbuddy_identity 应用到
// WorkBuddy 渠道（cli / desktop / quiet）。空值不覆盖环境变量与默认值。
func (g *Gateway) applyWorkBuddyIdentity() {
	v := strings.TrimSpace(g.settings.WorkBuddyIdentity)
	if v == "" {
		return
	}
	if err := workbuddy.SetIdentityPreset(v); err != nil {
		log.Printf("[WARN] %v", err)
	}
}

// saveSettings 原子写盘。调用方持有 settingsMu。
func (g *Gateway) saveSettings() error {
	path := g.settingsPath
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(g.settings, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// rebuildPrivacyEngine 按当前设置重建遮蔽引擎。
func (g *Gateway) rebuildPrivacyEngine() {
	if !g.settings.Privacy.Enabled {
		g.privacy.Store(nil)
		g.privacyErr.Store(nil)
		return
	}
	cfg := g.settings.Privacy
	if len(cfg.Rules) == 0 {
		// 开了开关但没选规则：应用默认集，避免"看起来开着其实什么都不遮"。
		cfg.Rules = append([]string{}, DefaultPrivacyRules...)
		g.settings.Privacy.Rules = cfg.Rules
	}
	if old := g.currentPrivacyEngine(); old != nil && old.sidecar != nil {
		old.sidecar.Close()
	}
	e, err := newPrivacyEngine(cfg)
	if err != nil {
		// 不静默降级：engine 保持 nil，requests 由 privacyState() 判定为
		// "要求遮蔽但不可用"并直接 503，绝不退化成明文发送。
		msg := err.Error()
		log.Printf("[ERROR] 隐私遮蔽引擎初始化失败，已拒绝发送: %v", err)
		g.privacyErr.Store(&msg)
		g.privacy.Store(nil)
		return
	}
	g.privacyErr.Store(nil)
	g.privacy.Store(e)
}

// currentPrivacyEngine 返回当前遮蔽引擎（nil = 未启用或初始化失败）。
func (g *Gateway) currentPrivacyEngine() *privacyEngine { return g.privacy.Load() }

// privacyState 返回 (是否要求遮蔽, 最近一次引擎重建失败的原因)。
// 要求遮蔽但引擎为 nil 时，调用方必须 fail-closed。
func (g *Gateway) privacyState() (bool, string) {
	g.settingsMu.Lock()
	required := g.settings.Privacy.Enabled
	g.settingsMu.Unlock()
	if !required {
		return false, ""
	}
	if p := g.privacyErr.Load(); p != nil {
		return true, *p
	}
	return true, ""
}

// handleSettings GET /api/settings
// WebDAV 密码与 OTLP Headers 打码后返回，明文只存本机 settings.json。
func (g *Gateway) handleSettings(w http.ResponseWriter, r *http.Request) {
	// settingsView 自己加锁：这里不能先持有 settingsMu（会自锁）。
	// aether 设置页把响应当作 prefs 本体读取（theme/lan/...），
	// 而旧接口调用方读 .settings —— 顶层给扁平字段，同时附一份
	// .settings 别名，两种形状都成立。
	flat := g.settingsView()
	flat["settings"] = g.settingsView()
	writeJSON(w, http.StatusOK, flat)
}

// handleUpdateSettings POST /api/settings
//
// 请求体（字段均可选，只更新出现的字段）：
//
//	{"privacy_mask": {"enabled": true, "rules": ["keys","jwt"]},
//	 "model_prices": {"claude-*": {"input_per_m": 3, "output_per_m": 15}}}
//
// webdav.password 收到打码占位符（"******"）或空串时保留旧密码，
// 避免前端回显打码值把真密码覆盖掉。
// otlp.headers 同理：收到打码形态（{"___masked___":...}）或未携带该键时
// 沿用旧 headers；显式给空对象 {} 才清空。
//
// OTLP 字段更新成功后会作废导出器的未发送积压（见 otlp.go 的 invalidate），
// 保证绝不把旧批次发给新目的地或新凭据。
func (g *Gateway) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Privacy        *PrivacyMaskConfig     `json:"privacy_mask"`
		ModelPrices    *map[string]ModelPrice `json:"model_prices"`
		LaunchCommands *map[string]string     `json:"launch_commands"`
		WebDAV         *WebDAVConfig          `json:"webdav"`
		// OTLP 用 RawMessage 收下：需要区分"headers 键未出现"（沿用旧值）
		// 与"显式提交 headers"（按打码/明文分支处理）。
		OTLP  *json.RawMessage `json:"otlp"`
		Tools *json.RawMessage `json:"tools"`
		// KeepAwake 开启/关闭“阻止系统睡眠”。
		KeepAwake *bool `json:"keep_awake"`
		// WSLDisabled 开启/关闭 WSL 接入（关掉后不枚举发行版）。
		WSLDisabled *bool `json:"wsl_disabled"`
		// FirstByteTimeoutSec / MaxAttempts：故障转移的两个关键旋钮。
		FirstByteTimeoutSec *int `json:"first_byte_timeout_sec"`
		MaxAttempts         *int `json:"max_attempts"`
		// APIKey 是网关鉴权密钥。过去这个字段没有对应分支，前端提交的密钥
		// 只会落进 settings.ui（永不生效的副本），于是"填了密钥仍然被拒"。
		APIKey *string `json:"api_key"`
		// LAN 是局域网开关。它同样被 reservedSettingKeys 拦住（不进 settings.ui），
		// 又一直没有对应分支 → 设置页那个开关点了等于没点。
		LAN *bool `json:"lan"`
		// Proxy 在运行中切换全局代理（http/https/socks5），空串关闭。
		Proxy *string `json:"proxy"`
		// Port 让设置页整份保存 prefs 时也能切换端口（幂等）。
		Port *json.RawMessage `json:"port"`
		// GitHub 更新源：repo 留空=默认仓库；token 打码回显，收到打码值时保留旧值。
		UpdateRepo  *string `json:"update_repo"`
		UpdateToken *string `json:"update_token"`
		// WorkBuddy 出网身份档位（cli / desktop / quiet / browser）。**只落盘**：
		// 运行中的渠道要重启网关后由 loadSettings 重新应用，见 workbuddy_identity_api.go。
		WorkBuddyIdentity *string `json:"workbuddy_identity"`
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "读取请求体失败"})
		return
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
			return
		}
	}

	// 锁外先校验 OTLP endpoint（非法直接 400，不影响其它字段的更新）。
	if body.OTLP != nil {
		var probe OTLPConfig
		if err := json.Unmarshal(*body.OTLP, &probe); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "OTLP 配置解析失败: " + err.Error()})
			return
		}
		if ep := strings.TrimSpace(probe.Endpoint); ep != "" {
			if _, err := otlpTracesURL(ep); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
		}
	}
	if body.UpdateRepo != nil {
		repo := strings.TrimSpace(*body.UpdateRepo)
		if repo != "" && !validGitHubRepo(repo) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "GitHub 仓库格式应为 owner/repo"})
			return
		}
	}
	// 出网身份档位先做白名单校验（锁外），非法值直接 400，不落盘任何东西。
	workBuddyPreset := ""
	if body.WorkBuddyIdentity != nil {
		preset, ok := normalizeWorkBuddyIdentity(*body.WorkBuddyIdentity)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": "未知 WorkBuddy 身份档位: " + strings.TrimSpace(*body.WorkBuddyIdentity) +
					"（可选 cli / desktop / quiet / browser）",
			})
			return
		}
		workBuddyPreset = preset
	}
	if body.KeepAwake != nil && *body.KeepAwake && !g.platform.KeepAwake {
		// 手机没有同义的"阻止 PC 睡眠"，不落盘一个永远不生效的开关。
		writePlatformUnsupported(w, "保持唤醒")
		return
	}

	otlpChanged := false
	g.settingsMu.Lock()
	if body.Privacy != nil {
		g.settings.Privacy = *body.Privacy
	}
	if body.FirstByteTimeoutSec != nil {
		if v := *body.FirstByteTimeoutSec; v < 5 || v > 300 {
			g.settingsMu.Unlock()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "首字节超时应在 5~300 秒之间"})
			return
		}
		g.settings.FirstByteTimeoutSec = *body.FirstByteTimeoutSec
	}
	if body.MaxAttempts != nil {
		if v := *body.MaxAttempts; v < 1 || v > 20 {
			g.settingsMu.Unlock()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "重试次数应在 1~20 之间"})
			return
		}
		g.settings.MaxAttempts = *body.MaxAttempts
	}
	if body.APIKey != nil {
		// 空串 = 关闭鉴权（仅本机监听时的合理默认）；否则立即生效，不用重启。
		key := strings.TrimSpace(*body.APIKey)
		// 手填的密钥太短等于没有：拦在这里，别等被人扫端口才发现。
		// 生成接口产出固定 sk-aether-<32hex>，不受这条限制。
		if key != "" && !gatewayKeyPlausible(key) {
			g.settingsMu.Unlock()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "网关密钥至少 16 个字符（可用「生成」自动创建）"})
			return
		}
		g.SetGatewayKey(key)
		g.settings.APIKey = key
	}
	if body.LAN != nil {
		// 走 SetLAN 真正改监听；被拒（没密钥）时原样返回错误，让前端能提示。
		if err := g.SetLAN(*body.LAN); err != nil {
			g.settingsMu.Unlock()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		g.settings.LAN = *body.LAN
	}
	if body.ModelPrices != nil {
		g.settings.ModelPrices = *body.ModelPrices
	}
	if body.LaunchCommands != nil {
		g.settings.LaunchCommands = *body.LaunchCommands
	}
	if body.WebDAV != nil {
		g.applyWebDAVConfig(body.WebDAV)
	}
	if body.Tools != nil {
		var tc ToolsConfig
		if err := json.Unmarshal(*body.Tools, &tc); err == nil {
			g.applyToolsConfig(&tc)
		}
	}
	if body.OTLP != nil {
		g.applyOTLPConfig(*body.OTLP)
		otlpChanged = true
	}
	keepChanged, keepOn := false, false
	if body.KeepAwake != nil {
		g.settings.KeepAwake = *body.KeepAwake
		keepChanged, keepOn = true, *body.KeepAwake
	}
	wslChanged := false
	if body.WSLDisabled != nil {
		g.settings.WSLDisabled = *body.WSLDisabled
		wslChanged = true
	}
	if body.UpdateRepo != nil {
		g.settings.UpdateRepo = strings.TrimSpace(*body.UpdateRepo)
	}
	if body.UpdateToken != nil {
		token := strings.TrimSpace(*body.UpdateToken)
		switch token {
		case "", webdavPasswordMask:
			// 空/打码值 = 不改变已保存令牌。
		case "-":
			g.settings.UpdateToken = ""
		default:
			g.settings.UpdateToken = token
		}
	}
	// 只落盘，不调用 workbuddy.SetIdentityPreset：切换档位要重启网关后
	// 由 loadSettings → applyWorkBuddyIdentity 重新应用（页面文案同口径）。
	if body.WorkBuddyIdentity != nil {
		g.settings.WorkBuddyIdentity = workBuddyPreset
	}
	g.applyUIPrefs(raw)
	saveErr := g.saveSettings()
	g.rebuildPrivacyEngine()
	groups := g.models.VirtualGroupDetails()
	g.settingsMu.Unlock()
	if keepChanged && g.platform.KeepAwake {
		if err := g.applyKeepAwake(keepOn); err != nil {
			log.Printf("[WARN] 设置保持唤醒失败: %v", err)
		} else {
			g.keepAwakeOn.Store(keepOn)
		}
	}
	if wslChanged {
		// 开关一变：会话扫描器停止/恢复枚举 WSL，注入目标里也立刻加上/去掉
		// WSL 行（检测本身有缓存）。
		g.syncWSLEnabled()
		if g.clientMgr != nil {
			g.clientMgr.SetTargets(g.wslTargets())
		}
	}
	g.syncRedactPrefs()
	// 插件钩子同样是"设置驱动"的：保存后立刻重建，不用重启。
	g.syncHooks()
	// 渠道网络档位与边缘固定表同样跟着设置走。
	g.syncChannelNet()
	// 故障转移旋钮（首字节超时 / 重试次数）立即生效，不必重启。
	g.applyFailoverSettings()
	if body.Proxy != nil {
		if _, err := g.applyGlobalProxy(*body.Proxy); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	if body.Port != nil {
		var v any
		_ = json.Unmarshal(*body.Port, &v)
		var text string
		switch x := v.(type) {
		case float64:
			text = strconv.Itoa(int(x))
		case string:
			text = x
		}
		if text != "" {
			if _, err := g.applyPort(text); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
		}
	}

	// 配置变更钩子：作废积压并停 worker，下次入队用新配置快照重启。
	// 即使落盘失败也要作废——内存里的配置已经变了。
	if otlpChanged {
		g.invalidateOTLP()
	}

	if saveErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存设置失败: " + saveErr.Error()})
		return
	}
	eventbus.Emit(eventbus.TopicSpec+".updated", map[string]any{"source": "settings"})
	// 前端把响应当 prefs 本体读取：返回扁平设置（含 UI 偏好与运行时 lan 字段）。
	view := g.settingsView()
	view["model_groups"] = groups
	writeJSON(w, http.StatusOK, view)
}

// applyWebDAVConfig 更新 WebDAV 配置。密码为空或打码占位符时沿用旧密码。
// 调用方持有 settingsMu。
func (g *Gateway) applyWebDAVConfig(in *WebDAVConfig) {
	if in.Password == "" || in.Password == webdavPasswordMask {
		if g.settings.WebDAV != nil {
			in.Password = g.settings.WebDAV.Password
		}
	}
	if g.settings.WebDAV == nil {
		g.settings.WebDAV = &WebDAVConfig{}
	}
	*g.settings.WebDAV = *in
	if g.settings.Sync != nil && g.settings.Sync.Kind == "webdav" {
		wd := *in
		g.settings.Sync.WebDAV = &wd
	}
}

// applyOTLPConfig 应用 OTLP 配置更新。headers 的沿用规则（对齐 WebDAV 密码
// 先例，测试钉死）：
//   - 请求体未携带 headers 键：沿用旧 headers（部分更新语义）；
//   - headers 为打码形态 {"___masked___":...}：沿用旧 headers；
//   - 其余（含显式空对象 {}）：按提交值替换。
//
// 调用方持有 settingsMu。
func (g *Gateway) applyOTLPConfig(raw json.RawMessage) {
	var in OTLPConfig
	if err := json.Unmarshal(raw, &in); err != nil {
		return // 解析失败已在锁外拦截并返回 400；防御性忽略
	}
	var probe map[string]json.RawMessage
	_ = json.Unmarshal(raw, &probe)
	_, headersPresent := probe["headers"]
	if g.settings.OTLP != nil && (!headersPresent || isMaskedOTLPHeaders(in.Headers)) {
		in.Headers = g.settings.OTLP.Headers
	}
	in.Endpoint = strings.TrimSpace(in.Endpoint)
	if g.settings.OTLP == nil {
		g.settings.OTLP = &OTLPConfig{}
	}
	*g.settings.OTLP = in
}
