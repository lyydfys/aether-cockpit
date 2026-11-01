package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/launcher"
)

// 应用启动 —— 与"一键接入"（client_api.go）互补：接入改写全局配置文件，
// 一次只能有一个网关会话；Launch 用环境变量拉起独立会话，桌面端则用
// 正常用户目录直接打开。
//
// launcherMgr 应用启动/多开管理器的进程内单例。
//
// 实例表是进程内状态：网关重启后列表清空，但已拉起的终端是脱离父进程的，
// 继续存活（不再被追踪，需要时手动关闭）。
var launcherMgr = launcher.New()

// defaultLaunchCommands 内置默认启动命令；settings.LaunchCommands 里的
// 非空条目按 client_id 覆盖对应项。
var defaultLaunchCommands = map[string]string{
	"claude_code": "claude",
	"codex_cli":   "codex",
	"gemini_cli":  "gemini",
	"opencode":    "opencode",
}

// resolveLaunchCommands 合并内置默认与设置项（设置里的非空条目优先，
// 空条目回落到内置默认）。
func (g *Gateway) resolveLaunchCommands() map[string]string {
	g.settingsMu.Lock()
	custom := g.settings.LaunchCommands
	g.settingsMu.Unlock()

	merged := make(map[string]string, len(defaultLaunchCommands)+len(custom))
	for k, v := range defaultLaunchCommands {
		merged[k] = v
	}
	for k, v := range custom {
		if strings.TrimSpace(v) == "" {
			continue
		}
		merged[k] = v
	}
	return merged
}

// handleLauncherInfo GET /api/launcher
func (g *Gateway) handleLauncherInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"commands": g.resolveLaunchCommands(),
		"running":  launcherMgr.List(),
		"desktop":  launcher.DesktopApps(),
	})
}

// handleLauncherDesktopLaunch POST /api/launcher/desktop/launch
//
// 请求体：{"app":"claude_desktop"} 或 {"app":"codex_desktop"}。
// 先把网关写进桌面端的正常用户配置（自动备份），再打开应用；桌面端
// 已在运行时配置要重启后才会重新读取。
func (g *Gateway) handleLauncherDesktopLaunch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		App string `json:"app"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	app := strings.TrimSpace(body.App)
	if app == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 app"})
		return
	}
	available := false
	for _, a := range launcher.DesktopApps() {
		if a.ID == app {
			available = a.Available
			break
		}
	}
	if !available {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "该桌面应用未安装或不可用"})
		return
	}

	origin, key := g.launcherOriginKey(r)
	configPath := ""
	restartRequired := false
	var applyErr error
	switch app {
	case "claude_desktop":
		configPath, applyErr = g.applyClaudeDesktopGateway(origin, key)
		restartRequired = processRunning("claude.exe")
	case "codex_desktop":
		configPath, applyErr = g.applyCodexGateway(origin, key)
		restartRequired = processRunning("ChatGPT.exe") || processRunning("Codex.exe")
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "未知桌面应用: " + app})
		return
	}
	if applyErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "接入网关失败: " + applyErr.Error()})
		return
	}
	if err := launcher.LaunchDesktopApp(app); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"launched": true, "app": app, "configured": true,
		"config_path": configPath, "restart_required": restartRequired,
	})
}

// handleLauncherLaunch POST /api/launcher/launch
//
// 请求体（model / isolate 均可选）：
//
//	{"client_id": "claude_code", "model": "claude-sonnet-4-6", "isolate": true}
//
// 在新终端窗口拉起 client_id 对应的启动命令，并注入指向本网关的环境变量。
// isolate=true 时走「实例隔离」：先创建实例私有用户目录并把网关接入配置
// 作为种子写进去，再注入隔离环境变量启动——客户端第一启动即直连网关，
// 且配置/会话与其它实例完全隔离（launcher.IsolationEnv 的注释里有各客户端
// 隔离变量的官方出处）。
func (g *Gateway) handleLauncherLaunch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientID string `json:"client_id"`
		Model    string `json:"model"`
		Isolate  bool   `json:"isolate"`
		// Connect 为真时先把网关写进客户端的正常用户配置（自动备份），
		// 再启动。隔离启动不写全局配置（种子已经指向网关）。
		Connect bool `json:"connect"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
			return
		}
	}
	clientID := strings.TrimSpace(body.ClientID)
	if clientID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 client_id"})
		return
	}

	commands := g.resolveLaunchCommands()
	command, ok := commands[clientID]
	if !ok {
		ids := make([]string, 0, len(commands))
		for k := range commands {
			ids = append(ids, k)
		}
		sort.Strings(ids)
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "该客户端没有配置启动命令，可用 client_id: " + strings.Join(ids, ", "),
		})
		return
	}

	// origin 用当前请求的 Host（与 client_api.go 一致）：桌面端端口错开时
	// cfg.Addr 不是真实地址，浏览器能打开管理界面的地址会话就能连上。
	origin := requestOrigin(r, g.PublicAddr())
	apiKey := strings.TrimSpace(g.cfg.APIKey)
	if apiKey == "" {
		// 本地免鉴权时给非空占位值：多数客户端拒绝空 Authorization。
		apiKey = "aether-local"
	}
	model := strings.TrimSpace(body.Model)

	// 一键启动：先把网关写进客户端的正常配置；隔离实例只写种子，不动全局。
	configured, configPath := false, ""
	if body.Connect && !body.Isolate {
		var applyErr error
		switch clientID {
		case "claude_code":
			configPath, applyErr = g.applyClaudeCodeGateway(origin, apiKey)
		case "codex_cli":
			configPath, applyErr = g.applyCodexGateway(origin, apiKey)
		}
		if applyErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "接入网关失败: " + applyErr.Error()})
			return
		}
		configured = configPath != ""
	}

	// 隔离启动：目录必须先建好（种子也要写入其中），再交给 Launch。
	isolateDir, seedErr := "", ""
	if body.Isolate {
		// 没有官方隔离环境变量的客户端（如 gemini_cli）不硬造方案：
		// 隔离不彻底等于没隔离，直接拒绝并说明。
		if !launcher.IsolationSupported(clientID) {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": "该客户端暂不支持隔离目录启动（缺少官方隔离环境变量），当前支持: claude_code, codex_cli, opencode",
			})
			return
		}
		// 实例根目录相对网关工作目录，与 data/settings.json 等运行时
		// 文件同一套约定；绝对路径由 PrepareIsolation 转换后记录。
		dir, warn, err := launcher.PrepareIsolation(
			filepath.Join("data", "instances"), clientID, origin, apiKey, model)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		isolateDir, seedErr = dir, warn
	}

	inst, err := launcherMgr.Launch(clientID, command,
		launcher.BuildEnv(origin, apiKey, model),
		launcher.LaunchOptions{IsolateDir: isolateDir, SeedError: seedErr})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instance": inst, "configured": configured, "config_path": configPath,
	})
}

// handleLauncherDesktopInstance POST /api/launcher/desktop/instance
//
// 请求体：{"app":"claude_desktop"|"codex_desktop"}。桌面端用独立
// --user-data-dir 启动；启动前同样先接入本网关。若应用限制单实例，
// 启动器会在 1.2 秒内发现进程退出并返回明确错误。
func (g *Gateway) handleLauncherDesktopInstance(w http.ResponseWriter, r *http.Request) {
	var body struct {
		App string `json:"app"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	appID := strings.TrimSpace(body.App)
	var app *launcher.DesktopApp
	for _, a := range launcher.DesktopApps() {
		if a.ID == appID {
			cp := a
			app = &cp
			break
		}
	}
	if app == nil || !app.Available {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "该桌面应用未安装或不可用"})
		return
	}
	if strings.TrimSpace(app.Executable) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": app.Name + " 找不到主程序，暂时无法开隔离实例"})
		return
	}

	origin, key := g.launcherOriginKey(r)
	var applyErr error
	switch appID {
	case "claude_desktop":
		_, applyErr = g.applyClaudeDesktopGateway(origin, key)
	case "codex_desktop":
		_, applyErr = g.applyCodexGateway(origin, key)
	}
	if applyErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "接入网关失败: " + applyErr.Error()})
		return
	}

	dir, err := filepath.Abs(filepath.Join("data", "instances", appID+"-"+time.Now().Format("20060102-150405")))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "生成实例目录失败: " + err.Error()})
		return
	}
	inst, err := launcherMgr.LaunchDesktopInstance(appID, app.Executable,
		[]string{"--user-data-dir=" + dir},
		launcher.BuildEnv(origin, key, ""), dir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instance": inst, "configured": true})
}

// handleLauncherStop POST /api/launcher/{id}/stop
func (g *Gateway) handleLauncherStop(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "实例 ID 不合法"})
		return
	}
	if err := launcherMgr.Stop(id); err != nil {
		if errors.Is(err, launcher.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stopped": true})
}

// handleLauncherCleanup POST /api/launcher/{id}/cleanup
//
// 请求体可选：{"wipe": true} —— wipe 为真时把实例的隔离目录连同内容
// 一起删除（os.RemoveAll，目录不存在等同已清理）。stop 刻意保留实例
// 目录（用户可能要保留会话状态），需要"人走茶凉"式收尾时用本接口。
// 响应：{"cleaned": true, "dir": "<实例目录，非隔离实例为空>"}。
func (g *Gateway) handleLauncherCleanup(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "实例 ID 不合法"})
		return
	}
	wipe := false
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if len(raw) > 0 {
		var body struct {
			Wipe bool `json:"wipe"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
			return
		}
		wipe = body.Wipe
	}
	dir, err := launcherMgr.Cleanup(id, wipe)
	if err != nil {
		if errors.Is(err, launcher.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cleaned": true, "dir": dir})
}
