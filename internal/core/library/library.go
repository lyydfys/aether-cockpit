// Package library 是"库"——prompts / MCP 服务器 / skills / subagents 的统一管理，
// 并支持一键写入各 AI 客户端的配置文件（对标 aether 的 Library）。
//
// 与 internal/core/clients（注入网关端点）相比，这里的对象是"内容条目"：
// 一段提示词、一个 MCP server 定义、一篇 SKILL.md、一个子代理定义。两者的
// 安全底线一致，因此这里按同样的三条原则自行实现写入（不 import clients，
// 避免把"客户端定义"与"内容条目"两套概念耦在一个包里）：
//
//  1. 写之前必须先备份（<file>.aether-bak-<时间戳>，同目录，保留最近 5 份）；
//  2. 只碰受管理的内容：prompt 用托管块、MCP 只写 mcpServers.<name>、
//     skill 与 subagent 整文件独立，用户文件的其它内容一个字节都不动；
//  3. 全部走临时文件 + rename 的原子写，写一半崩溃不会留下损坏配置。
package library

import (
	"aether-cockpit/internal/core/jsoncutil"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PromptEntry 是一条提示词。apply 时以托管块形式写入记忆文件。
type PromptEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Content     string `json:"content"`
	CreatedAt   int64  `json:"created_at"`
}

// MCPEntry 是一个 MCP 服务器定义。Config 是该 server 的完整定义对象
// （例如 {"command":"uvx","args":["mcp-server-fetch"],"env":{...}}），
// 原样保存、按目标客户端解释（JSON 合并或 TOML 渲染）。
type MCPEntry struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Config    json.RawMessage `json:"config"`
	CreatedAt int64           `json:"created_at"`
	// DisabledTools 是用户在资源库里关掉的工具名；中继在 tools/list 里隐藏、
	// 并拒绝直接 tools/call（见 gateway/mcp_tools.go）。
	DisabledTools []string `json:"disabled_tools,omitempty"`
	// OAuth 是用户预注册的客户端（服务商不支持动态注册时使用）。
	OAuth *MCPOAuthClient `json:"oauth,omitempty"`
}

// MCPOAuthClient 是预注册的 OAuth 客户端（公开客户端可只填 ClientID）。
// CallbackPort 非 0 时登录用该固定本机端口（服务商要求精确回调地址时）。
type MCPOAuthClient struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret,omitempty"`
	CallbackPort int    `json:"callbackPort,omitempty"`
}

// SkillEntry 是一个技能。Content 即 SKILL.md 全文（含 frontmatter）。
type SkillEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Content     string `json:"content"`
	CreatedAt   int64  `json:"created_at"`

	// 从仓库安装的技能：用于更新检查（Kind 目前为 github）。
	Kind             string `json:"kind,omitempty"`
	Repo             string `json:"repo,omitempty"`
	SkillPath        string `json:"skill_path,omitempty"`
	InstalledContent string `json:"installed_content,omitempty"`

	// Group 是用户自己分的组名（资源库的 Group/Ungroup）。
	Group string `json:"group,omitempty"`
}

// AgentEntry 是一个子代理（subagent）定义，对标 Claude Code 的
// ~/.claude/agents/<name>.md：YAML frontmatter + 正文（系统提示词）。
// Tools / Model 是 frontmatter 的可选字段，保存原样字符串
// （逗号分隔的工具列表，或留空表示不写这一行）。
type AgentEntry struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tools       string   `json:"tools,omitempty"`
	Model       string   `json:"model,omitempty"`
	Content     string   `json:"content"`
	Skills      []string `json:"skills,omitempty"`
	MCP         []string `json:"mcp,omitempty"`
	CreatedAt   int64    `json:"created_at"`
}

// ApplyResult 是单个目标的一次应用结果。
type ApplyResult struct {
	Target string `json:"target"`
	OK     bool   `json:"ok"`
	Path   string `json:"path,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Snapshot 是四类条目的深拷贝快照，JSON 形态即 GET /api/library 的响应体。
type Snapshot struct {
	Prompts    []PromptEntry `json:"prompts"`
	MCPServers []MCPEntry    `json:"mcp_servers"`
	Skills     []SkillEntry  `json:"skills"`
	Agents     []AgentEntry  `json:"agents"`
}

// 默认落盘位置（相对工作目录，与 data/settings.json 等其它运行时文件一致）。
const DefaultPath = "data/library.json"

// 备份后缀与保留数量。刻意与 clients 包保持同一约定，用户只需要记一套规则。
const (
	backupSuffix = ".aether-bak-"
	maxBackups   = 5
)

// errNoHome 无法定位用户主目录时返回。
var errNoHome = errors.New("无法定位用户主目录（HOME/USERPROFILE 未设置）")

// Store 是库本体。导出字段即 data/library.json 的持久化形态；
// 所有读写都经过 mu，外部请通过方法访问而不是直改字段。
type Store struct {
	mu   sync.Mutex
	path string
	// home 是用户主目录。独立成字段是为了测试可注入（NewStoreWithHome）。
	home string

	Prompts    []PromptEntry `json:"prompts"`
	MCPServers []MCPEntry    `json:"mcp_servers"`
	Skills     []SkillEntry  `json:"skills"`
	Agents     []AgentEntry  `json:"agents"`
	Hooks      []HookEntry   `json:"hooks,omitempty"`

	// SkillHow / AgentHow 是技能下发方式（copy / link）；空 = copy。
	SkillHow string            `json:"skill_how,omitempty"`
	AgentHow map[string]string `json:"agent_how,omitempty"`

	// hookMu 串行化 hooks 文件的读-改-写。
	hookMu sync.Mutex

	// mcpApplyMu / skillApplyMu 串行化各 Agent 配置文件的读-改-写。
	mcpApplyMu   sync.Mutex
	skillApplyMu sync.Mutex

	// mcpApplyHook 在写入 Agent 配置前改写一份副本（远程 MCP 已登录时
	// 指向上机中继），由网关设置；不改变库里的原始配置。
	mcpApplyHook func(name string, cfg map[string]any)

	// backupRoot 是集中展示的备份目录（各文件旁边仍保留原备份）。
	backupRoot string
}

// NewStore 创建库并从磁盘加载。path 为空用 data/library.json。
func NewStore(path string) *Store {
	if path == "" {
		path = DefaultPath
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// 不致命：列库/改库照常工作，只有 apply 会失败并报错。
		log.Printf("[WARN] 无法定位用户主目录，库的 apply 功能将不可用: %v", err)
		home = ""
	}
	return newStore(path, home)
}

// NewStoreWithHome 用指定主目录创建（测试用）。
func NewStoreWithHome(path, home string) *Store {
	if path == "" {
		path = DefaultPath
	}
	return newStore(path, home)
}

func newStore(path, home string) *Store {
	backupRoot := ""
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if abs, err := filepath.Abs(filepath.Join(dir, "backups")); err == nil {
			backupRoot = abs
		} else {
			backupRoot = filepath.Join(dir, "backups")
		}
	}
	s := &Store{
		path:       path,
		home:       home,
		backupRoot: backupRoot,
		Prompts:    []PromptEntry{},
		MCPServers: []MCPEntry{},
		Skills:     []SkillEntry{},
		Agents:     []AgentEntry{},
		Hooks:      []HookEntry{},
	}
	s.load()
	return s
}

// load 启动时加载。文件不存在按空库开始；损坏也按空库开始——
// 库是自用的内容数据，损坏不应该阻止网关启动，原文件保留待用户手工处理。
func (s *Store) load() {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[WARN] 库文件读取失败（按空库开始）: %v", err)
		}
		return
	}
	var disk struct {
		Prompts    []PromptEntry     `json:"prompts"`
		MCPServers []MCPEntry        `json:"mcp_servers"`
		Skills     []SkillEntry      `json:"skills"`
		Agents     []AgentEntry      `json:"agents"`
		Hooks      []HookEntry       `json:"hooks"`
		SkillHow   string            `json:"skill_how"`
		AgentHow   map[string]string `json:"agent_how"`
	}
	if err := json.Unmarshal(b, &disk); err != nil {
		log.Printf("[WARN] 库文件 %s 损坏（按空库开始，原文件未删除）: %v", s.path, err)
		return
	}
	if disk.Prompts != nil {
		s.Prompts = disk.Prompts
	}
	if disk.MCPServers != nil {
		s.MCPServers = disk.MCPServers
	}
	if disk.Skills != nil {
		s.Skills = disk.Skills
	}
	if disk.Agents != nil {
		s.Agents = disk.Agents
	}
	if disk.Hooks != nil {
		s.Hooks = disk.Hooks
	}
	if disk.SkillHow != "" {
		s.SkillHow = disk.SkillHow
	}
	if disk.AgentHow != nil {
		s.AgentHow = disk.AgentHow
	}
}

// --- 查询与增删 ---

// List 返回四类条目的深拷贝。深拷贝是刻意的：HTTP 层序列化、前端拿着改，
// 都不应该与 store 内部状态共享底层数组。
func (s *Store) List() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		Prompts:    clonePrompts(s.Prompts),
		MCPServers: cloneMCP(s.MCPServers),
		Skills:     cloneSkills(s.Skills),
		Agents:     cloneAgents(s.Agents),
	}
}

// SavePrompt 保存 prompt（upsert）。e.ID 为空时新建并生成 ID；
// ID 已存在时整体覆盖（视为更新），CreatedAt 沿用原值；
// ID 非空但不存在时按新条目插入（导入场景）。落盘失败返回错误。
func (s *Store) SavePrompt(e PromptEntry) (PromptEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.Name = strings.TrimSpace(e.Name)
	if e.Name == "" {
		return e, errors.New("name 不能为空")
	}
	if i := s.promptIndex(e.ID); i >= 0 {
		e.CreatedAt = s.Prompts[i].CreatedAt
		s.Prompts[i] = e
	} else {
		if e.ID == "" {
			e.ID = newID(e.Name, s.promptIndex)
		}
		if e.CreatedAt == 0 {
			e.CreatedAt = time.Now().Unix()
		}
		s.Prompts = append(s.Prompts, e)
	}
	if err := s.flushLocked(); err != nil {
		return e, err
	}
	return e, nil
}

// SaveMCP 保存 MCP 服务器定义（upsert）。Config 必须是 JSON 对象——
// 这是硬约束：JSON 合并要把它整个塞进 mcpServers.<name>，TOML 渲染要枚举它的键。
func (s *Store) SaveMCP(e MCPEntry) (MCPEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.Name = strings.TrimSpace(e.Name)
	if e.Name == "" {
		return e, errors.New("name 不能为空")
	}
	var probe map[string]any
	if err := json.Unmarshal(e.Config, &probe); err != nil {
		return e, fmt.Errorf("config 必须是 JSON 对象: %w", err)
	}
	if i := s.mcpIndex(e.ID); i >= 0 {
		e.CreatedAt = s.MCPServers[i].CreatedAt
		s.MCPServers[i] = e
	} else if j := s.mcpNameIndex(e.Name); j >= 0 {
		// 没有 ID 但同名：按名字更新，避免同一服务器在库里累积多份
		// （工具开关等条目上的状态也随之保留）。
		e.ID = s.MCPServers[j].ID
		e.CreatedAt = s.MCPServers[j].CreatedAt
		if e.DisabledTools == nil {
			e.DisabledTools = s.MCPServers[j].DisabledTools
		}
		s.MCPServers[j] = e
	} else {
		if e.ID == "" {
			e.ID = newID(e.Name, s.mcpIndex)
		}
		if e.CreatedAt == 0 {
			e.CreatedAt = time.Now().Unix()
		}
		s.MCPServers = append(s.MCPServers, e)
	}
	if err := s.flushLocked(); err != nil {
		return e, err
	}
	return e, nil
}

// UpdateMCPDisabledTools 记录用户关掉的工具名（中继据此过滤）。
func (s *Store) UpdateMCPDisabledTools(id string, tools []string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.mcpIndex(id)
	if i < 0 {
		return false
	}
	s.MCPServers[i].DisabledTools = tools
	_ = s.flushLocked()
	return true
}

// SaveSkill 保存技能（upsert）。Content 即 SKILL.md 全文。
func (s *Store) SaveSkill(e SkillEntry) (SkillEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.Name = strings.TrimSpace(e.Name)
	if e.Name == "" {
		return e, errors.New("name 不能为空")
	}
	if e.Content == "" {
		return e, errors.New("content 不能为空")
	}
	if i := s.skillIndex(e.ID); i >= 0 {
		e.CreatedAt = s.Skills[i].CreatedAt
		s.Skills[i] = e
	} else if j := s.skillIndexByName(e.Name); j >= 0 {
		// 同名保存（编辑器没有 ID）：更新而不是累积第二份，
		// 安装来源与分组等条目状态原样保留。
		old := s.Skills[j]
		e.ID, e.CreatedAt = old.ID, old.CreatedAt
		if e.Kind == "" {
			e.Kind, e.Repo, e.SkillPath, e.InstalledContent = old.Kind, old.Repo, old.SkillPath, old.InstalledContent
		}
		if e.Group == "" {
			e.Group = old.Group
		}
		s.Skills[j] = e
	} else {
		if e.ID == "" {
			e.ID = newID(e.Name, s.skillIndex)
		}
		if e.CreatedAt == 0 {
			e.CreatedAt = time.Now().Unix()
		}
		s.Skills = append(s.Skills, e)
	}
	if err := s.flushLocked(); err != nil {
		return e, err
	}
	s.refreshCanonicalLocked(e)
	return e, nil
}

// SaveAgent 保存子代理定义（upsert）。Content 为正文（系统提示词），
// Tools / Model 原样保存，留空表示 apply 时不写该 frontmatter 行。
func (s *Store) SaveAgent(e AgentEntry) (AgentEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.Name = strings.TrimSpace(e.Name)
	if e.Name == "" {
		return e, errors.New("name 不能为空")
	}
	if e.Content == "" {
		return e, errors.New("content 不能为空")
	}
	if i := s.agentIndex(e.ID); i >= 0 {
		e.CreatedAt = s.Agents[i].CreatedAt
		s.Agents[i] = e
	} else {
		if e.ID == "" {
			e.ID = newID(e.Name, s.agentIndex)
		}
		if e.CreatedAt == 0 {
			e.CreatedAt = time.Now().Unix()
		}
		s.Agents = append(s.Agents, e)
	}
	if err := s.flushLocked(); err != nil {
		return e, err
	}
	return e, nil
}

// DeletePrompt 按 ID 删除，返回是否删除了条目。
// 落盘失败只记日志：内存状态已正确，下次任一次成功落盘即自愈；
// 而删除本身不可逆，不值得为此让调用方再处理一种错误。
func (s *Store) DeletePrompt(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.promptIndex(id)
	if i < 0 {
		return false
	}
	s.Prompts = append(s.Prompts[:i], s.Prompts[i+1:]...)
	s.flushLockedAndLog()
	return true
}

// DeleteMCP 按 ID 删除，返回是否删除了条目。
func (s *Store) DeleteMCP(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.mcpIndex(id)
	if i < 0 {
		return false
	}
	s.MCPServers = append(s.MCPServers[:i], s.MCPServers[i+1:]...)
	s.flushLockedAndLog()
	return true
}

// DeleteSkill 按 ID 删除，返回是否删除了条目。
func (s *Store) DeleteSkill(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.skillIndex(id)
	if i < 0 {
		return false
	}
	s.Skills = append(s.Skills[:i], s.Skills[i+1:]...)
	s.flushLockedAndLog()
	return true
}

// DeleteAgent 按 ID 删除，返回是否删除了条目。
func (s *Store) DeleteAgent(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.agentIndex(id)
	if i < 0 {
		return false
	}
	s.Agents = append(s.Agents[:i], s.Agents[i+1:]...)
	s.flushLockedAndLog()
	return true
}

// Flush 显式落盘。
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked()
}

func (s *Store) promptIndex(id string) int {
	if id == "" {
		return -1
	}
	for i := range s.Prompts {
		if s.Prompts[i].ID == id {
			return i
		}
	}
	return -1
}

func (s *Store) mcpIndex(id string) int {
	if id == "" {
		return -1
	}
	for i := range s.MCPServers {
		if s.MCPServers[i].ID == id {
			return i
		}
	}
	return -1
}

func (s *Store) mcpNameIndex(name string) int {
	for i := range s.MCPServers {
		if s.MCPServers[i].Name == name {
			return i
		}
	}
	return -1
}

func (s *Store) skillIndexByName(name string) int {
	for i := range s.Skills {
		if s.Skills[i].Name == name {
			return i
		}
	}
	return -1
}

// UpdateSkillGroup 把给定的技能放进组（group 为空表示移出组）。
// names 为空时不改任何条目；返回真正变化的技能名。
func (s *Store) UpdateSkillGroup(names []string, group string) []string {
	if len(names) == 0 {
		return nil
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := []string{}
	for i := range s.Skills {
		if !want[s.Skills[i].Name] || s.Skills[i].Group == group {
			continue
		}
		s.Skills[i].Group = group
		changed = append(changed, s.Skills[i].Name)
	}
	if len(changed) > 0 {
		_ = s.flushLocked()
	}
	return changed
}

// SkillsInGroup 返回属于某组的技能名（按库内顺序）。
func (s *Store) SkillsInGroup(group string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for i := range s.Skills {
		if s.Skills[i].Group == group {
			out = append(out, s.Skills[i].Name)
		}
	}
	return out
}

func (s *Store) skillIndex(id string) int {
	if id == "" {
		return -1
	}
	for i := range s.Skills {
		if s.Skills[i].ID == id {
			return i
		}
	}
	return -1
}

func (s *Store) agentIndex(id string) int {
	if id == "" {
		return -1
	}
	for i := range s.Agents {
		if s.Agents[i].ID == id {
			return i
		}
	}
	return -1
}

// flushLocked 原子落盘（.tmp + rename，0600）。调用方持有 mu。
func (s *Store) flushLocked() error {
	if dir := filepath.Dir(s.path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) flushLockedAndLog() {
	if err := s.flushLocked(); err != nil {
		log.Printf("[WARN] 库文件写入失败: %v", err)
	}
}

// --- 深拷贝 ---

func clonePrompts(in []PromptEntry) []PromptEntry {
	out := make([]PromptEntry, len(in))
	copy(out, in)
	return out
}

func cloneSkills(in []SkillEntry) []SkillEntry {
	out := make([]SkillEntry, len(in))
	copy(out, in)
	return out
}

func cloneMCP(in []MCPEntry) []MCPEntry {
	out := make([]MCPEntry, len(in))
	for i, e := range in {
		out[i] = e
		// RawMessage 是切片，浅拷贝共享底层数组；条目从不原地改 Config，
		// 但拷贝是承诺过的，这里做彻底。
		out[i].Config = append(json.RawMessage(nil), e.Config...)
	}
	return out
}

func cloneAgents(in []AgentEntry) []AgentEntry {
	out := make([]AgentEntry, len(in))
	copy(out, in)
	return out
}

// --- ID 生成 ---

// newID 生成 "<slug>-<短哈希>" 形式的 ID：可读、可排序，且几乎不可能撞车。
// 中文等非 ASCII 名称的 slug 为空，此时用 "item" 兜底——短哈希保证唯一性。
func newID(name string, exists func(string) int) string {
	base := slug(name)
	if base == "" {
		base = "item"
	}
	if len(base) > 40 {
		base = strings.Trim(base[:40], "-")
	}
	// 名字 + 当前时间做哈希输入：同一名字多次新建也能得到不同 ID。
	input := name + "|" + time.Now().String()
	id := base + "-" + shortHash(input)
	for n := 2; exists(id) >= 0 && n < 10; n++ {
		id = base + "-" + shortHash(input+strconv.Itoa(n))
	}
	return id
}

// slug 把名称转成小写的 [a-z0-9-] 标识。
func slug(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// shortHash 与 gateway.admin.go 的同名思路一致：sha256 取前 8 位十六进制。
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

// safeFileName 把名称转成安全的文件/目录名。与 slug 不同，这里保留非 ASCII
// 字符（各平台文件系统都支持中文目录名），只替换路径非法字符与空白。
func safeFileName(name, fallback string) string {
	r := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "-", "?", "-",
		`"`, "-", "<", "-", ">", "-", "|", "-",
		" ", "-", "\t", "-", "\n", "-", "\r", "-",
	)
	out := strings.Trim(r.Replace(strings.TrimSpace(name)), "-. ")
	if out == "" {
		out = fallback
	}
	return out
}

// skillDirName 把技能名转成安全的目录名。
func skillDirName(name string) string { return safeFileName(name, "skill") }

// agentFileName 把子代理名转成安全的文件名（不含 .md 后缀）。
func agentFileName(name string) string { return safeFileName(name, "agent") }

// --- Prompt apply：记忆文件托管块 ---

// ApplyPrompt 把 prompt 以托管块形式写入目标客户端的记忆文件，返回文件路径。
// 目标：claude → ~/.claude/CLAUDE.md，codex → ~/.codex/AGENTS.md，gemini → ~/.gemini/GEMINI.md。
func (s *Store) ApplyPrompt(id, target string) (string, error) {
	s.mu.Lock()
	i := s.promptIndex(id)
	var entry PromptEntry
	if i >= 0 {
		entry = s.Prompts[i]
	}
	s.mu.Unlock()
	if i < 0 {
		return "", fmt.Errorf("prompt 条目不存在: %s", id)
	}
	if s.home == "" {
		return "", errNoHome
	}

	var path string
	switch target {
	case "claude":
		path = filepath.Join(s.home, ".claude", "CLAUDE.md")
	case "codex":
		path = filepath.Join(s.home, ".codex", "AGENTS.md")
	case "gemini":
		path = filepath.Join(s.home, ".gemini", "GEMINI.md")
	default:
		return "", fmt.Errorf("不支持的 prompt 目标: %s（支持 claude / codex / gemini）", target)
	}

	if err := s.backupFile(path); err != nil {
		return "", fmt.Errorf("备份失败: %w", err)
	}
	exists := fileExists(path)
	var existing []byte
	if exists {
		var err error
		existing, err = os.ReadFile(path)
		if err != nil {
			return "", err
		}
	}
	out, err := buildPromptFile(existing, exists, entry.Name, entry.Content)
	if err != nil {
		// 孤立 begin 标记等用户文件自身的问题：不改写，让用户手工处理。
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := atomicWrite(path, []byte(out)); err != nil {
		return "", err
	}
	return path, nil
}

func promptBegin(name string) string { return "<!-- aether-library:prompt " + name + " begin -->" }
func promptEnd(name string) string   { return "<!-- aether-library:prompt " + name + " end -->" }

// buildPromptFile 生成写入记忆文件的完整文本。
//
// 策略：只匹配完整的 begin..end 对（逐行状态机），把同名旧块整段移除后
// 在末尾追加新块；同名块之外的内容一个字节都不动。发现孤立的 begin
// （有 begin 没 end）说明用户文件已被手工改坏，此时拒绝改写并报错——
// 若继续追加会产生两个 begin 对一个 end，破坏只会更严重。
func buildPromptFile(existing []byte, exists bool, name, content string) (string, error) {
	var cleaned string
	if exists {
		var err error
		cleaned, err = stripPromptBlocks(string(existing), name)
		if err != nil {
			return "", err
		}
	} else {
		// 新文件先给一行标题，避免用户打开只看到裸标记摸不着头脑。
		cleaned = "# " + name + "\n\n"
	}

	var b strings.Builder
	b.WriteString(cleaned)
	// 文件没以换行结尾时补一个，保证块标记从行首开始。
	// 这是"追加"这一动作的必要代价，不触碰任何已有行。
	if b.Len() > 0 && !strings.HasSuffix(cleaned, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(promptBegin(name) + "\n")
	b.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(promptEnd(name) + "\n")
	return b.String(), nil
}

// stripPromptBlocks 移除文本中所有同名完整托管块。
// 逐行扫描：标记行整行删除，其余行（含行尾可能的 \r）原样保留，
// 因此 CRLF 文件除被删除的块外不会被动过。
func stripPromptBlocks(text, name string) (string, error) {
	begin, end := promptBegin(name), promptEnd(name)
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	inBlock := false
	for _, line := range lines {
		t := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if !inBlock && t == begin {
			inBlock = true
			continue
		}
		if inBlock {
			if t == end {
				inBlock = false
			}
			continue // 块内内容（含 end 标记）一律丢弃
		}
		out = append(out, line)
	}
	if inBlock {
		return "", fmt.Errorf(
			"文件里有不成对的托管块标记（%q 缺少对应的 end），拒绝改写；请手工删除该标记后重试",
			begin)
	}
	return strings.Join(out, "\n"), nil
}

// --- MCP apply：JSON 合并 / TOML 托管块 ---

// SetMCPApplyHook 注册写入前的配置改写钩子（远程 MCP 中继用）。
func (s *Store) SetMCPApplyHook(fn func(name string, cfg map[string]any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mcpApplyHook = fn
}

// ApplyMCP 把 MCP 服务器定义写入目标客户端的配置文件，返回文件路径。
// 目标：claude → ~/.claude.json，gemini → ~/.gemini/settings.json（JSON 合并），
// codex → ~/.codex/config.toml（托管块），opencode → ~/.config/opencode/opencode.json
// （mcp.<name>，远程 type/url、本地 type/command）。
func (s *Store) ApplyMCP(id, target string) (string, error) {
	return s.ApplyMCPTo(s.home, id, target)
}

// ApplyMCPTo 与 ApplyMCP 相同，但写到指定家目录：用于把同一条服务器下发到
// 另一个目标（例如 WSL 发行版的家目录）。库数据仍从本 Store 读。
func (s *Store) ApplyMCPTo(home, id, target string) (string, error) {
	s.mu.Lock()
	i := s.mcpIndex(id)
	var entry MCPEntry
	if i >= 0 {
		entry = s.MCPServers[i]
	}
	hook := s.mcpApplyHook
	s.mu.Unlock()
	if i < 0 {
		return "", fmt.Errorf("MCP 条目不存在: %s", id)
	}
	if home == "" {
		return "", errNoHome
	}
	s.mcpApplyMu.Lock()
	defer s.mcpApplyMu.Unlock()

	// 用 UseNumber 解码：数字保留原始字面量（40 不会被 float64 化成 40.0），
	// TOML 渲染与 JSON 重编码都因此保持用户输入的原样。
	cfg := map[string]any{}
	dec := json.NewDecoder(bytes.NewReader(entry.Config))
	dec.UseNumber()
	if err := dec.Decode(&cfg); err != nil {
		return "", fmt.Errorf("config 必须是 JSON 对象: %w", err)
	}
	if hook != nil {
		hook(entry.Name, cfg)
	}

	switch target {
	case "claude":
		return s.writeMCPJSON(filepath.Join(home, ".claude.json"), entry.Name, cfg)
	case "gemini":
		return s.writeMCPJSON(filepath.Join(home, ".gemini", "settings.json"), entry.Name, cfg)
	case "codex":
		return s.writeMCPTOML(filepath.Join(home, ".codex", "config.toml"), entry.Name, cfg)
	case "opencode":
		path := filepath.Join(home, ".config", "opencode", "opencode.json")
		jsoncPath := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
		if !fileExists(path) && fileExists(jsoncPath) {
			path = jsoncPath
		}
		return s.writeMCPOpenCode(path, entry.Name, cfg)
	default:
		return "", fmt.Errorf("不支持的 MCP 目标: %s（支持 claude / codex / gemini / opencode）", target)
	}
}

// openCodeMCPEntry 把 Claude 形状的 MCP 配置折算成 opencode 的 mcp.<name>
// 形状：远程 type/url/headers，本地 type/command（首元素为命令）+ environment。
func openCodeMCPEntry(cfg map[string]any) (map[string]any, error) {
	out := map[string]any{"enabled": true}
	if u, _ := cfg["url"].(string); strings.TrimSpace(u) != "" {
		out["type"] = "remote"
		out["url"] = u
		if h, ok := cfg["headers"].(map[string]any); ok && len(h) > 0 {
			out["headers"] = h
		}
		return out, nil
	}
	cmd, _ := cfg["command"].(string)
	if strings.TrimSpace(cmd) == "" {
		return nil, fmt.Errorf("MCP 配置缺少 command 或 url")
	}
	command := []any{cmd}
	if args, ok := cfg["args"].([]any); ok {
		command = append(command, args...)
	}
	out["type"] = "local"
	out["command"] = command
	if env, ok := cfg["env"].(map[string]any); ok && len(env) > 0 {
		out["environment"] = env
	}
	return out, nil
}

// writeMCPOpenCode 合并写 ~/.config/opencode/opencode.json 的 mcp.<name>，
// 顶层与已有服务器原样保留。opencode 允许 JSONC；带注释的文件无法安全
// 合并，解析失败时拒绝改写而不是重建空文档。
func (s *Store) writeMCPOpenCode(path, name string, cfg map[string]any) (string, error) {
	entry, err := openCodeMCPEntry(cfg)
	if err != nil {
		return "", err
	}
	if err := s.backupFile(path); err != nil {
		return "", fmt.Errorf("备份失败: %w", err)
	}
	root := map[string]any{}
	if fileExists(path) {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if len(bytes.TrimSpace(content)) > 0 {
			normalized, err := jsoncutil.Parse(content)
			if err != nil {
				return "", fmt.Errorf("现有 %s 不是合法 JSONC，拒绝改写: %w", filepath.Base(path), err)
			}
			if err := json.Unmarshal(normalized, &root); err != nil {
				return "", fmt.Errorf("现有 %s 不是合法 JSON，拒绝改写: %w", filepath.Base(path), err)
			}
		}
	}
	if _, exists := root["mcp"]; exists {
		if _, ok := root["mcp"].(map[string]any); !ok {
			return "", fmt.Errorf("现有 %s 里的 mcp 不是 JSON 对象，拒绝改写", filepath.Base(path))
		}
	} else {
		root["mcp"] = map[string]any{}
	}
	root["mcp"].(map[string]any)[name] = entry
	raw, _ := os.ReadFile(path)
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	if strings.HasSuffix(strings.ToLower(path), ".jsonc") || !json.Valid(raw) {
		out, err := jsoncutil.SpliceMember(raw, "mcp", root["mcp"])
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := atomicWrite(path, out); err != nil {
			return "", err
		}
		return path, nil
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := atomicWrite(path, out); err != nil {
		return "", err
	}
	return path, nil
}

// writeMCPJSON 合并写 mcpServers.<name>，其余键原样保留。
// ~/.claude.json 可能很大且含客户端其它状态，所以解析失败时拒绝改写
// 而不是"重建一个空的"——宁可让用户手工修复，也不能弄丢会话状态。
func (s *Store) writeMCPJSON(path, name string, cfg map[string]any) (string, error) {
	if err := s.backupFile(path); err != nil {
		return "", fmt.Errorf("备份失败: %w", err)
	}
	root := map[string]any{}
	if fileExists(path) {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if len(bytes.TrimSpace(content)) > 0 {
			if err := json.Unmarshal(content, &root); err != nil {
				return "", fmt.Errorf("现有 %s 不是合法 JSON，拒绝改写（请先修复或删除该文件）: %w",
					filepath.Base(path), err)
			}
		}
	}
	if _, exists := root["mcpServers"]; exists {
		if _, ok := root["mcpServers"].(map[string]any); !ok {
			return "", fmt.Errorf("现有 %s 里的 mcpServers 不是 JSON 对象，拒绝改写", filepath.Base(path))
		}
	} else {
		root["mcpServers"] = map[string]any{}
	}
	root["mcpServers"].(map[string]any)[name] = cfg

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := atomicWrite(path, out); err != nil {
		return "", err
	}
	return path, nil
}

// writeMCPTOML 把 MCP server 写成 config.toml 里的托管块。
// 删除旧的同名块（含其 env 等子表）后，把新块追加到文件末尾；
// 用户的注释、其它 section 原样保留。
func (s *Store) writeMCPTOML(path, name string, cfg map[string]any) (string, error) {
	block, err := renderMCPBlock(name, cfg)
	if err != nil {
		return "", err
	}
	if err := s.backupFile(path); err != nil {
		return "", fmt.Errorf("备份失败: %w", err)
	}
	var content []byte
	if fileExists(path) {
		content, err = os.ReadFile(path)
		if err != nil {
			return "", err
		}
	}
	cleaned := stripMCPSections(string(content), name)

	var b strings.Builder
	b.WriteString(cleaned)
	if b.Len() > 0 && !strings.HasSuffix(cleaned, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(strings.Join(block, "\n"))
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := atomicWrite(path, []byte(b.String())); err != nil {
		return "", err
	}
	return path, nil
}

// renderMCPBlock 把一个 MCP server 渲染成 TOML 行序列（以空行开头）：
//
//	[mcp_servers.<name>]
//	key = "value"
//	args = ["a", "b"]
//
//	[mcp_servers.<name>.env]
//	KEY = "value"
//
// 只支持一层对象嵌套（env 这类扁平键值表），再深报错——深嵌套说明
// 该 server 的配置复杂到应该让用户手工维护，程序猜错结构的风险更大。
// 子表必须排在父表所有键值之后（TOML 语法要求），所以分两轮输出。
func renderMCPBlock(name string, cfg map[string]any) ([]string, error) {
	lines := []string{"", "[mcp_servers." + tomlKey(name) + "]"}
	var subs [][]string
	for _, k := range sortedKeys(cfg) {
		key := tomlKey(k)
		switch v := cfg[k].(type) {
		case map[string]any:
			sub := []string{"", "[mcp_servers." + tomlKey(name) + "." + key + "]"}
			for _, sk := range sortedKeys(v) {
				sv, err := tomlScalar(v[sk])
				if err != nil {
					return nil, fmt.Errorf("字段 %q.%q: %w", k, sk, err)
				}
				sub = append(sub, tomlKey(sk)+" = "+sv)
			}
			subs = append(subs, sub)
		case []any:
			items := make([]string, 0, len(v))
			for _, el := range v {
				sv, err := tomlScalar(el)
				if err != nil {
					return nil, fmt.Errorf("字段 %q 的数组元素: %w", k, err)
				}
				items = append(items, sv)
			}
			lines = append(lines, key+" = ["+strings.Join(items, ", ")+"]")
		default:
			sv, err := tomlScalar(cfg[k])
			if err != nil {
				return nil, fmt.Errorf("字段 %q: %w", k, err)
			}
			lines = append(lines, key+" = "+sv)
		}
	}
	for _, sub := range subs {
		lines = append(lines, sub...)
	}
	return lines, nil
}

// tomlScalar 把 JSON 标量渲染成 TOML 值。TOML 没有 null，遇到就报错。
func tomlScalar(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return tomlQuote(x), nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	case json.Number:
		return x.String(), nil
	case nil:
		return "", errors.New("TOML 不支持 null 值")
	default:
		return "", fmt.Errorf("不支持嵌套的 %T（仅支持字符串/数字/布尔标量）", v)
	}
}

// bareKeyRe TOML 裸键允许的字符集。
var bareKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// tomlKey 渲染 TOML 键：合法裸键直接输出，否则用引号形式。
func tomlKey(k string) string {
	if bareKeyRe.MatchString(k) {
		return k
	}
	return `"` + tomlEscape(k) + `"`
}

// tomlEscape 转义 TOML 基本字符串里的特殊字符（\、"、换行等）。
func tomlEscape(s string) string {
	return strings.NewReplacer(
		`\`, `\\`, `"`, `\"`,
		"\n", `\n`, "\r", `\r`, "\t", `\t`,
	).Replace(s)
}

func tomlQuote(s string) string { return `"` + tomlEscape(s) + `"` }

// stripMCPSections 从 TOML 文本中删除 <name> 这个 server 的旧托管块
// （[mcp_servers.<name>] 及其 [mcp_servers.<name>.<sub>] 子表），
// 其它 section 原样保留。section 头去掉空格后比较，容忍手写的
// "[ mcp_servers.foo ]" 形式；同名前缀的其它 server（foo 与 foo-bar）不受影响。
func stripMCPSections(text, name string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	inManaged := false
	for _, line := range lines {
		t := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			section := strings.ReplaceAll(t[1:len(t)-1], " ", "")
			inManaged = isManagedSection(section, name)
			if inManaged {
				continue // 删掉托管 section 头
			}
		} else if inManaged {
			continue // 仍在托管块内，键行一并丢弃
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// isManagedSection 判断 section 头是否属于 <name> 的托管块。
// 同时匹配裸键与引号键两种形式（我们按名字自动选择其一，
// 但用户可能手工写过另一种）。
func isManagedSection(section, name string) bool {
	bare := "mcp_servers." + name
	quoted := `mcp_servers."` + tomlEscape(name) + `"`
	return section == bare || section == quoted ||
		strings.HasPrefix(section, bare+".") || strings.HasPrefix(section, quoted+".")
}

// --- Skill apply：技能目录 ---

// 技能下发方式：复制（默认）或链接（库内 canonical 目录 + 目录链接）。
const (
	SkillHowCopy = "copy"
	SkillHowLink = "link"
)

func (s *Store) skillHowLocked() string {
	if s.SkillHow == SkillHowLink {
		return SkillHowLink
	}
	return SkillHowCopy
}

// SkillHowSetting 返回库的默认技能下发方式。
func (s *Store) SkillHowSetting() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skillHowLocked()
}

// AgentSkillHow 返回某个 Agent 的覆盖设置与是否存在覆盖。
func (s *Store) AgentSkillHow(agent string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.AgentHow == nil {
		return "", false
	}
	h := s.AgentHow[agent]
	return h, h != ""
}

// SetSkillHow 设置库默认（agent 为空）或单个 Agent 的技能下发方式。
func (s *Store) SetSkillHow(agent, how string) error {
	how = strings.TrimSpace(how)
	if how != SkillHowCopy && how != SkillHowLink {
		return fmt.Errorf("不支持的技能方式: %s", how)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if agent == "" {
		s.SkillHow = how
	} else {
		if s.AgentHow == nil {
			s.AgentHow = map[string]string{}
		}
		s.AgentHow[agent] = how
	}
	return s.flushLocked()
}

// EffectiveSkillHow 返回某个 apply 目标（claude / agents）实际使用的方式。
// .agents 目录由三个 Agent 共用：只有三者覆盖一致时才用覆盖值。
func (s *Store) EffectiveSkillHow(target string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if target == "claude" {
		if h := s.AgentHow["claude_code"]; h != "" {
			return h
		}
		return s.skillHowLocked()
	}
	seen := ""
	for _, a := range []string{"codex_cli", "gemini_cli", "opencode"} {
		h := s.AgentHow[a]
		if h == "" {
			return s.skillHowLocked()
		}
		if seen == "" {
			seen = h
		} else if seen != h {
			return s.skillHowLocked()
		}
	}
	if seen != "" {
		return seen
	}
	return s.skillHowLocked()
}

// MaterializeSkill 把技能写进库自己的 canonical 目录，返回目录路径。
// 链接模式下各 Agent 目录都指向这里，保存技能即同步更新。
func (s *Store) MaterializeSkill(name, content string) (string, error) {
	dir := filepath.Join(filepath.Dir(s.path), "skills", skillDirName(name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := atomicWrite(filepath.Join(dir, "SKILL.md"), []byte(content)); err != nil {
		return "", err
	}
	return dir, nil
}

func (s *Store) refreshCanonicalLocked(e SkillEntry) {
	dir := filepath.Join(filepath.Dir(s.path), "skills", skillDirName(e.Name))
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		_ = atomicWrite(filepath.Join(dir, "SKILL.md"), []byte(e.Content))
	}
}

// prepareSkillDirForLink 清掉同名真实目录（只允许仅含 SKILL.md 的、我们自己
// 写出的副本），已有链接原样删除由调用方重建。
func prepareSkillDirForLink(dir string) error {
	if IsDirLink(dir) {
		return os.Remove(dir)
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return os.Remove(dir)
	}
	if len(entries) == 1 && entries[0].Name() == "SKILL.md" {
		return os.RemoveAll(dir)
	}
	return fmt.Errorf("目标已有同名技能目录且含其他文件，未改为链接: %s", dir)
}

// ApplySkill 把技能复制到目标客户端的技能目录（兼容旧调用）。
func (s *Store) ApplySkill(id, target string) (string, error) {
	return s.ApplySkillAs(id, target, false)
}

// ApplySkillAs 按 link 选择复制或链接下发。链接模式先写 canonical 目录，
// 再在目标处建目录链接（Windows 无权限时退化为 junction）。
func (s *Store) ApplySkillAs(id, target string, link bool) (string, error) {
	return s.ApplySkillAsTo(s.home, id, target, link)
}

// ApplySkillAsTo 与 ApplySkillAs 相同，但写入指定家目录（WSL 发行版等）。
func (s *Store) ApplySkillAsTo(home, id, target string, link bool) (string, error) {
	s.mu.Lock()
	i := s.skillIndex(id)
	var entry SkillEntry
	if i >= 0 {
		entry = s.Skills[i]
	}
	s.mu.Unlock()
	if i < 0 {
		return "", fmt.Errorf("skill 条目不存在: %s", id)
	}
	if home == "" {
		return "", errNoHome
	}
	s.skillApplyMu.Lock()
	defer s.skillApplyMu.Unlock()

	var base string
	switch target {
	case "claude":
		base = filepath.Join(home, ".claude", "skills")
	case "agents":
		base = filepath.Join(home, ".agents", "skills")
	default:
		return "", fmt.Errorf("不支持的 skill 目标: %s（支持 claude / agents）", target)
	}
	dir := filepath.Join(base, skillDirName(entry.Name))
	if link {
		canonical, err := s.MaterializeSkill(entry.Name, entry.Content)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(base, 0o755); err != nil {
			return "", err
		}
		if err := prepareSkillDirForLink(dir); err != nil {
			return "", err
		}
		if err := DirLink(canonical, dir); err != nil {
			return "", fmt.Errorf("创建技能链接失败: %w", err)
		}
		return filepath.Join(dir, "SKILL.md"), nil
	}
	if IsDirLink(dir) {
		if err := os.Remove(dir); err != nil {
			return "", fmt.Errorf("移除旧技能链接失败: %w", err)
		}
	}
	skillFile := filepath.Join(dir, "SKILL.md")
	// 覆盖已有 SKILL.md 前先备份该文件本身。
	if err := s.backupFile(skillFile); err != nil {
		return "", fmt.Errorf("备份失败: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := atomicWrite(skillFile, []byte(entry.Content)); err != nil {
		return "", err
	}
	return skillFile, nil
}

// --- Agent apply：子代理定义文件 ---

// ApplyAgent 把子代理定义写入目标客户端的 agents 目录，返回 md 文件路径。
// 目标：claude → ~/.claude/agents/<name>.md。Claude Code 是唯一有子代理
// 文件约定的客户端，其它目标值一律报错并列出可用目标。
func (s *Store) ApplyAgent(id, target string) (string, error) {
	return s.ApplyAgentTo(s.home, id, target)
}

// ApplyAgentTo 与 ApplyAgent 相同，但写入指定家目录（WSL 发行版等）。
func (s *Store) ApplyAgentTo(home, id, target string) (string, error) {
	s.mu.Lock()
	i := s.agentIndex(id)
	var entry AgentEntry
	if i >= 0 {
		entry = s.Agents[i]
	}
	s.mu.Unlock()
	if i < 0 {
		return "", fmt.Errorf("agent 条目不存在: %s", id)
	}
	if home == "" {
		return "", errNoHome
	}
	if target != "claude" {
		return "", fmt.Errorf("不支持的 agent 目标: %s（支持 claude）", target)
	}

	agentFile := filepath.Join(home, ".claude", "agents", agentFileName(entry.Name)+".md")
	// 覆盖已有同名 md 前先备份该文件本身。
	if err := s.backupFile(agentFile); err != nil {
		return "", fmt.Errorf("备份失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(agentFile), 0o755); err != nil {
		return "", err
	}
	if err := atomicWrite(agentFile, []byte(buildAgentFile(entry))); err != nil {
		return "", err
	}
	return agentFile, nil
}

// buildAgentFile 生成子代理 md 文件的完整内容：
// YAML frontmatter（name/description 必有，tools/model 留空则整行省略）
// + 空行 + 正文（系统提示词）。name/description 等字段按约定应为单行文本，
// 这里原样输出不做改写，与用户在编辑器里看到并保存的内容保持一致。
func buildAgentFile(e AgentEntry) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + e.Name + "\n")
	b.WriteString("description: " + e.Description + "\n")
	if strings.TrimSpace(e.Tools) != "" {
		b.WriteString("tools: " + e.Tools + "\n")
	}
	if strings.TrimSpace(e.Model) != "" {
		b.WriteString("model: " + e.Model + "\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(e.Content)
	return b.String()
}

// --- 文件工具（与 clients 包同约定，但独立实现以避免包耦合） ---

// backupFile 把现有文件复制为 <file>.aether-bak-<时间戳>（同目录），
// 并裁剪到最近 5 份。目标不存在时无需备份（首次创建）。
func backupFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	bak := path + backupSuffix + time.Now().Format("20060102-150405")
	if err := os.WriteFile(bak, data, 0o600); err != nil {
		return err
	}
	pruneBackups(path)
	return nil
}

// pruneBackups 只保留最新的 maxBackups 份备份。
func pruneBackups(path string) {
	matches, _ := filepath.Glob(path + backupSuffix + "*")
	sort.Sort(sort.Reverse(sort.StringSlice(matches))) // 时间戳格式天然按字典序即时间序
	for i := maxBackups; i < len(matches); i++ {
		_ = os.Remove(matches[i])
	}
}

// BackupDir 返回集中备份目录（按需创建）。
func (s *Store) BackupDir() string {
	if s.backupRoot == "" {
		return ""
	}
	if err := os.MkdirAll(s.backupRoot, 0o755); err != nil {
		return ""
	}
	return s.backupRoot
}

// backupFile 在原有「旁边留一份」之外，再往集中目录写一份，供资源库
// 的 Backups 按钮打开。
func (s *Store) backupFile(path string) error {
	if err := backupFile(path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil || s.backupRoot == "" {
		return nil
	}
	if err := os.MkdirAll(s.backupRoot, 0o755); err != nil {
		return nil
	}
	base := regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(filepath.Base(path), "_")
	hash := fmt.Sprintf("%08x", fnv32a(filepath.Clean(path)))
	prefix := base + "-" + hash + backupSuffix
	stamp := time.Now().Format("20060102-150405")
	if err := os.WriteFile(filepath.Join(s.backupRoot, prefix+stamp), data, 0o600); err != nil {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(s.backupRoot, prefix+"*"))
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))
	for i := maxBackups; i < len(matches); i++ {
		_ = os.Remove(matches[i])
	}
	return nil
}

// BackupContent 把一段内容写进集中备份目录（技能内容不落在单个文件上）。
func (s *Store) BackupContent(name, content string) error {
	if s.backupRoot == "" {
		return nil
	}
	if err := os.MkdirAll(s.backupRoot, 0o755); err != nil {
		return err
	}
	base := regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(name, "_")
	hash := fmt.Sprintf("%08x", fnv32a(name))
	stamp := time.Now().Format("20060102-150405")
	return os.WriteFile(filepath.Join(s.backupRoot, base+"-"+hash+backupSuffix+stamp), []byte(content), 0o600)
}

func fnv32a(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// AppliedSkillAgents 返回库里的技能当前被哪些 Agent 放在自己的技能目录里。
func (s *Store) AppliedSkillAgents(name string) []string {
	return s.AppliedSkillAgentsTo(s.home, name)
}

// AppliedSkillAgentsTo 在指定家目录里查该技能被哪些客户端持有。
func (s *Store) AppliedSkillAgentsTo(home, name string) []string {
	if home == "" {
		return nil
	}
	dir := skillDirName(name)
	out := []string{}
	if fileExists(filepath.Join(home, ".claude", "skills", dir, "SKILL.md")) {
		out = append(out, "claude_code")
	}
	if fileExists(filepath.Join(home, ".agents", "skills", dir, "SKILL.md")) {
		out = append(out, "codex_cli", "gemini_cli", "opencode")
	}
	return out
}

// SkillFilePath 返回库里该技能在磁盘上的 SKILL.md（若已物化），否则空串。
func (s *Store) SkillFilePath(name string) string {
	p := filepath.Join(filepath.Dir(s.path), "skills", skillDirName(name), "SKILL.md")
	if fileExists(p) {
		return p
	}
	return ""
}

// AppliedAgentAgents 返回已写入的 Agent 目标（目前只有 Claude Code 有子代理文件约定）。
func (s *Store) AppliedAgentAgents(name string) []string {
	return s.AppliedAgentAgentsTo(s.home, name)
}

// AppliedAgentAgentsTo 在指定家目录里查该子代理被哪些客户端持有。
func (s *Store) AppliedAgentAgentsTo(home, name string) []string {
	if home == "" {
		return nil
	}
	if fileExists(filepath.Join(home, ".claude", "agents", agentFileName(name)+".md")) {
		return []string{"claude_code"}
	}
	return []string{}
}

// RemoveAppliedAgent 删除某 Agent 里的子代理定义文件；不存在视为已删除。
func (s *Store) RemoveAppliedAgent(name, agent string) error {
	return s.RemoveAppliedAgentTo(s.home, name, agent)
}

// RemoveAppliedAgentTo 与 RemoveAppliedAgent 相同，但从指定家目录移除。
func (s *Store) RemoveAppliedAgentTo(home, name, agent string) error {
	if agent != "claude_code" {
		return fmt.Errorf("不支持的 agent 目标: %s（支持 claude_code）", agent)
	}
	if home == "" {
		return errNoHome
	}
	path := filepath.Join(home, ".claude", "agents", agentFileName(name)+".md")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// RemoveAppliedSkill 从某目标的全局技能目录移除同名技能。
// 只删除我们能确认由库写入的内容（链接或仅含 SKILL.md 的目录），
// 目录里有用户自己的其他文件时拒绝，避免误删。
func (s *Store) RemoveAppliedSkill(name, target string) error {
	return s.RemoveAppliedSkillTo(s.home, name, target)
}

// RemoveAppliedSkillTo 与 RemoveAppliedSkill 相同，但从指定家目录移除。
func (s *Store) RemoveAppliedSkillTo(home, name, target string) error {
	if home == "" {
		return errNoHome
	}
	var base string
	switch target {
	case "claude":
		base = filepath.Join(home, ".claude", "skills")
	case "agents":
		base = filepath.Join(home, ".agents", "skills")
	default:
		return fmt.Errorf("不支持的 skill 目标: %s（支持 claude / agents）", target)
	}
	s.skillApplyMu.Lock()
	defer s.skillApplyMu.Unlock()
	dir := filepath.Join(base, skillDirName(name))
	if IsDirLink(dir) {
		return os.Remove(dir)
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		only := true
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !(name == "SKILL.md" || strings.HasPrefix(name, "SKILL.md.aether-")) {
				only = false
				break
			}
		}
		if only {
			return os.RemoveAll(dir)
		}
	}
	return fmt.Errorf("技能目录含其他文件，未删除: %s", dir)
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// atomicWrite 先写临时文件再改名，避免写一半崩溃留下损坏配置。
func atomicWrite(path string, data []byte) error {
	tmp := path + ".aether-tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// sortedKeys 返回 map 的键的升序序列，保证输出确定性（可 diff、可复测）。
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Home 返回库用来解析客户端文件的用户主目录。
func (s *Store) Home() string { return s.home }
