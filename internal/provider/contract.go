package provider

import (
	"context"
	"net/http"
	"time"

	"aether-cockpit/internal/core/ir"
)

// BaseProvider 是所有渠道必须实现的最小契约：只有 Manifest 一个方法。
//
// 刻意保持极小，避免"大而全接口导致大量空实现"。
// 其余功能通过下面的能力接口按需实现，内核用类型断言动态探测。
type BaseProvider interface {
	Manifest() ProviderManifest
}

// ChatCapability [能力A] 流式对话反代。
//
// 实现约定：
//   - 必须返回带缓冲的 channel，并在 goroutine 内 defer close(out)。
//   - 只允许产出 ir 定义的那几种极简事件，协议差异由出站 transducer 负责。
//   - ctx 取消时必须尽快退出，不得泄漏 goroutine。
type ChatCapability interface {
	ChatStream(ctx context.Context, env *AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error)
}

// LocalImportCapability [能力B] 本机 IDE 登录态嗅探。
type LocalImportCapability interface {
	DiscoverLocalAccounts(ctx context.Context) ([]*CredentialDraft, error)
}

// LocalInjectCapability [能力B+] 一键切号：把账号登录态写回本机 IDE。
//
// 与 LocalImportCapability 互逆。实现方负责把 env.Credentials 转换成
// IDE 认得的落盘格式（凭据管理器条目或本地 token 文件），返回值只描述
// 写到了哪里，绝不回传凭证内容。
type LocalInjectCapability interface {
	InjectLocalAccount(ctx context.Context, env *AccountEnv) (*InjectResult, error)
}

// InjectResult 描述一次注入的落点（不含任何凭证）。
type InjectResult struct {
	// Target 主落点，如 "windows-credential:gemini:antigravity"。
	Target string `json:"target"`
	// Files 同时写出的回退文件路径（已做 ~ 折叠）。
	Files []string `json:"files,omitempty"`
	// Warnings 非致命问题，例如凭据管理器写入失败但回退文件成功。
	Warnings []string `json:"warnings,omitempty"`
	// RestartHint 提示用户何时需要重启 IDE 才生效。
	RestartHint string `json:"restart_hint,omitempty"`
}

// OfficialBinding 是官方订阅账号的绑定信息（只含可展示字段）。
type OfficialBinding struct {
	Official  bool   `json:"official"`
	AccountID string `json:"account_id,omitempty"`
	Email     string `json:"email,omitempty"`
	Plan      string `json:"plan,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
}

// OfficialBindingCapability 让渠道从凭证里解析出官方绑定信息（账号/邮箱/套餐）。
type OfficialBindingCapability interface {
	OfficialBinding(credentials map[string]string) *OfficialBinding
}

// OfficialAccountCapability 标记渠道能区分"官方订阅账号"与"第三方中转账号"。
//
// 渠道 Manifest 里声明 Capabilities.OfficialFirst=true 时，选号会先把官方
// 账号排在前面（显式 priority 仍然优先）；官方账号全部不可用时才落到中转。
type OfficialAccountCapability interface {
	IsOfficialAccount(credentials map[string]string) bool
}

// TokenRefreshCapability [能力C] 凭证保活与刷新。
type TokenRefreshCapability interface {
	RefreshCredential(ctx context.Context, env *AccountEnv) (*CredentialDraft, error)
}

// CheckInCapability [能力D] 签到与积分任务。
type CheckInCapability interface {
	ExecuteTasks(ctx context.Context, env *AccountEnv, taskIDs []string) (*CheckInReport, error)
}

// QuotaCapability [能力E] 配额与积分查询。
type QuotaCapability interface {
	QueryQuota(ctx context.Context, env *AccountEnv) (*QuotaStatus, error)
}

// ModelLister [能力] 从上游拉取"今天实际可用"的模型清单
// （OpenAI 兼容 /models 一类）。网关用它做实时目录，失败时退回静态清单。
type ModelLister interface {
	ListModels(ctx context.Context, env *AccountEnv) ([]string, error)
}

// ModelListSupport 声明 ListModels 在给定账号上是否有可用端点。
// 没有模型列表接口的渠道（私有协议 / 本地无此路由）不该被自动刷新。
type ModelListSupport interface {
	SupportsModelList(env *AccountEnv) bool
}

// ModelInfo 是上游模型目录里的一条：对外 ID 与上游标识可以不同
// （Qoder 的对外名是显示名，上游聊天必须用内部 key）。
type ModelInfo struct {
	ID            string
	UpstreamModel string
	Name          string
	Context       int
	Capabilities  ir.ModelCapabilities
	Efforts       []string
	// Rate 是上游声明的积分倍率（Qoder 的 price_factor），0 表示未声明/免费。
	Rate float64
	Free bool
}

// RichModelLister 返回带元数据的模型目录；目录层优先用它。
type RichModelLister interface {
	ListModelInfos(ctx context.Context, env *AccountEnv) ([]ModelInfo, error)
}

// ResponsesWSCapability 可选：用上游 WebSocket 跑一轮流式 Responses，
// 事件以 SSE 写给下游（Codex 订阅的 chatgpt.com 后端）。
// 返回 started=true 表示已经开始写响应，调用方不能再降级 HTTP。
type ResponsesWSCapability interface {
	ResponsesWebSocketStream(ctx context.Context, env *AccountEnv, rawReq []byte, upstreamModel string, w http.ResponseWriter) (started bool, err error)
}

// NativeResponsesCapability [能力F] 原生 OpenAI Responses 协议直通。
//
// 存在的意义：当下游客户端（Codex CLI）和目标上游都是 /v1/responses 时，
// 走 IR 转换要经历"解析 → 重建 → 再序列化"，不仅多一次 CPU 开销，
// 还会丢掉 OpenAI 后续新增的实验性字段（encrypted_content、内置工具、
// 未来的 reasoning 变体）。直通把整条链路压成"改 Auth 头 + 换 model"。
//
// 实现约定：
//   - 只需替换 Authorization 与 model 字段，其余请求体字节保持原样；
//   - 返回的 *http.Response 由内核负责转发与关闭，实现方不要读取 Body。
type NativeResponsesCapability interface {
	ResponsesPassthrough(ctx context.Context, env *AccountEnv, rawReq []byte, upstreamModel string) (*http.Response, error)
}

// CredentialDraft 一次登录/导入产出的凭证草稿。
type CredentialDraft struct {
	// Label 用于 UI 展示，如邮箱或手机号后四位。
	Label       string            `json:"label"`
	Credentials map[string]string `json:"credentials"`
	// Fingerprint 若导入时能拿到真实设备指纹则填充，否则由内核生成并持久化。
	Fingerprint *DeviceFingerprint `json:"fingerprint,omitempty"`
	// ExpiresAt Unix 秒，0 表示未知。
	ExpiresAt int64 `json:"expires_at,omitempty"`
	// Source 记录来源，如 "local_import:/Users/x/.trae/config.json"。
	Source string `json:"source,omitempty"`
}

// TaskResult 单个任务的执行结果。
type TaskResult struct {
	TaskID  string `json:"task_id"`
	Title   string `json:"title"`
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Points  int    `json:"points,omitempty"`
	// Skipped 表示今日已完成或条件不满足，不算失败。
	Skipped bool `json:"skipped,omitempty"`
	// LatencyMS 便于排查反风控抖动是否生效。
	LatencyMS int64 `json:"latency_ms,omitempty"`
}

// CheckInReport 一次签到的汇总。
type CheckInReport struct {
	Success      bool         `json:"success"`
	EarnedPoints int          `json:"earned_points"`
	Message      string       `json:"message,omitempty"`
	Results      []TaskResult `json:"results,omitempty"`
	// Outcome 是签到结论（Checkin* 常量），供用量卡片如实展示
	// "已签到 / 今天还没有活动 / 需要去官方 App"等，而不只是成功/失败。
	Outcome string `json:"outcome,omitempty"`
	// Credit / Streak 是本次领到的积分与连续天数（上游给了才有）。
	Credit float64 `json:"credit,omitempty"`
	Streak int     `json:"streak,omitempty"`
	// Day 是签到归属日（北京时区 "2006-01-02"），前端按它判断"今天"。
	Day string `json:"day,omitempty"`
}

// 签到结论。与 aether 的 WorkBuddyCheckin.Outcome 对齐，前端直接消费。
const (
	CheckinClaimed    = "claimed"    // 本次领到
	CheckinDone       = "done"       // 之前已领/上游说今天已签
	CheckinIneligible = "ineligible" // 账号不满足条件
	CheckinInactive   = "inactive"   // 当前没有签到活动
	CheckinFailed     = "failed"     // 没有结论（网络/凭证/未知错误），当天稍后重试
	CheckinOwnApp     = "own-app"    // 只能在官方 App 内签到
)

// beijing 是签到归属日使用的时区（UTC+8 常年不变）。
// 用 FixedZone 而不是 LoadLocation：Windows 上没有 Go 能直接读的时区库，
// 而厂商的"一天"都按北京时间切。
var beijing = time.FixedZone("CST", 8*60*60)

// CheckinDay 返回 t 所属的北京日（"2006-01-02"）。
func CheckinDay(t time.Time) string { return t.In(beijing).Format("2006-01-02") }

// QuotaWindow 是订阅窗口（5 小时 / 每周等）的用量读数。
//
// Used 用百分比而不是绝对值：上游各家给的口径不同（有的只给 utilization），
// 而 UI 的进度条与"还剩多少"换算都以百分比为唯一基准。Limit/Amount 只在
// 上游确实给出绝对值时填充，供卡片显示 "1,234 / 10,000 次"。
type QuotaWindow struct {
	Name   string  `json:"name"`
	Used   float64 `json:"used"`             // 0-100
	Amount int64   `json:"amount,omitempty"` // 已用绝对值
	Limit  int64   `json:"limit,omitempty"`  // 窗口总量
	Unit   string  `json:"unit,omitempty"`   // 次 / tokens / 积分
	// Aside 表示"模型组自己的窗口"（如 M Plan 按模型组的额度），
	// 卡片上与主额度分开展示。
	Aside    bool  `json:"aside,omitempty"`
	ResetsAt int64 `json:"resets_at,omitempty"`
	StartsAt int64 `json:"starts_at,omitempty"`
	// Pool / Members / Family 用于"按模型池共享额度"的渠道（Antigravity）：
	// 池窗口带 Pool；模型窗口带 Family，且 Pool 指向同一池以便作为成员展示。
	Pool    string        `json:"pool,omitempty"`
	Family  string        `json:"family,omitempty"`
	Members []QuotaWindow `json:"members,omitempty"`
}

// QuotaStatus 配额快照。
type QuotaStatus struct {
	// Total / Used / Remaining 单位由渠道自定义（积分、请求数、Token）。
	Total     int    `json:"total"`
	Used      int    `json:"used"`
	Remaining int    `json:"remaining"`
	Unit      string `json:"unit,omitempty"`
	// ExpiresAt 积分清零时间（Unix 秒），用于"优先消耗即将过期额度"的加权路由。
	ExpiresAt int64  `json:"expires_at,omitempty"`
	Message   string `json:"message,omitempty"`
	// Windows 是订阅的限流窗口（Claude 5h/每周、Codex primary/secondary）。
	Windows []QuotaWindow `json:"windows,omitempty"`
	// Plan / Until 是订阅计划名与到期时间（有新窗口时一并给出）。
	Plan  string `json:"plan,omitempty"`
	Until int64  `json:"until,omitempty"`
	// Balance 是账户余额（美元/积分），与窗口并存时 UI 单独显示一行。
	Balance     float64 `json:"balance,omitempty"`
	BalanceUnit string  `json:"balance_unit,omitempty"`
	// BalanceParts 是自定义余额接口的多段读数（5h / 每周 / 积分…），
	// 用量页每段单独一行；第一段同时作为 Balance。
	BalanceParts []QuotaBalancePart `json:"balance_parts,omitempty"`
	// ReadAt 是这次读数的取得时间，UI 用它显示 "As of ..." 并判断是否过期。
	ReadAt int64 `json:"read_at,omitempty"`
	// Resets 是 Codex 一类订阅的"重置卡"剩余数量（前端显示 ↺ N resets）。
	Resets *QuotaResets `json:"resets,omitempty"`
}

// QuotaResets 是订阅自带的重置次数。
type QuotaResets struct {
	Count int `json:"count"`
}

// QuotaBalancePart 是余额接口返回的一段余额（带可选标签）。
type QuotaBalancePart struct {
	Label string `json:"label,omitempty"`
	Text  string `json:"text"`
}

// 编译期能力探测辅助。
var (
	_ = ChatCapability(nil)
	_ = LocalImportCapability(nil)
	_ = LocalInjectCapability(nil)
	_ = OfficialAccountCapability(nil)
	_ = OfficialBindingCapability(nil)
	_ = TokenRefreshCapability(nil)
	_ = CheckInCapability(nil)
	_ = QuotaCapability(nil)
	_ = NativeResponsesCapability(nil)
)

// ResponsesCompactCapability 可选：原生 Responses 渠道的上下文压缩端点。
// rawReq 是 OpenAI Responses 形状的原始请求；返回上游原始 JSON。
type ResponsesCompactCapability interface {
	CompactResponses(ctx context.Context, env *AccountEnv, rawReq []byte) ([]byte, error)
}
