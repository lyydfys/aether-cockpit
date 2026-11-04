package gateway

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"aether-cockpit/internal/core/launcher"
	"aether-cockpit/internal/core/sessions"
)

// 会话「在终端重开」。
//
// 之前只有 Claude Code 拼了 `claude --resume <id>`，而且只能复制粘贴
// （POST /api/sessions/terminal 是空 stub）。这里把"重开"真正接通：
// 生成命令 → 在会话自己的工作目录里开一个新终端窗口跑起来。

// resumeCommands 是各客户端"接着上次会话"的命令模板。
//
// 只收录确认过命令形态的三个（与扫描器支持的 agent 一一对应）；
// 其余留空，页面据此显示"该客户端暂不支持重开"，而不是给一条错命令。
var resumeCommands = map[string]string{
	"claude-code": "claude --resume %s",
	"codex":       "codex resume %s",
	"gemini-cli":  "gemini --resume %s",
}

// resumeCommand 返回某会话的重开命令；不支持的 agent 返回空串。
//
// 会话 ID 只允许安全字符：它会被拼进一条要交给 shell 的命令行。
// 模板来源：用户设置里的覆盖优先，其次是内置的三个。
//
// 为什么允许用户覆盖：各客户端"续接上次会话"的参数并不统一（有的 --resume、
// 有的 resume 子命令、有的 --session），猜错就是给用户一条跑不通的命令。
// 让用户自己填模板，既覆盖了未知客户端，也不用我们为每个 agent 发一次版。
func (g *Gateway) resumeCommand(agent, id string) string {
	agent = strings.ToLower(strings.TrimSpace(agent))
	if agent == "" || !safeSessionID(id) {
		return ""
	}
	tpl := ""
	if g != nil {
		g.settingsMu.Lock()
		tpl = strings.TrimSpace(g.settings.ResumeCommands[agent])
		g.settingsMu.Unlock()
	}
	if !validResumeTemplate(tpl) {
		// 用户模板不合法（缺 %s 或占位符过多）时退回内置，而不是拼出坏命令。
		tpl = resumeCommands[agent]
	}
	if tpl == "" {
		return ""
	}
	return fmt.Sprintf(tpl, id)
}

// validResumeTemplate 校验模板：必须且只能有一个 %s 占位符（会话 ID）。
//
// 为什么不允许多个：多出来的占位符拿不到参数，会被格式化成 "%!s(MISSING)"，
// 交给 shell 就是一条坏命令；而且模板最终会进终端，越简单越安全。
func validResumeTemplate(tpl string) bool {
	if tpl == "" || strings.Contains(tpl, "%%") {
		return false
	}
	return strings.Count(tpl, "%s") == 1
}

// safeSessionID 限制会话 ID 的字符集，避免把任意内容拼进 shell 命令行。
func safeSessionID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}

// handleSessionTerminal POST /api/sessions/terminal
//
// 请求体：{"agent":"claude-code","id":"<session id>"}
// 也可以带 "cwd" 覆盖默认工作目录（WSL 会话由 cwd 决定落在哪）。
func (g *Gateway) handleSessionTerminal(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Agent string `json:"agent"`
		ID    string `json:"id"`
		WSL   string `json:"wsl"`
		Cwd   string `json:"cwd"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	command := g.resumeCommand(body.Agent, body.ID)
	if command == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "暂不支持在该客户端重开会话: " + strings.TrimSpace(body.Agent),
		})
		return
	}
	// 优先用客户端扫出来的 cwd（会话原本就工作在哪个目录），
	// 其次是请求里显式给的；目录不存在就退回用户主目录。
	dir := strings.TrimSpace(body.Cwd)
	if dir == "" {
		for _, s := range sessions.Scan() {
			if s.ID == body.ID {
				dir = s.Cwd
				break
			}
		}
	}
	if dir != "" {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			dir = ""
		}
	}
	if err := launcher.OpenTerminal(dir, command); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "打开终端失败: " + err.Error(), "command": command,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"opened": true, "command": command, "cwd": dir})
}
