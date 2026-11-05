package claude

// Claude Code 订阅流量的客户端一致性补丁。
//
// OAuth token 只对 Claude Code 客户端放开；除了 UA 与 anthropic-beta，
// 上游还会校验：
//   - X-Stainless-* / X-App: cli 等官方 CLI 头；
//   - system 首块的 x-anthropic-billing-header，且 cc_version 后缀指纹
//     与 UA 版本、首条 user 文本强绑定。
//
// 指纹算法取自 高并发网关 / 标准套件 对真实 CLI 抓包的复刻；只用于 OAuth
// 订阅模式，API Key 模式保持原样。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf16"
)

const claudeFingerprintSalt = "59cf53e54c78"

var claudeCLIVersionRe = regexp.MustCompile(`claude-cli/(\d+\.\d+\.\d+)`)

// claudeCLIVersion 从 UA 里取版本；不是 Claude CLI 形态时返回空。
func claudeCLIVersion(ua string) string {
	m := claudeCLIVersionRe.FindStringSubmatch(ua)
	if len(m) != 2 {
		return ""
	}
	return m[1]
}

// firstClaudeUserText 取 messages 中第一条 user 消息的首段 text。
func firstClaudeUserText(body map[string]any) string {
	messages, _ := body["messages"].([]any)
	for _, raw := range messages {
		msg, _ := raw.(map[string]any)
		if msg == nil || normalizeRole(anyText(msg["role"])) != "user" {
			continue
		}
		switch content := msg["content"].(type) {
		case string:
			return content
		case []any:
			for _, partRaw := range content {
				part, _ := partRaw.(map[string]any)
				if part == nil {
					continue
				}
				if anyText(part["type"]) != "text" {
					continue
				}
				if text, _ := part["text"].(string); text != "" {
					return text
				}
			}
			return ""
		}
	}
	return ""
}

// claudeCodeFingerprint 复刻 Claude Code CLI 的 cc_version 指纹：
// UTF-16 码元取 4/7/20（越界补 '0'），样本+SALT+版本做 SHA-256，取前 3 位。
func claudeCodeFingerprint(body map[string]any, version string) string {
	text := firstClaudeUserText(body)
	units := make([]uint16, 0, 22)
	for _, r := range text {
		units = utf16.AppendRune(units, r)
		if len(units) > 20 {
			break
		}
	}
	chars := make([]uint16, 0, 3)
	for _, idx := range []int{4, 7, 20} {
		if idx < len(units) {
			chars = append(chars, units[idx])
		} else {
			chars = append(chars, '0')
		}
	}
	sample := string(utf16.Decode(chars))
	sum := sha256.Sum256([]byte(claudeFingerprintSalt + sample + version))
	return hex.EncodeToString(sum[:])[:3]
}

// injectClaudeBillingHeader 在 system 首块写入 billing attribution。
// 返回 false 表示 UA 不是 Claude CLI 形态（例如自定义中转 UA），不做伪装。
func injectClaudeBillingHeader(body map[string]any, ua string) bool {
	version := claudeCLIVersion(ua)
	if version == "" {
		return false
	}
	text := fmt.Sprintf("x-anthropic-billing-header: cc_version=%s.%s; cc_entrypoint=cli;",
		version, claudeCodeFingerprint(body, version))
	system, _ := body["system"].([]any)
	body["system"] = append([]any{map[string]any{"type": "text", "text": text}}, system...)
	return true
}

// applyClaudeMimicHeaders 写入官方 CLI 的一组固定头；stream=true 时补
// x-stainless-helper-method: stream。
func applyClaudeMimicHeaders(h http.Header, stream bool) {
	h.Set("X-App", "cli")
	h.Set("X-Stainless-Lang", "js")
	h.Set("X-Stainless-Package-Version", "0.94.0")
	h.Set("X-Stainless-OS", "Linux")
	h.Set("X-Stainless-Arch", "arm64")
	h.Set("X-Stainless-Runtime", "node")
	h.Set("X-Stainless-Runtime-Version", "v24.3.0")
	h.Set("X-Stainless-Retry-Count", "0")
	h.Set("X-Stainless-Timeout", "600")
	h.Set("Anthropic-Dangerous-Direct-Browser-Access", "true")
	if stream {
		h.Set("X-Stainless-Helper-Method", "stream")
	}
}

func anyText(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
