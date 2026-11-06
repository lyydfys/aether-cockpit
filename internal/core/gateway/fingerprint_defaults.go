package gateway

// 反代渠道的默认 TLS 指纹。
//
// 为什么需要它：uTLS 只在账号或 spec 显式声明 tls_profile 时才生效，而仓库里
// 没有一条 spec 声明过——也就是说绝大多数反代请求，上游看到的是 Go 标准库的
// TLS 指纹（Ja3/Ja4 与任何真实客户端都对不上）。这比 UA 更容易被识别：UA 可以
// 改，握手特征改不了。
//
// 这里按"真实客户端是什么"给默认值，而不是一律 Chrome：
//   - Electron / VSCode 系桌面客户端（WorkBuddy、CodeBuddy、Qoder、Trae、
//     Cursor）本身就是 Chromium，用 chrome_131 最接近真身；
//   - 浏览器 Web 端同理；
//   - 纯 API Key 渠道（官方 OpenAI/DeepSeek 等）保持默认，不要套浏览器指纹——
//     官方 SDK 用的是各自运行时，冒充浏览器反而更异常。
//
// 只影响**没有显式 tls_profile** 的账号；账号级 / spec 级配置始终优先。

// reverseTLSDefaults 是"客户端本身是 Chromium"的渠道的默认指纹。
var reverseTLSDefaults = map[string]string{
	"workbuddy-cn":    "chrome_131",
	"workbuddy-intl":  "chrome_131",
	"codebuddy-cn":    "chrome_131",
	"codebuddy-intl":  "chrome_131",
	"qoder-cn":        "chrome_131",
	"qoder-intl":      "chrome_131",
	"trae-cn":         "chrome_131",
	"trae-intl":       "chrome_131",
	"trae-solo":       "chrome_131",
	"trae-solo-cn":    "chrome_131",
	"cursor":          "chrome_131",
	"claude":          "chrome_131",
	"claude-sub":      "chrome_131",
	"antigravity":     "chrome_131",
	"antigravity-ide": "chrome_131",
	"opencode":        "chrome_131",
	"opencode-go":     "chrome_131",
	"devin":           "chrome_131",
	"windsurf":        "chrome_131",
	"github-copilot":  "chrome_131",
	"kiro":            "chrome_131",
}

// defaultTLSProfile 返回该渠道的默认 TLS 指纹；没有默认值时返回空串。
func defaultTLSProfile(providerID string) string {
	return reverseTLSDefaults[providerID]
}
