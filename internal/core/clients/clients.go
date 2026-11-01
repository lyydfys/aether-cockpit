// Package clients 负责把网关端点"一键注入"到本机 AI 客户端的配置文件里。
//
// 与"生成片段让用户自己粘贴"相比，直接写文件是 aether 类工具的核心体验：
// 一次点击完成接入，再点一次恢复原状。为把风险压到最低，这里坚持三条：
//
//  1. 写之前必须先备份（<file>.aether-bak-<时间戳>），保留最近 5 份；
//  2. 只碰"受管理"的字段：JSON 合并 env 键、TOML 只增删自己的 provider 块，
//     绝不重排或重写用户的其它配置；
//  3. 恢复 = 原样拷回备份文件，不做任何"聪明"的合并。
package clients

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Kind 是客户端配置文件的类型。
type Kind string

const (
	KindJSON    Kind = "json"
	KindTOML    Kind = "toml"
	KindEnvFile Kind = "envfile"
)

// 备份文件的后缀与保留数量。
const (
	backupSuffix = ".aether-bak-"
	maxBackups   = 5
)

// ApplyParams 是一次注入所需的参数。
type ApplyParams struct {
	// BaseURL 形如 http://127.0.0.1:7860（不带末尾斜杠）。
	BaseURL string
	// APIKey 网关的管理/调用 Key。本地免鉴权时由调用方填占位值。
	APIKey string
	// Model 可选：客户端的默认模型（各客户端字段不同，由 applier 解释）。
	Model string
	// ModelOptions 是网关当前可选的模型/路由组，写入支持模型选择器的客户端
	// （目前是 Claude Code 的 settings.json modelPicker）。为空则不写。
	ModelOptions []ModelOption
	// CodexCatalog 是写进 config.toml 的 catalog 文件名（仅 Codex）。
	CodexCatalog string
	// CodexBearerToken 非空时，Codex provider 块直接写入
	// experimental_bearer_token，而不是要求外部 AETHER_API_KEY 环境变量。
	// 桌面端无法可靠继承网关进程的环境变量，一键启动时用它完成接入。
	CodexBearerToken string
}

// ModelOption 是模型选择器的一行。
type ModelOption struct {
	Value       string
	Label       string
	Description string
}

// ManagedInfo 描述当前文件里受管理字段的状态（不回显密钥）。
type ManagedInfo struct {
	BaseURL string `json:"base_url,omitempty"`
	Model   string `json:"model,omitempty"`
}

// Backup 是一份备份记录。
type Backup struct {
	File string `json:"file"`
	Time int64  `json:"time"`
}

// Status 是一个客户端的接入状态。
type Status struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Kind     string       `json:"kind"`
	Path     string       `json:"path"`
	Detected bool         `json:"detected"`
	Managed  *ManagedInfo `json:"managed,omitempty"`
	Backups  []Backup     `json:"backups"`
	// Note 面向前端的补充说明（例如需要设置的环境变量）。
	Note string `json:"note,omitempty"`
	// WSL 非空表示这一条是 WSL 发行版里的客户端（值为发行版名），
	// Path 是 \\wsl.localhost\... 形式的 UNC 路径。
	WSL string `json:"wsl,omitempty"`
	// Target 是注入目标 id（WSL 条目才有）；同一条目在 Windows 侧
	// 与 WSL 侧各有一份，id 不同。
	Target string `json:"target,omitempty"`
}

// Target 是一个额外的注入目标（目前只有 WSL 发行版）：
// 同一套客户端定义在另一个家目录里再列一遍，id 带前缀以便区分。
type Target struct {
	// ID 是目标前缀（如 "wsl-ubuntu-22-04"）；客户端条目的 id 为
	// "<ID>--<client>"，避免与 Windows 本机条目撞名。
	ID    string `json:"id"`
	Label string `json:"label"`
	// Home 是目标家目录（UNC 路径或普通目录）。
	Home string `json:"home"`
	// WSL 是发行版名（非 WSL 目标为空）。
	WSL string `json:"wsl,omitempty"`
}

// SetTargets 替换额外注入目标。家目录变化（WSL 重启后 IP 变了、用户
// 装了新发行版）时调用即可，已有备份与配置不受影响。
//
// 目标没变时不做任何事：这个函数会被客户端列表的每次读取调用，
// 每次都清缓存会让 UNC 上的 stat 白做（实测 /api/clients 因此超时）。
func (m *Manager) SetTargets(ts []Target) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	if sameTargets(m.targets, ts) {
		return
	}
	m.targets = append([]Target(nil), ts...)
	m.cache = nil
}

// sameTargets 比较两组目标（id / 家目录 / 发行版）。
func sameTargets(a, b []Target) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Home != b[i].Home || a[i].WSL != b[i].WSL {
			return false
		}
	}
	return true
}

// Targets 返回当前额外目标。
func (m *Manager) Targets() []Target {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	return append([]Target(nil), m.targets...)
}

// TargetOf 返回某条目所属的额外目标（本机条目返回 false）。
func (m *Manager) TargetOf(id string) (Target, bool) {
	d, ok := m.def(id)
	if !ok || d.target == nil {
		return Target{}, false
	}
	return *d.target, true
}

// targetStatusTTL 是 WSL 条目状态的缓存时长：UNC 上的 stat/glob 不算便宜，
// 而客户端列表会被 Agents 页反复读取。写入后会主动失效（见 forgetStatus）。
const targetStatusTTL = 15 * time.Second

type cachedStatus struct {
	st Status
	at time.Time
}

// clientDef 是一个客户端的静态定义。
type clientDef struct {
	id      string
	name    string
	kind    Kind
	relPath string
	note    string
	// apply / managed 是可选的自定义钩子：为 nil 时按 kind 走默认分派。
	// 同为 JSON 的客户端 schema 各不相同（Claude 合并 env 键、opencode 合并
	// provider 条目），因此为这类客户端提供独立的合并逻辑入口，而不把
	// applyJSON 越改越复杂；现有三个客户端不受影响。
	apply   func(content []byte, p ApplyParams) ([]byte, error)
	managed func(data []byte) *ManagedInfo
	// pathFn 可选：客户端配置目录由环境变量覆盖（如 QWEN_HOME），
	// 返回完整文件路径；为 nil 时按 home + relPath 解析。
	pathFn func(home string) string
	// aux 是该客户端的附属配置文件（如 Cline 的 globalState/secrets）：
	// Apply 一并写入并各自留备份，Restore 一并还原。
	aux []auxFile
	// restoreHook 在 Restore 覆盖主文件之前调用（参数为当前主文件路径），
	// 用于清理外部状态（例如 Zed 写在系统凭据里的 Key）。
	restoreHook func(path string)
	env         *envKeys
	absPath     string
	custom      bool
	// target 非空表示这条定义属于某个额外目标（WSL 发行版）。
	target *Target
}

// auxFile 是附属于主配置的第二个文件。
type auxFile struct {
	relPath string
	// onlyIfExists 为 true 时，文件不存在就跳过（例如只在扩展安装后才有的
	// globalState.json）。
	onlyIfExists bool
	pathFn       func(home string) string
	render       func(existing []byte, p ApplyParams) ([]byte, error)
}

// envKeys 是 envfile 类客户端的键名与 base_url 后缀。
type envKeys struct {
	baseURL string
	apiKey  string
	model   string
	suffix  string
}

func defaultEnvKeys() *envKeys {
	return &envKeys{
		baseURL: "GOOGLE_GEMINI_BASE_URL",
		apiKey:  "GEMINI_API_KEY",
		model:   "GEMINI_MODEL",
		suffix:  "/v1beta",
	}
}

func (d clientDef) effectiveEnvKeys() *envKeys {
	if d.env != nil {
		return d.env
	}
	return defaultEnvKeys()
}

// defs 全部支持的客户端。Gemini CLI 的 .env 指向网关 /v1beta 端点。
var defs = []clientDef{
	{
		id:      "claude_code",
		name:    "Claude Code",
		kind:    KindJSON,
		relPath: filepath.Join(".claude", "settings.json"),
		note:    "合并写入 settings.json 的 env 键；应用后需要重启 Claude Code 会话。",
	},
	{
		id:      "codex_cli",
		name:    "Codex CLI",
		kind:    KindTOML,
		relPath: filepath.Join(".codex", "config.toml"),
		note:    "写入 model_provider 与 [model_providers.aether] 块。Codex 从环境变量 AETHER_API_KEY 读取密钥，请自行执行一次：setx AETHER_API_KEY <key>。",
	},
	{
		id:      "gemini_cli",
		name:    "Gemini CLI",
		kind:    KindEnvFile,
		relPath: filepath.Join(".gemini", ".env"),
		note:    "写入 ~/.gemini/.env（GOOGLE_GEMINI_BASE_URL / GEMINI_API_KEY），应用后需要重启 Gemini CLI。",
	},
	{
		// MiMo Code（小米）是 opencode 的同源 fork：配置文件在
		// ~/.config/mimocode/mimocode.jsonc，provider schema 与 opencode 一致
		// （provider.<id>.npm / models / options.baseURL）。
		// 它自带的 provider 用 @ai-sdk/openai，因此沿用同一个包名。
		// 带注释的 JSONC 无法安全合并，会拒绝改写并给出提示。
		id:      "mimocode",
		name:    "MiMo Code",
		kind:    KindJSON,
		relPath: filepath.Join(".config", "mimocode", "mimocode.jsonc"),
		note:    "写入 ~/.config/mimocode/mimocode.jsonc 的 provider.aether 条目（JSONC 含注释时拒绝改写）；应用后需重启 MiMo Code。",
		apply:   openCodeStyleApplier("@ai-sdk/openai", "mimocode.jsonc"),
		managed: managedOpenCode,
	},
	{
		// 配置路径与自定义 provider 的 schema 依据官方文档（2026-10 核实）：
		//   https://opencode.ai/docs/config/    —— 全局配置为 ~/.config/opencode/opencode.json
		//   https://opencode.ai/docs/providers/ —— 自定义 OpenAI 兼容 provider：
		//     provider.<id>.npm = "@ai-sdk/openai-compatible"（/v1/chat/completions 适配器）、
		//     provider.<id>.options.baseURL / options.apiKey；顶层 "model" 形如 "<provider>/<model-id>"。
		id:      "opencode",
		name:    "opencode",
		kind:    KindJSON,
		relPath: filepath.Join(".config", "opencode", "opencode.json"),
		note:    "写入 ~/.config/opencode/opencode.json：新增/覆盖 provider.aether（OpenAI 兼容适配器），Model 非空时把默认 model 设为 aether/<model>，其余配置原样保留。应用后需要重启 opencode。若该文件带注释（JSONC）会拒绝改写。",
		apply:   applyOpenCode,
		managed: managedOpenCode,
	},
}

// Manager 管理全部客户端定义与备份。
type Manager struct {
	home   string
	custom []clientDef

	// 额外目标（WSL 发行版）及其状态缓存。
	cacheMu sync.Mutex
	targets []Target
	cache   map[string]cachedStatus
}

// New 创建管理器，自动定位用户主目录。
func New() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Manager{home: home}, nil
}

// NewWithHome 用指定主目录创建（测试用）。
func NewWithHome(home string) *Manager { return &Manager{home: home} }

func (m *Manager) pathOf(d clientDef) string {
	if d.absPath != "" {
		return d.absPath
	}
	if d.pathFn != nil {
		return d.pathFn(m.home)
	}
	return filepath.Join(m.home, d.relPath)
}

// List 返回全部客户端状态。
func (m *Manager) List() []Status {
	all := m.allDefs()
	out := make([]Status, 0, len(all))
	for _, d := range all {
		out = append(out, m.statusOf(d))
	}
	return out
}

// statusOf 返回条目状态；额外目标（WSL）的状态短暂缓存，避免 Agents 页
// 每次刷新都在 UNC 上逐个 stat。
func (m *Manager) statusOf(d clientDef) Status {
	if d.target == nil {
		return m.statusOfUncached(d)
	}
	m.cacheMu.Lock()
	if m.cache != nil {
		if c, ok := m.cache[d.id]; ok && time.Since(c.at) < targetStatusTTL {
			m.cacheMu.Unlock()
			return c.st
		}
	}
	m.cacheMu.Unlock()

	st := m.statusOfUncached(d)
	m.cacheMu.Lock()
	if m.cache == nil {
		m.cache = map[string]cachedStatus{}
	}
	m.cache[d.id] = cachedStatus{st: st, at: time.Now()}
	m.cacheMu.Unlock()
	return st
}

// forgetStatus 丢掉某条目的状态缓存（写入/还原后必须立刻反映真实状态）。
func (m *Manager) forgetStatus(id string) {
	m.cacheMu.Lock()
	delete(m.cache, id)
	m.cacheMu.Unlock()
}

func (m *Manager) statusOfUncached(d clientDef) Status {
	path := m.pathOf(d)
	st := Status{
		ID:       d.id,
		Name:     d.name,
		Kind:     string(d.kind),
		Path:     path,
		Detected: fileExists(path),
		Note:     d.note,
		Backups:  m.listBackups(path),
	}
	if d.target != nil {
		st.WSL = d.target.WSL
		st.Target = d.target.ID
	}
	if st.Detected {
		st.Managed = m.managedOf(d, path)
	}
	return st
}

// listBackups 列出备份（新 -> 旧），只保留记录，不在这里删文件。
func (m *Manager) listBackups(path string) []Backup {
	matches, _ := filepath.Glob(path + backupSuffix + "*")
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))
	out := make([]Backup, 0, len(matches))
	for _, f := range matches {
		base := filepath.Base(f)
		i := strings.LastIndex(base, backupSuffix)
		raw := base[i+len(backupSuffix):]
		// 首次连接时目标文件并不存在，备份是一个 "…-created" 标记：
		// 断开时应当删除文件，而不是写回空内容。
		raw = strings.TrimSuffix(raw, "-created")
		ts, err := time.Parse("20060102-150405", raw)
		if err != nil {
			continue
		}
		out = append(out, Backup{File: f, Time: ts.Unix()})
	}
	return out
}

// backup 把当前文件复制为带时间戳的备份，并裁剪到 maxBackups 份。
func (m *Manager) backup(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// 目标不存在：留下一个"创建前为空"的标记，断开时据此删除文件。
			if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
				return "", mkErr
			}
			bak := path + backupSuffix + time.Now().Format("20060102-150405") + "-created"
			if wErr := os.WriteFile(bak, nil, 0o600); wErr != nil {
				return "", wErr
			}
			return bak, nil
		}
		return "", err
	}
	bak := path + backupSuffix + time.Now().Format("20060102-150405")
	if err := os.WriteFile(bak, data, 0o600); err != nil {
		return "", err
	}
	// 裁剪旧备份。
	old := m.listBackups(path)
	if len(old) > maxBackups {
		for _, b := range old[maxBackups:] {
			_ = os.Remove(b.File)
		}
	}
	return bak, nil
}

// Apply 把网关端点写入指定客户端的配置文件，返回备份文件路径。
func (m *Manager) Apply(id string, p ApplyParams) (string, error) {
	d, ok := m.def(id)
	if !ok {
		return "", fmt.Errorf("未知客户端: %s", id)
	}
	if strings.TrimSpace(p.BaseURL) == "" {
		return "", fmt.Errorf("BaseURL 不能为空")
	}
	p.BaseURL = strings.TrimRight(p.BaseURL, "/")

	path := m.pathOf(d)
	bak, err := m.backup(path)
	if err != nil {
		return "", fmt.Errorf("备份失败: %w", err)
	}

	var content []byte
	if fileExists(path) {
		content, err = os.ReadFile(path)
		if err != nil {
			return "", err
		}
		// Windows 记事本 / PowerShell 写出的 JSON、TOML 常带 UTF-8 BOM。
		// 严格解析器会直接拒绝，用户只会看到"配置无法解析"。
		content = trimBOM(content)
	}
	if d.kind == KindTOML && len(p.ModelOptions) > 0 {
		existing := tomlTopValue(content, "model_catalog_json")
		if existing == "" || existing == codexCatalogName {
			if err := writeCodexCatalog(filepath.Join(filepath.Dir(path), codexCatalogName), p.ModelOptions); err != nil {
				return "", err
			}
			p.CodexCatalog = codexCatalogName
		}
	}

	out, err := m.renderFor(d, content, p)
	if err != nil {
		return "", err
	}

	// 附属文件先全部渲染成功再落盘，避免主文件改了、附属文件报错的中途状态。
	type auxWrite struct {
		path string
		data []byte
	}
	var writes []auxWrite
	for _, a := range d.aux {
		apath := m.auxPath(d, a)
		exists := fileExists(apath)
		if a.onlyIfExists && !exists {
			continue
		}
		var existing []byte
		if exists {
			existing, err = os.ReadFile(apath)
			if err != nil {
				return "", err
			}
			existing = trimBOM(existing)
		}
		adata, rerr := a.render(existing, p)
		if rerr != nil {
			return "", rerr
		}
		writes = append(writes, auxWrite{path: apath, data: adata})
	}
	// 备份所有将被改写的文件（含主文件，已在上面备份过）。
	for _, w := range writes {
		if _, err := m.backup(w.path); err != nil {
			return "", fmt.Errorf("备份失败(%s): %w", filepath.Base(w.path), err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := atomicWrite(path, out); err != nil {
		return "", err
	}
	for _, w := range writes {
		if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
			return "", err
		}
		if err := atomicWrite(w.path, w.data); err != nil {
			return "", err
		}
	}
	m.forgetStatus(id)
	return bak, nil
}

// auxPath 解析附属文件路径。
func (m *Manager) auxPath(d clientDef, a auxFile) string {
	if a.pathFn != nil {
		return a.pathFn(m.home)
	}
	return filepath.Join(m.home, a.relPath)
}

// renderFor 计算 Apply 会写出的内容，不落盘（preview 与 Apply 共用）。
func (m *Manager) renderFor(d clientDef, content []byte, p ApplyParams) ([]byte, error) {
	var out []byte
	var err error
	switch {
	case d.apply != nil:
		out, err = d.apply(content, p)
	default:
		switch d.kind {
		case KindJSON:
			out, err = applyJSON(content, p)
		case KindTOML:
			out, err = applyTOML(content, p)
		case KindEnvFile:
			out, err = applyEnvFile(content, p, d.effectiveEnvKeys())
		default:
			err = fmt.Errorf("未知配置类型: %s", d.kind)
		}
	}
	if err != nil {
		return nil, err
	}
	// 写前校验：先保证原文件本身可解析，再校验改写结果。
	if d.apply == nil {
		switch d.kind {
		case KindTOML:
			if len(content) > 0 {
				if err := validateTOML(content); err != nil {
					return nil, fmt.Errorf("现有配置不是合法 TOML，已拒绝改写（请先修好或换一个文件）: %w", err)
				}
			}
			if err := validateTOML(out); err != nil {
				return nil, fmt.Errorf("改写结果不是合法 TOML，已拒绝写入: %w", err)
			}
		case KindEnvFile:
			if err := validateEnvFile(out); err != nil {
				return nil, fmt.Errorf("改写结果不是合法 env 文件，已拒绝写入: %w", err)
			}
		}
	}
	return out, nil
}

// Restore 用备份文件覆盖当前配置。backup 为空时选最新一份。
func (m *Manager) Restore(id, backup string) error {
	d, ok := m.def(id)
	if !ok {
		return fmt.Errorf("未知客户端: %s", id)
	}
	path := m.pathOf(d)
	if d.restoreHook != nil {
		d.restoreHook(path)
	}
	if backup == "" {
		bs := m.listBackups(path)
		if len(bs) == 0 {
			return fmt.Errorf("没有可用的备份")
		}
		backup = bs[0].File
	}
	// 首次连接创建的文件：删除即可，但**不能提前 return**——附属文件
	// 也需要还原（Goose 的 config.yaml 就是应用时新建的）。
	if strings.HasSuffix(filepath.Base(backup), "-created") {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("删除首次连接创建的文件失败: %w", err)
		}
	} else {
		data, err := os.ReadFile(backup)
		if err != nil {
			return fmt.Errorf("读取备份失败: %w", err)
		}
		if err := atomicWrite(path, data); err != nil {
			return err
		}
	}
	// 附属文件：各自取最新备份还原；"-created" 标记表示应用前不存在，删掉。
	for _, a := range d.aux {
		apath := m.auxPath(d, a)
		bs := m.listBackups(apath)
		if len(bs) == 0 {
			continue
		}
		latest := bs[0].File
		if strings.HasSuffix(filepath.Base(latest), "-created") {
			_ = os.Remove(apath)
			continue
		}
		if adata, rerr := os.ReadFile(latest); rerr == nil {
			_ = atomicWrite(apath, adata)
		}
	}
	// Codex：断开后 catalog 文件不再被 config 引用，删掉我们写的那份。
	if d.kind == KindTOML {
		catPath := filepath.Join(filepath.Dir(path), codexCatalogName)
		if _, err := os.Stat(catPath); err == nil {
			_ = os.Remove(catPath)
		}
	}
	m.forgetStatus(id)
	return nil
}

func (m *Manager) def(id string) (clientDef, bool) {
	for _, d := range m.allDefs() {
		if d.id == id {
			return d, true
		}
	}
	return clientDef{}, false
}

// Lookup 判断客户端 ID 是否已定义，并返回其状态。
func (m *Manager) Lookup(id string) (Status, bool) {
	d, ok := m.def(id)
	if !ok {
		return Status{}, false
	}
	return m.statusOf(d), true
}

// allDefs 返回内置 + 自定义定义，再为每个额外目标（WSL）复制一份：
// id 带 "<target>--" 前缀，路径指向该目标家目录。
func (m *Manager) allDefs() []clientDef {
	base := defs
	if len(m.custom) > 0 {
		base = make([]clientDef, 0, len(defs)+len(m.custom))
		base = append(base, defs...)
		base = append(base, m.custom...)
	}
	m.cacheMu.Lock()
	targets := append([]Target(nil), m.targets...)
	m.cacheMu.Unlock()
	if len(targets) == 0 {
		return base
	}
	out := make([]clientDef, 0, len(base)*(1+len(targets)))
	out = append(out, base...)
	for _, t := range targets {
		tt := t
		for _, d := range base {
			// 自定义客户端可能是绝对路径，没有"相对家目录"的语义，不复制。
			if d.absPath != "" {
				continue
			}
			c := d
			c.id = tt.ID + "--" + d.id
			c.name = d.name + " · " + tt.Label
			c.absPath = filepath.Join(tt.Home, d.relPath)
			c.note = d.note + "（目标：" + tt.Label + "）"
			c.target = &tt
			out = append(out, c)
		}
	}
	return out
}

// managedOf 探测当前文件里的受管理字段。
func (m *Manager) managedOf(d clientDef, path string) *ManagedInfo {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	data = trimBOM(data)
	if d.managed != nil {
		return d.managed(data)
	}
	switch d.kind {
	case KindEnvFile:
		keys := d.effectiveEnvKeys()
		mi := &ManagedInfo{}
		for _, line := range strings.Split(string(data), "\n") {
			t := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
			if v, ok := strings.CutPrefix(t, keys.baseURL+"="); ok {
				mi.BaseURL = strings.Trim(v, `"'`)
			}
			if keys.model != "" {
				if v, ok := strings.CutPrefix(t, keys.model+"="); ok {
					mi.Model = strings.Trim(v, `"'`)
				}
			}
		}
		if mi.BaseURL == "" && mi.Model == "" {
			return nil
		}
		return mi
	case KindJSON:
		var root struct {
			Env map[string]string `json:"env"`
		}
		if json.Unmarshal(data, &root) == nil && root.Env != nil {
			mi := &ManagedInfo{BaseURL: root.Env["ANTHROPIC_BASE_URL"], Model: root.Env["ANTHROPIC_MODEL"]}
			if mi.BaseURL == "" && mi.Model == "" {
				return nil
			}
			return mi
		}
	case KindTOML:
		text := string(data)
		if strings.Contains(text, "[model_providers.aether]") {
			mi := &ManagedInfo{}
			for _, line := range strings.Split(text, "\n") {
				t := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
				if strings.HasPrefix(t, "model_provider =") && strings.Contains(t, "aether") {
					mi.BaseURL = "managed"
				}
				if strings.HasPrefix(t, "base_url =") {
					mi.BaseURL = strings.Trim(strings.TrimPrefix(t, "base_url ="), `"'`)
				}
				if strings.HasPrefix(t, "model =") && mi.Model == "" {
					mi.Model = strings.Trim(strings.TrimPrefix(t, "model ="), `"'`)
				}
			}
			return mi
		}
	}
	return nil
}

// --- 各类型的改写逻辑 ---

// applyJSON 合并写 JSON：只动 env 键，其余字段原样保留。
// 注意：Go 的 json 重编码会规范化键序与缩进——settings.json 本就是
// 机器管理的文件（无注释），这个代价可接受，且备份可完整还原。
func applyJSON(content []byte, p ApplyParams) ([]byte, error) {
	root := map[string]any{}
	if len(strings.TrimSpace(string(content))) > 0 {
		if err := json.Unmarshal(content, &root); err != nil {
			return nil, fmt.Errorf("现有 settings.json 不是合法 JSON，拒绝改写（请先修复或删除该文件）: %w", err)
		}
	}
	env, _ := root["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
		root["env"] = env
	}
	env["ANTHROPIC_BASE_URL"] = p.BaseURL
	if p.APIKey != "" {
		env["ANTHROPIC_AUTH_TOKEN"] = p.APIKey
	}
	if len(p.ModelOptions) > 0 {
		writeClaudePicker(root, p.ModelOptions)
	}
	if p.Model != "" {
		env["ANTHROPIC_MODEL"] = p.Model
		// Claude Code 的 opus/sonnet/haiku 别名与子代理会按 tier 变量取模型；
		// 不设置时它们会绕过网关写死的模型名，上游无法解析。统一指到所选模型。
		for _, key := range []string{
			"ANTHROPIC_DEFAULT_OPUS_MODEL",
			"ANTHROPIC_DEFAULT_SONNET_MODEL",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL",
			"ANTHROPIC_SMALL_FAST_MODEL",
		} {
			env[key] = p.Model
		}
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// writeClaudePicker 写 Claude Code 的 /model 列表（settings.json modelPicker）。
// 用户自己写的 modelPicker（没有任何 "via Aether Cockpit" 行）原样保留；
// 只有不存在或整体是我们的才更新，避免覆盖手工配置。
func writeClaudePicker(root map[string]any, opts []ModelOption) {
	const mark = "via Aether Cockpit"
	if cur, ok := root["modelPicker"].(map[string]any); ok {
		rows, _ := cur["options"].([]any)
		if len(rows) > 0 {
			ours := true
			for _, r := range rows {
				m, _ := r.(map[string]any)
				d, _ := m["description"].(string)
				if !strings.Contains(d, mark) {
					ours = false
					break
				}
			}
			if !ours {
				return
			}
		}
	}
	rows := make([]any, 0, len(opts))
	for _, o := range opts {
		if strings.TrimSpace(o.Value) == "" {
			continue
		}
		label := o.Label
		if label == "" {
			label = o.Value
		}
		desc := strings.TrimSpace(o.Description)
		if desc == "" {
			desc = "Aether Cockpit"
		}
		if !strings.Contains(desc, mark) {
			desc += " · " + mark
		}
		rows = append(rows, map[string]any{"model": o.Value, "label": label, "description": desc})
	}
	if len(rows) == 0 {
		return
	}
	root["modelPicker"] = map[string]any{"options": rows}
}

// applyOpenCode 合并写 opencode 的全局配置（~/.config/opencode/opencode.json）：
// 只新增/覆盖 provider.aether 条目（旧的同名条目整体替换），并在 Model 非空时
// 把顶层 "model" 设为 "aether/<Model>"；用户的其它 provider 与任意配置原样保留。
//
// schema 依据官方文档（2026-10 核实）：https://opencode.ai/docs/providers/ ——
// 自定义 OpenAI 兼容 provider 用 npm = "@ai-sdk/openai-compatible" 适配器 +
// options.baseURL / options.apiKey；顶层 "model" 形如 "<provider>/<model-id>"。
//
// 注意：opencode.json 官方允许 JSONC（带注释）。带注释的文件无法安全合并，
// 与 applyJSON 一致，这里选择拒绝改写而不是破坏用户文件。
func applyOpenCode(content []byte, p ApplyParams) ([]byte, error) {
	return openCodeStyleApplier("@ai-sdk/openai-compatible", "opencode.json")(content, p)
}

// openCodeStyleApplier 是 opencode 系（opencode / mimocode 等同源 fork）的通用
// provider 合并器：写入 provider.aether，保留用户自己的其它 provider。
//
// npm 由调用方指定——同源 fork 各自内置的适配器包不同，写错包名会让客户端起不来。
func openCodeStyleApplier(npm, fileLabel string) func([]byte, ApplyParams) ([]byte, error) {
	return func(content []byte, p ApplyParams) ([]byte, error) {
		return applyOpenCodeStyle(content, p, npm, fileLabel)
	}
}

func applyOpenCodeStyle(content []byte, p ApplyParams, npm, fileLabel string) ([]byte, error) {
	root := map[string]any{}
	if len(strings.TrimSpace(string(content))) > 0 {
		if err := json.Unmarshal(content, &root); err != nil {
			return nil, fmt.Errorf("现有 %s 不是合法 JSON（可能含注释，JSONC 无法安全合并），拒绝改写（请先修复或删除该文件）: %w", fileLabel, err)
		}
	}
	provider, _ := root["provider"].(map[string]any)
	if provider == nil {
		provider = map[string]any{}
		root["provider"] = provider
	}
	provider["aether"] = map[string]any{
		"npm": npm,
		"options": map[string]any{
			"baseURL": p.BaseURL + "/v1",
			"apiKey":  p.APIKey,
		},
	}
	if p.Model != "" {
		root["model"] = "aether/" + p.Model
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// managedOpenCode 探测 opencode.json 里的受管理字段（provider.aether 的
// baseURL 与顶层 model），不回显密钥。
func managedOpenCode(data []byte) *ManagedInfo {
	var root struct {
		Provider map[string]struct {
			Options struct {
				BaseURL string `json:"baseURL"`
			} `json:"options"`
		} `json:"provider"`
		Model string `json:"model"`
	}
	if json.Unmarshal(data, &root) != nil {
		return nil
	}
	a, ok := root.Provider["aether"]
	if !ok || a.Options.BaseURL == "" {
		return nil
	}
	return &ManagedInfo{BaseURL: a.Options.BaseURL, Model: root.Model}
}

// applyTOML 改写 Codex 的 config.toml：删除旧的管理块后重建，
// 其余内容（用户自己的 provider、注释、其它配置）原样保留。
//
// 顶层键语义：应用即"把这个客户端切换到网关"，因此顶层 model_provider 与
// model（仅当显式指定时）会被替换 —— 旧值永远可以从备份恢复。
func applyTOML(content []byte, p ApplyParams) ([]byte, error) {
	lines := strings.Split(string(content), "\n")

	out := make([]string, 0, len(lines)+8)
	inManagedBlock := false
	seenSection := false
	for _, line := range lines {
		t := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		// 托管块从 [model_providers.aether] 开始，到下一个 section 头结束。
		if t == "[model_providers.aether]" {
			inManagedBlock = true
			continue
		}
		if inManagedBlock {
			if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
				inManagedBlock = false // 下一个 section，恢复逐行输出
			} else {
				continue // 仍在托管块内，丢弃
			}
		}
		// 顶层键区（第一个 section 之前）：替换 model_provider / model。
		if !seenSection {
			if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
				seenSection = true
			} else if strings.HasPrefix(t, "model_provider =") || strings.HasPrefix(t, "model_provider=") {
				continue
			} else if p.Model != "" && (strings.HasPrefix(t, "model =") || strings.HasPrefix(t, "model=")) {
				continue
			} else if p.CodexCatalog != "" && (strings.HasPrefix(t, "model_catalog_json =") || strings.HasPrefix(t, "model_catalog_json=")) {
				continue
			}
		}
		out = append(out, line)
	}

	// 顶层键插到文件最前面（TOML 顶层键必须出现在任何 section 之前）。
	head := []string{`model_provider = "aether"`}
	if p.Model != "" {
		head = append(head, `model = "`+p.Model+`"`)
	}
	if p.CodexCatalog != "" {
		head = append(head, `model_catalog_json = "`+p.CodexCatalog+`"`)
	}
	result := append(head, out...)

	block := []string{
		"",
		"[model_providers.aether]",
		`name = "Aether Cockpit"`,
		`base_url = "` + p.BaseURL + `/v1"`,
	}
	if p.CodexBearerToken != "" {
		block = append(block,
			`requires_openai_auth = false`,
			`experimental_bearer_token = `+tomlQuote(p.CodexBearerToken),
		)
	} else {
		block = append(block, `env_key = "AETHER_API_KEY"`)
	}
	block = append(block,
		`wire_api = "responses"`,
		"",
	)
	result = append(result, block...)

	joined := strings.Join(result, "\n")
	// 去掉可能连续出现的空行堆叠。
	joined = strings.ReplaceAll(joined, "\n\n\n", "\n\n")
	if !strings.HasSuffix(joined, "\n") {
		joined += "\n"
	}
	return []byte(joined), nil
}

// applyEnvFile 改写 KEY=VALUE 形态的 env 文件（Gemini CLI 的 ~/.gemini/.env）。
// 保留注释与其它键，只替换受管理的键。
func applyEnvFile(content []byte, p ApplyParams, keys *envKeys) ([]byte, error) {
	if keys == nil {
		keys = defaultEnvKeys()
	}
	managed := map[string]string{
		keys.baseURL: p.BaseURL + keys.suffix,
		keys.apiKey:  p.APIKey,
	}
	if keys.model != "" && p.Model != "" {
		managed[keys.model] = p.Model
	}

	lines := strings.Split(string(content), "\n")
	seen := map[string]bool{}
	out := make([]string, 0, len(lines)+4)
	for _, line := range lines {
		t := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		key := ""
		if t != "" && !strings.HasPrefix(t, "#") {
			if i := strings.Index(t, "="); i > 0 {
				key = strings.TrimSpace(t[:i])
			}
		}
		if v, ok := managed[key]; ok {
			if !seen[key] {
				out = append(out, key+"="+v)
				seen[key] = true
			}
			continue
		}
		out = append(out, line)
	}
	for k, v := range managed {
		if !seen[k] {
			out = append(out, k+"="+v)
		}
	}
	joined := strings.Join(out, "\n")
	if !strings.HasSuffix(joined, "\n") {
		joined += "\n"
	}
	return []byte(joined), nil
}

// --- 工具 ---

// trimBOM 去掉 UTF-8 BOM：Windows 记事本 / PowerShell 写出的配置文件常带它，
// 带 BOM 的 JSON 不是合法 JSON，严格解析会直接拒绝。
func trimBOM(b []byte) []byte {
	return bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// validateTOML 解析一遍，确认语法合法且没有重复键。
func validateTOML(b []byte) error {
	var probe map[string]any
	return toml.Unmarshal(b, &probe)
}

// validateEnvFile 校验 KEY=VALUE 形态（允许空行与整行注释）。
func validateEnvFile(b []byte) error {
	for i, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		eq := strings.Index(t, "=")
		if eq <= 0 {
			return fmt.Errorf("第 %d 行不是 KEY=VALUE: %q", i+1, t)
		}
		key := strings.TrimSpace(t[:eq])
		if key == "" || strings.ContainsAny(key, " \t") {
			return fmt.Errorf("第 %d 行的键名非法: %q", i+1, key)
		}
	}
	return nil
}

// atomicWrite 先写同目录临时文件再改名，避免写一半崩溃留下损坏配置。
// 临时名必须唯一；目标若是符号链接要写到真实文件；权限沿用原文件。
func atomicWrite(path string, data []byte) error {
	real := path
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if target, err := filepath.EvalSymlinks(path); err == nil {
			real = target
		}
	}
	dir := filepath.Dir(real)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(real)+".aether-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	mode := os.FileMode(0o600)
	if fi, err := os.Stat(real); err == nil {
		mode = fi.Mode().Perm()
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, real)
}

// CustomClient 是 data/clients.json 里的一条自定义客户端定义。
//
// 只支持 envfile 类型：CLI 类工具基本都用环境变量文件，而各家的 JSON/TOML
// schema 差异很大，靠猜格式加定义只会在用户机器上写坏配置。
type CustomClient struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Path 必须是 ~/ 相对路径或绝对路径。
	Path string `json:"path"`
	Note string `json:"note,omitempty"`
	Keys struct {
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
		Model   string `json:"model"`
		// BaseURLSuffix 追加在 base_url 后（例如 /v1beta）。
		BaseURLSuffix string `json:"base_url_suffix,omitempty"`
	} `json:"keys"`
}

// LoadCustomFile 从 JSON 文件加载自定义客户端定义（文件不存在则忽略）。
func (m *Manager) LoadCustomFile(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var file struct {
		Clients []CustomClient `json:"clients"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("clients.json 解析失败: %w", err)
	}
	seen := make(map[string]bool, len(defs)+len(file.Clients))
	for _, d := range defs {
		seen[d.id] = true
	}
	out := make([]clientDef, 0, len(file.Clients))
	for i, c := range file.Clients {
		id := strings.TrimSpace(c.ID)
		name := strings.TrimSpace(c.Name)
		p := strings.TrimSpace(c.Path)
		if id == "" || name == "" || p == "" {
			return fmt.Errorf("clients[%d]: id/name/path 均不能为空", i)
		}
		if seen[id] {
			return fmt.Errorf("clients[%d]: id %q 与内置或其它自定义客户端重复", i, id)
		}
		if c.Kind != "" && c.Kind != string(KindEnvFile) {
			return fmt.Errorf("clients[%d]: 自定义客户端只支持 envfile 类型（当前 %q）", i, c.Kind)
		}
		if c.Keys.BaseURL == "" || c.Keys.APIKey == "" {
			return fmt.Errorf("clients[%d]: keys.base_url 与 keys.api_key 必填", i)
		}
		d := clientDef{
			id:     id,
			name:   name,
			kind:   KindEnvFile,
			note:   strings.TrimSpace(c.Note),
			custom: true,
			env: &envKeys{
				baseURL: c.Keys.BaseURL,
				apiKey:  c.Keys.APIKey,
				model:   c.Keys.Model,
				suffix:  c.Keys.BaseURLSuffix,
			},
		}
		// 统一成正斜杠再判断，避免 Windows 反斜杠转义问题。
		p = filepath.ToSlash(p)
		if strings.HasPrefix(p, "~/") {
			d.relPath = p[2:]
		} else if filepath.IsAbs(p) {
			d.absPath = p
		} else {
			return fmt.Errorf("clients[%d]: path 必须是 ~/ 开头或绝对路径（当前 %q）", i, p)
		}
		seen[id] = true
		out = append(out, d)
	}
	m.custom = out
	return nil
}
