// Package launcher 实现「应用多开」：为客户端拉起带网关环境变量的
// 独立会话进程——每个实例一个新终端窗口，互不干扰，可随时停止。
//
// 与"一键接入"（internal/core/clients，改写全局配置文件）互补：接入一次
// 只能有一个网关会话；多开用环境变量隔离每个实例，同一个客户端可以
// 同时开多个会话，各自绑定不同的模型/账号环境。
//
// 三条设计约束：
//
//  1. 实例进程脱离网关：启动后立即 Release 不等待，网关退出/重启
//     不影响已拉起的会话。代价是实例表只是进程内状态，网关重启后
//     列表清空，正在运行的终端不再被追踪（仍可手动关闭）；
//  2. 环境变量只注入端点与模型相关键（ANTHROPIC_* / OPENAI_*），
//     不注入 ANTHROPIC_DEFAULT_* 系列——那组由 Claude Code 自己管理；
//  3. 命令模板支持 {MODEL} / {ORIGIN} / {API_KEY} 占位符，启动前展开，
//     同一条模板即可服务不同模型与账号。
//
// v2 追加「实例隔离」：isolate 启动时为每个实例准备私有用户目录
// （PrepareIsolation，配置种子自动写入）并注入官方隔离环境变量
// （IsolationEnv，把客户端的配置根目录指进私有目录），同一客户端的
// 多个实例不再共享全局配置，多账号真正并行。
package launcher

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/clients"
)

// ErrNotFound 表示实例 ID 不在运行表中（可能已停止，或网关重启后列表被清空）。
var ErrNotFound = errors.New("实例不存在或已退出")

// Instance 是一个运行中实例的记录。
type Instance struct {
	ID        int64  `json:"id"`
	ClientID  string `json:"client_id"`
	PID       int    `json:"pid"`
	Command   string `json:"command"`
	Model     string `json:"model,omitempty"`
	StartedAt int64  `json:"started_at"`
	// Dir 是隔离实例的私有用户目录（绝对路径）；非隔离启动为空。
	// 隔离后客户端的配置/会话/凭据全部落在该目录下，同一客户端多账号
	// 真正并行互不串扰；Stop 不删目录（用户可能要保留实例状态），由
	// Cleanup(wipe) 负责删除。
	Dir string `json:"dir,omitempty"`
	// SeedError 是启动前向实例目录注入网关接入配置（种子）时收集到的
	// 警告：种子失败不阻塞启动，客户端首启动会自己生成默认配置。
	SeedError string `json:"seed_error,omitempty"`
}

// Manager 记录本进程拉起过的实例。
type Manager struct {
	mu      sync.Mutex
	nextID  int64
	running map[int64]*Instance
}

// New 创建管理器。
func New() *Manager {
	return &Manager{running: map[int64]*Instance{}}
}

// LaunchOptions 是 Launch 的可选项。零值即"非隔离启动"，行为与 v1 一致。
type LaunchOptions struct {
	// IsolateDir 非空表示本次启动是隔离实例：Launch 会把 IsolationEnv
	// 返回的隔离变量叠加到 env 上（同名键覆盖），并把该目录记录进
	// Instance.Dir。目录由调用方先经 PrepareIsolation 创建并注入种子。
	IsolateDir string
	// SeedError 是调用方做配置种子注入时收集到的警告（可空），原样
	// 记录进 Instance.SeedError 展示给前端。
	SeedError string
}

// Launch 在新终端窗口里拉起 command。
//
// env 是叠加到当前进程环境之上的键值对（同名键以 env 为准，其余原样
// 继承——客户端 CLI 需要 PATH/HOME 等）。命令模板里的占位符用 env 中的
// ANTHROPIC_MODEL / ANTHROPIC_BASE_URL / ANTHROPIC_AUTH_TOKEN 展开，
// 返回记录里的 Command 是展开后的实际命令。
//
// opts.IsolateDir 非空时（隔离实例），隔离环境变量会在 env 之上叠加：
// 这些变量把客户端的配置根目录指进实例私有目录，是"同一客户端多账号
// 并行"的关键——没有它们，客户端仍会读写真实用户主目录里的全局配置。
func (m *Manager) Launch(clientID, command string, env map[string]string, opts LaunchOptions) (*Instance, error) {
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("启动命令为空")
	}
	if opts.IsolateDir != "" {
		// 拷贝后再叠加，避免污染调用方传入的 map（BuildEnv 每次新建，
		// 这里防御性处理，代价可忽略）。
		merged := make(map[string]string, len(env)+2)
		for k, v := range env {
			merged[k] = v
		}
		for k, v := range IsolationEnv(clientID, opts.IsolateDir) {
			merged[k] = v
		}
		env = merged
	}
	model := env["ANTHROPIC_MODEL"]
	expanded := expandCommand(command, model, env["ANTHROPIC_BASE_URL"], env["ANTHROPIC_AUTH_TOKEN"])

	pid, err := startDetached(expanded, mergeEnv(env))
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	inst := &Instance{
		ID:        m.nextID,
		ClientID:  clientID,
		PID:       pid,
		Command:   expanded,
		Model:     model,
		StartedAt: time.Now().Unix(),
		Dir:       opts.IsolateDir,
		SeedError: opts.SeedError,
	}
	m.running[inst.ID] = inst
	return inst, nil
}

// LaunchDesktopInstance 用独立用户目录启动桌面端（多开）。
//
// 桌面端没有官方隔离环境变量，但 Electron 客户端认 --user-data-dir：
// 每个实例一个独立 profile 目录，登录态/缓存互不串扰。启动后短暂等待，
// 进程若立即退出通常意味着应用限制单实例（把参数转发给已有窗口），
// 这时返回错误而不是把一条僵尸记录放进运行表。
func (m *Manager) LaunchDesktopInstance(clientID, executable string, args []string, env map[string]string, dir string) (*Instance, error) {
	if strings.TrimSpace(executable) == "" {
		return nil, fmt.Errorf("没有找到 %s 的主程序，无法启动隔离实例", clientID)
	}
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("隔离目录不能为空")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建隔离目录失败: %w", err)
	}
	pid, err := startGuiDetached(executable, args, mergeEnv(env))
	if err != nil {
		return nil, err
	}
	time.Sleep(1200 * time.Millisecond)
	if !processAlive(pid) {
		return nil, fmt.Errorf("%s 没有建立独立实例（应用可能限制单实例，或启动后立即退出）", clientID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	inst := &Instance{
		ID: m.nextID, ClientID: clientID, PID: pid,
		Command:   executable + " " + strings.Join(args, " "),
		StartedAt: time.Now().Unix(), Dir: dir,
	}
	m.running[inst.ID] = inst
	return inst, nil
}

// List 返回运行中的实例（新 -> 旧）。
func (m *Manager) List() []Instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Instance, 0, len(m.running))
	for _, inst := range m.running {
		out = append(out, *inst)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// Stop 结束指定实例的整棵进程树，并从运行表移除记录。
//
// 无论 kill 是否成功（例如用户已手动关掉终端），记录都会移除——
// 对一个已不存在的会话，"停止"的语义已经达成，错误仅用于提示。
// 注意：Stop 刻意不删实例目录（隔离实例的配置/会话状态可能还想保留），
// 删目录是 Cleanup(wipe) 的职责。
func (m *Manager) Stop(id int64) error {
	m.mu.Lock()
	inst, ok := m.running[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	delete(m.running, id)
	m.mu.Unlock()
	return killTree(inst.PID)
}

// Cleanup 收尾指定实例：尽力结束其进程树（进程可能已自行退出，kill
// 失败不视为错误）、从运行表移除记录；wipe 为真且实例带隔离目录时把
// 目录一并删除（os.RemoveAll：目录不存在等同已清理，不报错）。
//
// 返回实例目录路径（非隔离实例为空串），供 API 回显给前端。
func (m *Manager) Cleanup(id int64, wipe bool) (string, error) {
	m.mu.Lock()
	inst, ok := m.running[id]
	if !ok {
		m.mu.Unlock()
		return "", ErrNotFound
	}
	delete(m.running, id)
	dir := inst.Dir
	m.mu.Unlock()

	_ = killTree(inst.PID) // 无人照看的收尾：进程死了正好，不用上报
	if wipe && dir != "" {
		if err := os.RemoveAll(dir); err != nil {
			return dir, fmt.Errorf("删除实例目录失败: %w", err)
		}
	}
	return dir, nil
}

// BuildEnv 组装要注入实例的环境变量。
//
// origin 形如 http://127.0.0.1:7860（末尾斜杠自动去掉）；apiKey 原样
// 使用——本地免鉴权时由调用方传占位值（与 client_api.go 一致用
// "aether-local"），多数客户端会拒绝空 Authorization。model 非空时额外
// 注入 ANTHROPIC_MODEL 与 OPENAI_MODEL；刻意不注入 ANTHROPIC_DEFAULT_*
// 系列，那组由 Claude Code 自己管理。
func BuildEnv(origin, apiKey, model string) map[string]string {
	origin = strings.TrimRight(origin, "/")
	env := map[string]string{
		"ANTHROPIC_BASE_URL":   origin,
		"ANTHROPIC_AUTH_TOKEN": apiKey,
		// 有些 Claude Code 版本只认 API_KEY；同值再给一份，避免终端里
		// 仍然去连官方端点。
		"ANTHROPIC_API_KEY": apiKey,
		"OPENAI_BASE_URL":   origin + "/v1",
		"OPENAI_API_KEY":    apiKey,
	}
	if model != "" {
		env["ANTHROPIC_MODEL"] = model
		env["OPENAI_MODEL"] = model
	}
	return env
}

// --- 实例隔离（v2） ---

// isolationDef 描述一个客户端的隔离方案：往实例进程注入哪个环境变量、
// 该客户端的"配置根目录"相对实例目录的位置。
//
// 变量取值一律是 <实例目录>/<客户端点目录>（而非实例目录本身）：客户端
// 的配置根就是真实主目录里的那个点目录（~/.claude、~/.codex、~/.config），
// 而"配置种子"统一由 clients.NewWithHome(<实例目录>).Apply 写到
// <实例目录>/<同一个点目录>/ 下——两边必须指向同一处，种子才会被
// 客户端第一启动读到。
type isolationDef struct {
	envKey string // 注入实例进程的环境变量名
	subDir string // 客户端配置根目录相对实例目录的位置
}

// isolationDefs 各客户端的隔离变量与其官方出处（2026-10 核实）：
//
//   - claude_code → CLAUDE_CONFIG_DIR，官方文档明确 "To keep the
//     home-directory files somewhere else, set CLAUDE_CONFIG_DIR; Claude
//     Code then stores your settings, session history, and plugins there
//     instead"（即 $CLAUDE_CONFIG_DIR 等价于 ~/.claude 本身，settings.json
//     位于 $CLAUDE_CONFIG_DIR/settings.json）：
//     https://code.claude.com/docs/en/settings
//
//   - codex_cli → CODEX_HOME，官方环境变量文档："Sets the root for Codex
//     state, including config, auth, logs, sessions, skills, and standalone
//     package metadata"（默认 ~/.codex，config.toml 位于 $CODEX_HOME 下；
//     另有要求"目录必须已存在"——PrepareIsolation 会先 MkdirAll）：
//     https://learn.chatgpt.com/docs/config-file/environment-variables
//
//   - opencode → XDG_CONFIG_HOME，opencode 全局配置按 XDG 约定查找：
//     设置了 $XDG_CONFIG_HOME 时读 $XDG_CONFIG_HOME/opencode/opencode.json
//     （未设置时 ~/.config 即 XDG 默认值）。官方配置文档只写了默认路径
//     ~/.config/opencode/opencode.json（https://opencode.ai/docs/config/），
//     XDG 覆盖行为由源码与社区复现确认：
//     https://github.com/anomalyco/opencode/issues/6669
//
//   - gemini_cli 刻意缺席：官方 settings 文档把用户配置固定在
//     ~/.gemini（https://github.com/google-gemini/gemini-cli 的
//     docs/cli/settings.md），没有任何可迁移配置目录的环境变量
//     （GEMINI_CLI_SYSTEM_SETTINGS_PATH 只改"系统级"设置文件路径，
//     .gemini 目录可配置化是未实现的 feature request #2815）。
//     不瞎猜变量名——该客户端走"暂不支持隔离"，由 API 层返回 400。
var isolationDefs = map[string]isolationDef{
	"claude_code": {envKey: "CLAUDE_CONFIG_DIR", subDir: ".claude"},
	"codex_cli":   {envKey: "CODEX_HOME", subDir: ".codex"},
	"opencode":    {envKey: "XDG_CONFIG_HOME", subDir: ".config"},
}

// IsolationSupported 报告 client_id 是否定义了隔离方案（有对应官方
// 隔离环境变量）。API 层用它拒绝暂不支持的客户端。
func IsolationSupported(clientID string) bool {
	_, ok := isolationDefs[clientID]
	return ok
}

// IsolationEnv 返回把 client_id 的配置根目录指进隔离目录 dir 所需的
// 环境变量（键值均可用 mergeEnv 直接叠加：同名键会覆盖父环境，用户
// 全局设置的 XDG_CONFIG_HOME 等不会泄漏进隔离实例）。
//
// dir 为空或该客户端没有隔离方案（isolationDefs 无此 id）时返回空 map
// ——调用方据此判断"不支持隔离"。
func IsolationEnv(clientID, dir string) map[string]string {
	def, ok := isolationDefs[clientID]
	if !ok || strings.TrimSpace(dir) == "" {
		return map[string]string{}
	}
	return map[string]string{def.envKey: filepath.Join(dir, def.subDir)}
}

// PrepareIsolation 为一次隔离启动准备实例私有目录：
//
//  1. 在 baseDir（如 <网关工作目录>/data/instances）下创建
//     <client_id>/<时间戳>-<短随机>/，MkdirAll 保证存在；
//  2. 用 clients.NewWithHome(<实例目录>) 把网关接入配置作为"种子"写进
//     实例目录（配合 IsolationEnv 注入的隔离变量，客户端第一启动即直连
//     网关，无需用户再配置）。
//
// 返回实例目录的绝对路径与种子注入警告。种子失败不阻塞启动（返回的
// seedWarning 交给 LaunchOptions.SeedError 展示）：客户端首启动会自己
// 生成默认配置，隔离性不受影响；但目录创建失败必须中止启动。
func PrepareIsolation(baseDir, clientID, origin, apiKey, model string) (dir string, seedWarning string, err error) {
	dir = filepath.Join(baseDir, clientID, time.Now().Format("20060102-150405")+"-"+randSuffix())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("创建隔离实例目录失败: %w", err)
	}
	// 转绝对路径：Instance.Dir 与隔离环境变量都要求绝对路径（子进程的
	// 工作目录不保证是网关工作目录，相对路径会指错地方）。
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	// 种子注入：复用"一键接入"的改写逻辑，但 home 指向实例目录——
	// 全部文件落在实例目录内，用户的真实全局配置零接触。
	cm := clients.NewWithHome(dir)
	if _, err := cm.Apply(clientID, clients.ApplyParams{
		BaseURL: origin,
		APIKey:  apiKey,
		Model:   model,
	}); err != nil {
		return dir, fmt.Sprintf("配置种子注入失败（客户端将使用自生成的默认配置）: %v", err), nil
	}
	return dir, "", nil
}

// randSuffix 生成 6 位十六进制短随机串，避免同一秒内多次启动撞目录名。
func randSuffix() string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "000000" // crypto/rand 失败极罕见；退化为固定后缀，时间戳仍能区分
	}
	return hex.EncodeToString(b[:])
}

// expandCommand 展开命令模板里的 {MODEL} / {ORIGIN} / {API_KEY} 占位符
// （可多次出现；对应值为空时展开成空串）。
func expandCommand(tmpl, model, origin, apiKey string) string {
	return strings.NewReplacer(
		"{MODEL}", model,
		"{ORIGIN}", origin,
		"{API_KEY}", apiKey,
	).Replace(tmpl)
}

// mergeEnv 把 extra 叠加到当前进程环境上：父环境里与 extra 同名的键
// 丢弃，extra 的值生效，其余键原样保留。
func mergeEnv(extra map[string]string) []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		if i := strings.IndexByte(kv, '='); i > 0 {
			if _, hit := extra[kv[:i]]; hit {
				continue
			}
		}
		out = append(out, kv)
	}
	// 固定遍历顺序，保证产出环境块的形态确定（利于测试与排查）。
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+extra[k])
	}
	return out
}
