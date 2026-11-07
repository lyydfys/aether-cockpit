package workbuddy

// 客户端出网身份（identity）：一个渠道同一时刻只使用**一个身份档位**，
// UA、IDE 版本、CLI 段、Stainless 头、运行时声明、设备标识、请求间隔都从同一个
// IdentityProfile 派生，不允许"UA 说桌面、头却发 CLI"的跨档组合。
//
// 依据：用 REA（jadx-headless）对参考实现 `com.joy4fire.wbproxy` 1.0.6 做的
// 方法级比对（证据见 docs/ANTI_BAN.md §2.4）。参考实现的身份模型：
//
//	uaMode "cli"      双段 UA：WorkBuddy/5.9.3 WorkBuddy/5.9.3 CLI/2.165.0
//	                  + Stainless 族（lang=js / runtime=node / Windows x64）
//	uaMode "client"   单段 UA：WorkBuddy/5.9.3
//	uaMode "browser"  Identity.BROWSER_UA（Chrome 125 / Windows）
//
// 三档预设（与参考实现逐字一致）：
//
//	cli（官方 CLI，参考实现默认） clientVersion 5.9.3 / cliVersion 2.165.0 /
//	    stainless 6.25.0 / Windows / x64 / node / v22.14.0 / SaaS /
//	    privateData true / deviceHeaders false / machineSeed wb2a / pace 350ms
//	desktop  同 cli，但 uaMode=client
//	quiet    同 desktop，但 pace 900ms
//
// 另有 browser 档（自定义/导入形态）复用 BROWSER_UA。
//
// 边界：只发官方自己会发的头；设备标识默认关（参考实现 deviceHeaders=false），
// 只有调用方显式开启、且能按参考算法精确派生时才发；不伪造签名、不复制他人 ID。

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// 参考实现 Fingerprint 的构造默认值。
	defaultClientVersion    = "5.9.3"
	defaultCLIVersion       = "2.165.0"
	defaultStainlessVersion = "6.25.0"
	defaultProduct          = "SaaS"
	defaultMachineSeed      = "wb2a"

	// browserUA 是 Identity.BROWSER_UA 的字面值（REA 提取自 dex 静态字段）。
	browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

	// identityEnvVar 是进程级配置入口；默认 cli（双段，与参考实现一致）。
	identityEnvVar = "AETHER_WORKBUDDY_IDENTITY"
)

// IdentityProfile 描述一个自洽的"出网身份"。头与 UA 全部由它派生。
type IdentityProfile struct {
	Name             string
	UAMode           string // cli（双段）/ client（单段桌面）/ browser
	ClientVersion    string
	CLIVersion       string
	StainlessVersion string
	OS               string
	Arch             string
	Runtime          string
	RuntimeVersion   string
	Product          string
	PrivateData      bool
	DeviceHeaders    bool // 默认 false：不发 X-Machine-ID / X-Session-ID
	MachineSeed      string
	PaceGap          time.Duration
}

// UA 按 uaMode 生成三种形态（逐字对齐 Fingerprint.ua()）。
func (p IdentityProfile) UA() string {
	switch p.UAMode {
	case "client":
		return "WorkBuddy/" + p.ClientVersion
	case "browser":
		return browserUA
	default: // "cli"：参考实现默认，双段 UA
		return "WorkBuddy/" + p.ClientVersion + " WorkBuddy/" + p.ClientVersion + " CLI/" + p.CLIVersion
	}
}

// Stainless 只在 uaMode=="cli" 时为 true（Fingerprint.getStainless 同义）。
func (p IdentityProfile) Stainless() bool { return p.UAMode == "cli" }

func newIdentityPreset(name, uaMode string, pace time.Duration) IdentityProfile {
	return IdentityProfile{
		Name:             name,
		UAMode:           uaMode,
		ClientVersion:    defaultClientVersion,
		CLIVersion:       defaultCLIVersion,
		StainlessVersion: defaultStainlessVersion,
		OS:               "Windows",
		Arch:             "x64",
		Runtime:          "node",
		RuntimeVersion:   "v22.14.0",
		Product:          defaultProduct,
		PrivateData:      true,
		DeviceHeaders:    false,
		MachineSeed:      defaultMachineSeed,
		PaceGap:          pace,
	}
}

var (
	// 参考实现 OFFICIAL/DESKTOP/QUIET 三个预设（Fingerprint.<clinit>）。
	identityCLI     = newIdentityPreset("cli", "cli", 350*time.Millisecond)
	identityDesktop = newIdentityPreset("desktop", "client", 350*time.Millisecond)
	identityQuiet   = newIdentityPreset("quiet", "client", 900*time.Millisecond)
	// browser 是参考实现支持的第三种 UA 形态，作为额外可选档位。
	identityBrowser = newIdentityPreset("browser", "browser", 350*time.Millisecond)
)

var identityPresets = map[string]IdentityProfile{
	identityCLI.Name:     identityCLI,
	identityDesktop.Name: identityDesktop,
	identityQuiet.Name:   identityQuiet,
	identityBrowser.Name: identityBrowser,
}

// identityPresetOrder 是可选档位的稳定顺序（官方 CLI → 桌面 → 安静，
// browser 为附加 UA 形态）。HTTP 接口的 available 用它，避免第二份列表。
var identityPresetOrder = []string{
	identityCLI.Name,
	identityDesktop.Name,
	identityQuiet.Name,
	identityBrowser.Name,
}

// EffectiveIdentity 返回**运行中进程实际生效**的身份档位。
// 注意与 settings.json 里保存的值可能不同（保存后未重启），调用方按需对比。
func EffectiveIdentity() IdentityProfile { return currentIdentity() }

// AvailableIdentityPresets 返回可选档位名（稳定顺序的副本）。
func AvailableIdentityPresets() []string {
	out := make([]string, len(identityPresetOrder))
	copy(out, identityPresetOrder)
	return out
}

// NormalizeIdentityPreset 把外部输入规范化为合法档位名。
// 空串 = 默认档位（cli）；official 是 cli 的兼容别名；ok=false 表示未知档位。
// 设置页保存、后端白名单校验都用它，避免各处各写一份可选项列表。
func NormalizeIdentityPreset(name string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return identityCLI.Name, true
	}
	if key == "official" {
		return identityCLI.Name, true
	}
	if _, ok := identityPresets[key]; ok {
		return key, true
	}
	return "", false
}

// resolveIdentityPreset 把配置值解析成档位；空值 = 默认 cli（双段）。
// 别名 official = cli，与参考实现的「官方 CLI」档对应。
func resolveIdentityPreset(name string) (IdentityProfile, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" || key == "official" {
		if key == "" {
			return identityCLI, nil
		}
		key = "cli"
	}
	if p, ok := identityPresets[key]; ok {
		return p, nil
	}
	return identityCLI, fmt.Errorf("未知 WorkBuddy 身份档位 %q（可选 cli / desktop / quiet / browser），已按 cli 处理", name)
}

// identityOverride 是宿主（网关配置）注入的档位；比环境变量优先。
var identityOverride atomic.Value

// SetIdentityPreset 供网关在加载配置后调用（配置项 workbuddy_identity）。
// 传空串恢复默认（cli）。
func SetIdentityPreset(name string) error {
	p, err := resolveIdentityPreset(name)
	if err != nil {
		return err
	}
	identityOverride.Store(p.Name)
	return nil
}

// currentIdentity 解析当前生效档位：
// 宿主显式设置 > 环境变量 > 默认 cli（与参考实现一致，双段 UA）。
func currentIdentity() IdentityProfile {
	if v, ok := identityOverride.Load().(string); ok && v != "" {
		if p, err := resolveIdentityPreset(v); err == nil {
			return p
		}
	}
	if v := strings.TrimSpace(os.Getenv(identityEnvVar)); v != "" {
		if p, err := resolveIdentityPreset(v); err == nil {
			return p
		}
	}
	return identityCLI
}

// IdentityPresetName 返回当前生效档位名（诊断/状态页用）。
func IdentityPresetName() string {
	return currentIdentity().Name
}

// setPluginHeaders 写入官方固定插件头（7 个，逐字对齐 Identity.pluginHeaders）。
func setPluginHeaders(h http.Header, prof IdentityProfile) {
	h.Set("User-Agent", prof.UA())
	h.Set("X-Product", prof.Product)
	h.Set("X-IDE-Type", "WorkBuddy")
	h.Set("X-IDE-Name", "WorkBuddy")
	h.Set("X-IDE-Version", prof.ClientVersion)
	h.Set("X-Private-Data", strconv.FormatBool(prof.PrivateData))
	h.Set("X-CodeBuddy-Request", "1")
	h.Set("X-Requested-With", "XMLHttpRequest")
}

// applyStainlessHeaders 仅 CLI 形态下发（参考实现 Retry-Count 恒为 "0"）。
func applyStainlessHeaders(h http.Header, prof IdentityProfile) {
	if !prof.Stainless() {
		return
	}
	h.Set("X-Stainless-Lang", "js")
	h.Set("X-Stainless-Package-Version", prof.StainlessVersion)
	h.Set("X-Stainless-OS", prof.OS)
	h.Set("X-Stainless-Arch", prof.Arch)
	h.Set("X-Stainless-Runtime", prof.Runtime)
	h.Set("X-Stainless-Runtime-Version", prof.RuntimeVersion)
	h.Set("X-Stainless-Retry-Count", "0")
}

// setConversationHeaders 对齐 Identity.conversationHeaders：
// X-Request-ID 就是 messageId（不是另一个随机值）。
func setConversationHeaders(h http.Header, conversationID, messageID string) {
	h.Set("X-Conversation-ID", conversationID)
	h.Set("X-Conversation-Message-ID", messageID)
	h.Set("X-Request-ID", messageID)
	h.Set("X-Agent-Intent", "craft")
	h.Set("X-Agent-Purpose", "conversation")
}

// applyChannelVerifyHeaders 对齐 Identity.channelVerifyHeaders。
//
// traceId/spanId/parentSpanId 由 SecureRandom 现取（参考实现 Identity.hex(n)），
// b3 的顺序是 <trace>-<span>-1-<parent>（注意与常见 B3 写法不同）。
// x-codebuddy-request 用**小写 x** 单列一条，与 X-CodeBuddy-Request 并存。
func applyChannelVerifyHeaders(h http.Header, conversationID, requestID string) {
	traceID := randomHex(32)
	spanID := randomHex(16)
	parentSpanID := randomHex(16)

	h.Set("X-Private-Data", "true")
	h["x-codebuddy-request"] = []string{"1"} // 保留小写，逐字对齐官方抓包
	h.Set("X-Conversation-Request-ID", requestID)
	h.Set("traceparent", "00-"+traceID+"-"+spanID+"-01")
	h.Set("b3", traceID+"-"+spanID+"-1-"+parentSpanID)
	h.Set("X-B3-TraceId", traceID)
	h.Set("X-B3-ParentSpanId", parentSpanID)
	h.Set("X-B3-SpanId", spanID)
	h.Set("X-B3-Sampled", "1")
	h.Set("baggage", "codebuddy.session_id="+conversationID+
		",codebuddy.conversation_request_id="+requestID)
}

// --- 设备标识（默认关）---

// machineHeadersFor 按参考算法派生设备标识；默认不发。
//
// 开启条件：账号凭证 device_headers=true（或未来 profile 显式打开）。
// 算法逐字复刻 Identity.derive / deriveUuid：
//
//	X-Machine-ID = sha256(machineSeed + ":machine:" + uid) 的前 8 位 hex
//	X-Session-ID = 同一 hash 拼成 UUID 形态（版本位 4 / 变体位 8）
func machineHeadersFor(prof IdentityProfile, get func(string) string) map[string]string {
	enabled := prof.DeviceHeaders || credentialBool(get("device_headers"))
	if !enabled {
		return nil
	}
	uid := strings.TrimSpace(get("user_id"))
	if uid == "" {
		return nil
	}
	seed := strings.TrimSpace(get("machine_seed"))
	if seed == "" {
		seed = prof.MachineSeed
	}
	machine := deriveHex(seed, "machine", uid)
	session := deriveUUID(seed, "session", uid)
	return map[string]string{
		"X-Machine-ID": machine[:8],
		"X-Session-ID": session,
	}
}

// accountCredentials 是 machineHeadersFor 需要的最小凭证接口（便于测试）。
func credentialBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// deriveHex 逐字复刻 Identity.derive：sha256(seed: purpose : uid) 的小写 hex。
func deriveHex(seed, purpose, uid string) string {
	sum := sha256.Sum256([]byte(seed + ":" + purpose + ":" + uid))
	return hex.EncodeToString(sum[:])
}

// deriveUUID 逐字复刻 Identity.deriveUuid：
// h[0:8]-h[8:12]-4h[13:16]-8h[17:20]-h[20:32]。
func deriveUUID(seed, purpose, uid string) string {
	h := deriveHex(seed, purpose, uid)
	return h[0:8] + "-" + h[8:12] + "-4" + h[13:16] + "-8" + h[17:20] + "-" + h[20:32]
}

// --- 会话/请求 ID ---

var convCache = struct {
	mu sync.Mutex
	m  map[string]string
}{m: map[string]string{}}

// conversationIDFor 返回账号级稳定会话 ID（UUID v4，进程生命周期内固定）。
//
// 参考实现 Accounts.conversationId 以 (account.key + sessionSalt) 为键缓存
// uuidV4；这里用账号 ID 作为键，语义一致（同一账号同一进程内会话不变）。
func conversationIDFor(accountID string) string {
	key := strings.TrimSpace(accountID)
	if key == "" {
		return uuidV4()
	}
	convCache.mu.Lock()
	defer convCache.mu.Unlock()
	if v, ok := convCache.m[key]; ok {
		return v
	}
	v := uuidV4()
	convCache.m[key] = v
	return v
}

// uuidV4 是标准 UUID v4（带连字符），用于会话 ID。
func uuidV4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// uuidV7 逐字复刻 Identity.uuidV7：12 位毫秒时间 + 20 位随机 hex，共 32 位、
// **不带连字符**。参考实现把它同时用作 X-Conversation-Message-ID / X-Request-ID
// 与独立的 X-Conversation-Request-ID。
func uuidV7() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%012x", time.Now().UnixMilli()) + hex.EncodeToString(b)
}

// randomHex 对齐 Identity.hex(n)：n 位随机 hex（SecureRandom）。
func randomHex(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, (n+1)/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

// --- 请求节流 ---

var wbPace = struct {
	mu   sync.Mutex
	next map[string]time.Time
}{next: map[string]time.Time{}}

// paceWaitAt 返回本次请求需要等待的时长，并把该账号的下一个可用时刻向后推。
// now 由调用方传入（纯计算，便于单测）；并发请求会各占一个时间片。
func paceWaitAt(next map[string]time.Time, accountID string, now time.Time, gap time.Duration) time.Duration {
	if accountID == "" || gap <= 0 {
		return 0
	}
	slot := next[accountID]
	if slot.IsZero() || !now.Before(slot) {
		next[accountID] = now.Add(gap)
		return 0
	}
	wait := slot.Sub(now)
	next[accountID] = slot.Add(gap)
	return wait
}

// paceWait 是同账号请求节流入口；间隔由当前身份档位决定。
func paceWait(accountID string, gap time.Duration) time.Duration {
	wbPace.mu.Lock()
	defer wbPace.mu.Unlock()
	return paceWaitAt(wbPace.next, accountID, time.Now(), gap)
}

// --- 区域（Region）---

// regionOrigin / regionDefaultDomain 对齐参考实现 Region 枚举：
//
//	DOMESTIC      origin=https://copilot.tencent.com  defaultDomain=www.codebuddy.cn
//	INTERNATIONAL origin=https://www.workbuddy.ai      defaultDomain=www.workbuddy.ai
func regionOrigin(intl bool) string {
	if intl {
		return "https://www.workbuddy.ai"
	}
	return "https://copilot.tencent.com"
}

func regionDefaultDomain(intl bool) string {
	if intl {
		return "www.workbuddy.ai"
	}
	return "www.codebuddy.cn"
}

// resetIdentityForTest 清空宿主注入的档位与账号会话缓存（仅测试使用）。
func resetIdentityForTest() {
	identityOverride.Store("")
	convCache.mu.Lock()
	convCache.m = map[string]string{}
	convCache.mu.Unlock()
}
