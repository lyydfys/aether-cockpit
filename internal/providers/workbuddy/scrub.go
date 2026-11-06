package workbuddy

// 提示词指纹清洗（**默认关闭，按账号开启**）。
//
// 场景：把 Claude Code / Codex 这类 agent 客户端指到 WorkBuddy 后端时，客户端会在
// 系统提示里塞一段带产品指纹的开场白（"You are Claude Code, Anthropic's official
// CLI for Claude." 之类）。对 WorkBuddy 的上游来说，这等于直接写明"我是别家客户端"，
// 是风控最容易抓的一类信号。
//
// 只在**账号显式开启**时生效（凭证 sanitize_claude_prompt=1）：
//   - 只处理第一条 system 消息；
//   - 只替换已知的开场白指纹句，不动用户正文、不动代码；
//   - 替换成中性的 assistant 描述，语义等价、行为不变。
//
// 不做"全文替换 Claude Code 字样"——那会改坏用户粘贴的文档与代码，副作用远大于收益。

import (
	"strings"
)

// claudePromptFingerprints 是已知的客户端开场白指纹（按长度降序匹配，先长后短）。
var claudePromptFingerprints = []struct {
	marker string
	repl   string
}{
	{
		"You are Claude Code, Anthropic's official CLI for Claude.",
		"You are a helpful AI coding assistant.",
	},
	{
		"You are Claude Code, Anthropic's official CLI for Claude",
		"You are a helpful AI coding assistant.",
	},
	{
		"You are Claude Code, Anthropic's official CLI.",
		"You are a helpful AI coding assistant.",
	},
	{
		"You are Claude Code",
		"You are a helpful AI coding assistant",
	},
	{
		"You are OpenCode, the open source coding agent.",
		"You are a helpful AI coding assistant.",
	},
}

// scrubEnabled 判断该账号是否开启了提示词清洗。
func scrubEnabled(get func(string) string) bool {
	v := strings.ToLower(strings.TrimSpace(get("sanitize_claude_prompt")))
	switch v {
	case "1", "true", "yes", "on", "开启":
		return true
	}
	return false
}

// scrubClaudeFingerprint 清洗 payload 里第一条 system 消息的开场白指纹。
// 返回 true 表示确实改写过（供日志/埋点使用）。
func scrubClaudeFingerprint(payload map[string]any) bool {
	if payload == nil {
		return false
	}
	msgs, _ := payload["messages"].([]map[string]any)
	for i, msg := range msgs {
		if !strings.EqualFold(strings.TrimSpace(strOf(msg["role"])), "system") {
			continue
		}
		content := strOf(msg["content"])
		if content == "" {
			// 多段 content 也要清洗：取第一段 text。
			if parts, ok := msg["content"].([]map[string]any); ok && len(parts) > 0 {
				content = strOf(parts[0]["text"])
			}
		}
		cleaned, changed := scrubText(content)
		if !changed {
			return false
		}
		msgs[i]["content"] = cleaned
		payload["messages"] = msgs
		return true
	}
	return false
}

// scrubText 替换文本里出现的指纹句。
func scrubText(text string) (string, bool) {
	out := text
	changed := false
	for _, fp := range claudePromptFingerprints {
		if strings.Contains(out, fp.marker) {
			out = strings.ReplaceAll(out, fp.marker, fp.repl)
			changed = true
		}
	}
	return out, changed
}
