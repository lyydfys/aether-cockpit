package reverse

import (
	"net/url"
	"strings"

	"aether-cockpit/internal/provider"
)

// opencodeIsOfficial 判断一份 OpenCode 凭证是不是官方端点。
//
// OpenCode 这一类渠道天然是"官转 + 自部署"混装的：同一个渠道里既有官方
// Zen / Go 的 Key，也有用户自己（或第三方）搭的兼容端点。两者的差别只在
// base_url —— 官方端点是 *.opencode.ai，自部署是别的主机。
//
// 判定规则（宁可把判断做窄，也别把中转误判成官方）：
//   - 没有 base_url：走渠道默认端点，即官方；
//   - 有 base_url 且主机是 opencode.ai / *.opencode.ai：官方；
//   - 其它主机：自部署/中转。
//
// 主机比较必须按域名边界，不能简单 strings.Contains："opencode.ai.evil.com"
// 和 "notopencode.ai" 都含这个子串，但它们显然不是官方。
func opencodeIsOfficial(creds map[string]string) bool {
	base := strings.ToLower(strings.TrimSpace(creds["base_url"]))
	if base == "" {
		return true
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		// base_url 写坏了不该被当成官方：保守地当作自部署、排序靠后。
		// 账号本身仍可用（请求会直接失败并给出真实错误），不会因此丢号。
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	return host == "opencode.ai" || strings.HasSuffix(host, ".opencode.ai")
}

// init 注册全部逆向渠道骨架。
//
// 这里刻意不 import 任何具体协议实现：同一产品的 CN / Intl 变体共用本引擎，
// 差异只体现在 Profile（网络策略、区域标识）与 spec 文件上。
// 真正的协议细节由 configs/specs/<provider>.json 提供，抓包后无需改代码。
func init() {
	for _, p := range profiles() {
		provider.Register(New(p))
	}
}

// profiles 是总方案第 3 节渠道矩阵的代码映射。
//
// 早期文档口径写"13 个逆向渠道"，但 v3 矩阵实际列出的是下面 10 个 ProviderID；
// 多出来的 3 个是尚未落入矩阵的草案区域变体。本版以矩阵为准，避免凭空造 ID。
func profiles() []Profile {
	return []Profile{
		// WorkBuddy 已升级为独立实现（internal/providers/workbuddy），
		// 不再由 spec 驱动的逆向骨架承载。

		// Trae 已升级为独立实现（internal/providers/trae），
		// 不再由 spec 驱动的逆向骨架承载。

		// Qoder 已升级为独立实现（internal/providers/qoder）：
		// COSY 签名 + 自定义 Base64 + 设备令牌导入，spec 无法表达。

		// Antigravity 已升级为独立实现（internal/providers/antigravity），
		// 不再由 spec 驱动的逆向骨架承载。

		// Kiro 已升级为独立实现（internal/providers/kiro）：
		// AWS EventStream 与 conversationState 不是 spec 能表达的。

		{
			ID:            "codex",
			DisplayName:   "Codex",
			Family:        "codex",
			RegionTag:     "GLOBAL",
			BrandColor:    "#111827",
			Description:   "Codex 渠道。支持从本机 Codex CLI 的 config.toml 导入自定义 provider（base_url / bearer token / model），上游原生 Responses 时自动走 Fast-Path 零损直通；模型可用 codex/auto 或 codex/<模型名>；也导入 ~/.codex/auth.json 的 ChatGPT 订阅登录态（自动读 5h/7d 额度、到期刷新、直通带账号头）。",
			NetworkPolicy: provider.NetPolicyProxyFirst,
			Auths: []provider.AuthMethod{
				provider.AuthLocalImport,
				provider.AuthManualInput,
			},
			// Codex CLI 公开 OAuth 客户端：固定 localhost:1455 回调，
			// 换 token 后从 id_token 取 chatgpt_account_id 作为账号标识。
			OAuth: &provider.OAuthConfig{
				AuthorizeURL: "https://auth.openai.com/oauth/authorize",
				TokenURL:     "https://auth.openai.com/oauth/token",
				ClientID:     codexClientID,
				Scopes:       []string{"openid", "profile", "email", "offline_access"},
				RedirectPath: "/auth/callback",
				RedirectHost: "localhost",
				RedirectPort: 1455,
				ExtraAuthParams: map[string]string{
					"id_token_add_organizations": "true",
					"codex_cli_simplified_flow":  "true",
					"originator":                 "codex_cli_rs",
				},
				IDTokenClaims: map[string]string{
					"account_id": "https://api.openai.com/auth#chatgpt_account_id",
				},
				ExtraCredentials: map[string]string{
					"base_url": "https://chatgpt.com/backend-api/codex",
					"model":    "gpt-5-codex",
				},
			},
			Middlewares:              []string{"stateful_session"},
			DefaultResponsesPath:     "/responses",
			NativeResponses:          true,
			NativeResponsesNonStream: true,
			AutoModel:                true,
			QuotaKind:                "codex",
			Refresh:                  true,
			// 官方订阅优先：ChatGPT OAuth 登录态（account_id / refresh_token）
			// 排在中转账号（base_url + token）之前；显式 priority 仍然优先。
			OfficialFirst: true,
			OfficialAccount: func(creds map[string]string) bool {
				return creds["account_id"] != "" || creds["refresh_token"] != ""
			},
			OfficialBinding: codexOfficialBinding,
		},

		{
			ID:            "opencode",
			DisplayName:   "OpenCode",
			Family:        "opencode",
			RegionTag:     "MIXED",
			BrandColor:    "#10B981",
			Description:   "OpenCode Zen 渠道。OpenAI 兼容端点，支持从 ~/.local/share/opencode/auth.json 的 opencode 条目导入 API Key；免费模型与按量付费模型都可用 opencode/<模型名>。Go 订阅请用 OpenCode Go 渠道。",
			NetworkPolicy: provider.NetPolicySystem,
			Auths: []provider.AuthMethod{
				provider.AuthLocalImport,
				provider.AuthManualInput,
			},
			Middlewares:     []string{"stateful_session"},
			DefaultBaseURL:  "https://opencode.ai/zen/v1",
			DefaultChatPath: "/chat/completions",
			// 官转优先：同一个渠道里常常既有官方 Zen 的 Key、又有自部署/中转
			// 端点。官方端点先走，等它限流/超额时再落到中转上——这就是"尽量
			// 走官方，但别把可用性打死"。
			OfficialFirst:   true,
			OfficialAccount: opencodeIsOfficial,
			ModelListPath:   "/models",
			DefaultModels: []string{
				"big-pickle",
				"beam-free",
				"flint-40b-free",
				"mimo-v2.6-flash-free",
				"nemotron-3.5-super-vl-free",
				"nemotron-3.5-lightning-free",
				"deepseek-v4.1-flash-free",
				"glm-5.3-flash-free",
				"deepseek-v4.1-flash",
				"gemini-4-argon",
				"muse-wonder-1.0",
			},
		},

		{
			ID:            "opencode-go",
			DisplayName:   "OpenCode Go",
			Family:        "opencode",
			RegionTag:     "MIXED",
			BrandColor:    "#10B981",
			Description:   "OpenCode Go 订阅渠道。默认走 canonical 端点（/inference/go/openai/v1），需要 x-opencode-session；支持从 ~/.local/share/opencode/auth.json 的 opencode-go 条目导入 Go API Key；额度按滚动 / 每周 / 每月三个窗口计。",
			NetworkPolicy: provider.NetPolicySystem,
			Auths: []provider.AuthMethod{
				provider.AuthLocalImport,
				provider.AuthManualInput,
			},
			Middlewares: []string{"stateful_session"},
			// canonical 路径，不是旧的 /zen/go/v1。
			//
			// 依据实测（2026-11-05，25 次/路径的行为探针）：OpenCode Go 背后是
			// "原生官转透传 + 翻译层官转 + 自部署 cell" 的动态混合池，
			// canonical /inference/go/openai/v1 的官方命中率约 80%，而
			// /zen/go/v1 只有 6/8、短路径 /inference/go/v1 更差（3/8）。
			//
			// 另有一条硬约束：旧 /zen/go 路径要 x-api-key，而本引擎默认发
			// Authorization: Bearer；canonical OpenAI 路径接受 Bearer。
			// 换句话说，走 canonical 既更"官方"，也更不容易鉴权失败。
			DefaultBaseURL:  "https://opencode.ai/inference/go/openai/v1",
			DefaultChatPath: "/chat/completions",
			SessionHeader:   "x-opencode-session",
			// Go 订阅本身就是官方端点；同样允许用户把它指到自部署上，
			// 所以沿用同一套判定：官方优先、自部署兜底。
			OfficialFirst:   true,
			OfficialAccount: opencodeIsOfficial,
			// 模型目录不在 canonical chat 前缀下：/inference/go/openai/v1/models
			// 会 404（实测）。目录走绝对 URL，命中 /inference/go/v1/models
			// （该路径实测 200，且带模型元数据）。chat 仍然走 canonical。
			ModelListPath: "https://opencode.ai/inference/go/v1/models",
			QuotaKind:     "opencode_go",
			DefaultModels: []string{
				"deepseek-v4.1-flash",
				"deepseek-v4-pro",
				"glm-5.2",
				"glm-5.3",
				"grok-4.7",
				"muse-spark-1.2-contributor",
				"muse-spark-1.3-contributor",
				"glm-5.3-flash",
				"gpt-5.6-luna",
				"gpt-6-luna",
				"claude-haiku-5-5",
				"hy3",
				"hy4-preview",
				"kimi-k2.7-code",
				"kimi-k3",
				"mimo-v2.6-flash",
				"mimo-v2.6-pro",
				"minimax-m3",
				"minimax-m3.1-flash",
				"longcat-2.0",
				"step-5-preview",
				"beam-free",
				"qwen3.8-max",
				"qwen3.8-flash",
				"qwen3.7-plus",
				"kimi-k3.1-pro",
				"iceman-contributor-free",
				"gemini-4-argon",
			},
		},
	}
}
