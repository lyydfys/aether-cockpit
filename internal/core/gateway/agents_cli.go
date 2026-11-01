package gateway

// 本机 Agent CLI 版本巡检：发现 PATH 上的命令行 Agent、读取版本、给出
// 与安装方式匹配的升级命令。只做「巡检 + 提示」——真正升级由用户执行，
// 避免网关替用户改动机器上的全局工具链。

import (
	"aether-cockpit/internal/core/executil"
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// agentCLI 是一个受巡检的命令行 Agent。
type agentCLI struct {
	ID         string
	Name       string
	Bins       []string // 可执行文件候选名
	Package    string   // 包名（npm/pip）
	Manager    string   // npm | pip | manual
	VersionArg string
}

// agentCLIRegistry 只收包名/命令有公开共识的 CLI；
// 拿不准安装方式的宁可不列，避免给错升级命令。
var agentCLIRegistry = []agentCLI{
	{ID: "claude", Name: "Claude Code", Bins: []string{"claude"}, Package: "@anthropic-ai/claude-code", Manager: "npm", VersionArg: "--version"},
	{ID: "codex", Name: "Codex CLI", Bins: []string{"codex"}, Package: "@openai/codex", Manager: "npm", VersionArg: "--version"},
	{ID: "gemini", Name: "Gemini CLI", Bins: []string{"gemini"}, Package: "@google/gemini-cli", Manager: "npm", VersionArg: "--version"},
	{ID: "opencode", Name: "OpenCode", Bins: []string{"opencode"}, Package: "opencode-ai", Manager: "npm", VersionArg: "--version"},
	{ID: "qwen", Name: "Qwen Code", Bins: []string{"qwen"}, Package: "@qwen-code/qwen-code", Manager: "npm", VersionArg: "--version"},
	{ID: "cline", Name: "Cline CLI", Bins: []string{"cline"}, Package: "cline", Manager: "npm", VersionArg: "--version"},
	{ID: "aider", Name: "Aider", Bins: []string{"aider"}, Package: "aider-chat", Manager: "pip", VersionArg: "--version"},
}

// agentCLIStatus 是对外暴露的巡检结果。
type agentCLIStatus struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Installed  bool   `json:"installed"`
	Path       string `json:"path,omitempty"`
	Version    string `json:"version,omitempty"`
	Method     string `json:"method,omitempty"` // npm | pip | brew | direct
	InstallCmd string `json:"install_cmd,omitempty"`
	UpdateCmd  string `json:"update_cmd,omitempty"`
}

var agentVersionRe = regexp.MustCompile(`(\d+\.\d+(?:\.\d+)?(?:[-+][0-9A-Za-z.-]+)?)`)

// parseAgentVersion 从 `cmd --version` 输出里取第一个版本号。
func parseAgentVersion(out string) string {
	if m := agentVersionRe.FindStringSubmatch(out); len(m) > 1 {
		return m[1]
	}
	return ""
}

// agentVersionRunner 可注入，便于测试；默认执行 `<path> --version`。
var agentVersionRunner = func(ctx context.Context, path string, arg string) (string, error) {
	cmd := exec.CommandContext(ctx, path, arg)
	executil.Hide(cmd)
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "CI=1")
	b, err := cmd.CombinedOutput()
	return string(b), err
}

// detectAgentCLI 在 PATH 上查找并读取版本。
func detectAgentCLI(ctx context.Context, a agentCLI) agentCLIStatus {
	st := agentCLIStatus{ID: a.ID, Name: a.Name}
	var path string
	for _, bin := range a.Bins {
		if p, err := exec.LookPath(bin); err == nil {
			path = p
			break
		}
	}
	if path == "" {
		st.InstallCmd = installCommand(a)
		return st
	}
	st.Installed = true
	st.Path = path
	st.Method = detectInstallMethod(path)
	arg := a.VersionArg
	if arg == "" {
		arg = "--version"
	}
	vctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if out, err := agentVersionRunner(vctx, path, arg); err == nil || out != "" {
		st.Version = parseAgentVersion(out)
	}
	st.UpdateCmd = updateCommand(a, st.Method, path)
	return st
}

// detectInstallMethod 按可执行文件路径判断安装来源。
func detectInstallMethod(path string) string {
	lower := strings.ToLower(filepath.ToSlash(path))
	switch {
	case strings.Contains(lower, "/npm/"), strings.Contains(lower, "/node_modules/"):
		return "npm"
	case strings.Contains(lower, "/cellar/"), strings.Contains(lower, "/homebrew/"):
		return "brew"
	case strings.Contains(lower, "/scripts/") || strings.Contains(lower, "/site-packages/"):
		return "pip"
	default:
		return "direct"
	}
}

func installCommand(a agentCLI) string {
	switch a.Manager {
	case "npm":
		return "npm i -g " + a.Package
	case "pip":
		return "pip install " + a.Package
	default:
		return ""
	}
}

func updateCommand(a agentCLI, method, path string) string {
	switch method {
	case "npm":
		return "npm i -g " + a.Package + "@latest"
	case "pip":
		return "pip install -U " + a.Package
	case "brew":
		return "brew upgrade " + strings.ToLower(a.ID)
	}
	switch a.Manager {
	case "npm":
		return "npm i -g " + a.Package + "@latest"
	case "pip":
		return "pip install -U " + a.Package
	}
	_ = path
	return ""
}

// agentCLICache 缓存一轮巡检，避免设置页轮询时反复起进程。
type agentCLICache struct {
	mu       sync.Mutex
	at       time.Time
	statuses map[string]agentCLIStatus
	order    []string
	ttl      time.Duration
}

var agentCLIs agentCLICache

func (c *agentCLICache) snapshot(ctx context.Context) (map[string]agentCLIStatus, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl := c.ttl
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	if c.statuses != nil && time.Since(c.at) < ttl {
		return c.statuses, c.at
	}
	out := make(map[string]agentCLIStatus, len(agentCLIRegistry))
	order := make([]string, 0, len(agentCLIRegistry))
	for _, a := range agentCLIRegistry {
		st := detectAgentCLI(ctx, a)
		out[a.ID] = st
		order = append(order, a.ID)
	}
	c.statuses, c.order, c.at = out, order, time.Now()
	return out, c.at
}

// handleAgentsCLI 替换 compat_api 里的空桩：真实巡检本机 CLI。
func (g *Gateway) handleAgentsCLI(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	statuses, at := agentCLIs.snapshot(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"agents":     statuses,
		"checked_at": at.Unix(),
		"platform":   runtime.GOOS,
	})
}

// handleAgentsInstall 返回安装/升级指引（未安装给安装命令，已安装给升级命令）。
func (g *Gateway) handleAgentsInstall(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	statuses, _ := agentCLIs.snapshot(ctx)
	list := make([]agentCLIStatus, 0, len(statuses))
	for _, a := range agentCLIRegistry {
		list = append(list, statuses[a.ID])
	}
	writeJSON(w, http.StatusOK, list)
}

// handleAgentsCLIUpdate 不代管安装：返回升级命令与原因。
func (g *Gateway) handleAgentsCLIUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	statuses, _ := agentCLIs.snapshot(ctx)
	st, ok := statuses[id]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "未知 Agent: " + id})
		return
	}
	msg := "Aether Cockpit 不代管本机 CLI 的安装与升级"
	if st.UpdateCmd != "" {
		msg += "；请运行：" + st.UpdateCmd
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": st.Version, "pending": false,
		"installed": st.Installed, "command": st.UpdateCmd, "error": msg,
	})
}

// handleAgentsCLIRefresh POST /api/agents/cli —— 清缓存并重新巡检。
func (g *Gateway) handleAgentsCLIRefresh(w http.ResponseWriter, r *http.Request) {
	agentCLIs.mu.Lock()
	agentCLIs.statuses = nil
	agentCLIs.mu.Unlock()
	g.handleAgentsCLI(w, r)
}
