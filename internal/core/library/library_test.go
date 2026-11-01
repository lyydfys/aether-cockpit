package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"aether-cockpit/internal/core/jsoncutil"
)

// newTestStore 建一个落在临时目录的库，home 也指向另一个临时目录。
func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	home := t.TempDir()
	s := NewStoreWithHome(filepath.Join(t.TempDir(), "library.json"), home)
	return s, home
}

func mustSavePrompt(t *testing.T, s *Store, name, content string) PromptEntry {
	t.Helper()
	e, err := s.SavePrompt(PromptEntry{Name: name, Content: content})
	if err != nil {
		t.Fatalf("SavePrompt(%q): %v", name, err)
	}
	return e
}

func mustSaveMCP(t *testing.T, s *Store, name, config string) MCPEntry {
	t.Helper()
	e, err := s.SaveMCP(MCPEntry{Name: name, Config: json.RawMessage(config)})
	if err != nil {
		t.Fatalf("SaveMCP(%q): %v", name, err)
	}
	return e
}

func mustSaveSkill(t *testing.T, s *Store, name, content string) SkillEntry {
	t.Helper()
	e, err := s.SaveSkill(SkillEntry{Name: name, Content: content})
	if err != nil {
		t.Fatalf("SaveSkill(%q): %v", name, err)
	}
	return e
}

func mustSaveAgent(t *testing.T, s *Store, name, content string) AgentEntry {
	t.Helper()
	e, err := s.SaveAgent(AgentEntry{Name: name, Content: content})
	if err != nil {
		t.Fatalf("SaveAgent(%q): %v", name, err)
	}
	return e
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	return string(b)
}

// sameJSON 判断两段 JSON 是否语义等价（忽略空白与键序）。
func sameJSON(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("解析 JSON: %v", err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("解析 JSON: %v", err)
	}
	return reflect.DeepEqual(va, vb)
}

// --- 存储层 ---

func TestStoreCRUDAndPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "library.json")
	s := NewStoreWithHome(path, t.TempDir())

	// 新建：生成 ID 与时间戳。
	p, err := s.SavePrompt(PromptEntry{Name: "代码审查", Content: "请逐行审查"})
	if err != nil {
		t.Fatalf("SavePrompt: %v", err)
	}
	if p.ID == "" || p.CreatedAt == 0 {
		t.Fatalf("新建未生成 id/created_at: %+v", p)
	}
	// 中文名称 slug 为空，ID 应是 "item-<hash>" 形态。
	if !strings.HasPrefix(p.ID, "item-") {
		t.Fatalf("中文名称的 ID 应为 item-*，实际 %q", p.ID)
	}

	// 同 ID 覆盖：视为更新，CreatedAt 沿用。
	p2, err := s.SavePrompt(PromptEntry{ID: p.ID, Name: "代码审查 v2", Content: "新的内容"})
	if err != nil {
		t.Fatalf("SavePrompt(upsert): %v", err)
	}
	if p2.ID != p.ID || p2.CreatedAt != p.CreatedAt {
		t.Fatalf("upsert 改变了 id/created_at: %+v -> %+v", p, p2)
	}
	snap := s.List()
	if len(snap.Prompts) != 1 || snap.Prompts[0].Name != "代码审查 v2" {
		t.Fatalf("upsert 后状态不对: %+v", snap.Prompts)
	}

	// List 是深拷贝：改快照不影响内部状态。
	snap.Prompts[0].Name = "mutated"
	if s.List().Prompts[0].Name == "mutated" {
		t.Fatal("List 返回的不是深拷贝")
	}

	// MCP / Skill 一并入库。
	m := mustSaveMCP(t, s, "fetch", `{"command":"uvx","args":["mcp-server-fetch"]}`)
	if !strings.HasPrefix(m.ID, "fetch-") {
		t.Fatalf("英文名 ID 应有 slug 前缀，实际 %q", m.ID)
	}
	sk := mustSaveSkill(t, s, "pdf", "---\nname: pdf\n---\n# PDF 工具")

	// 落盘后重开：持久化往返。
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	s2 := NewStoreWithHome(path, t.TempDir())
	snap2 := s2.List()
	if len(snap2.Prompts) != 1 || snap2.Prompts[0].Content != "新的内容" {
		t.Fatalf("prompt 往返不一致: %+v", snap2.Prompts)
	}
	// 注意：json.MarshalIndent 会重排 RawMessage 的缩进，所以 Config 只做语义比较。
	if len(snap2.MCPServers) != 1 || !sameJSON(t, snap2.MCPServers[0].Config, json.RawMessage(m.Config)) {
		t.Fatalf("mcp 往返不一致: %+v", snap2.MCPServers)
	}
	if len(snap2.Skills) != 1 || snap2.Skills[0].Content != sk.Content {
		t.Fatalf("skill 往返不一致: %+v", snap2.Skills)
	}

	// 删除：删一次成功，再删返回 false。
	if !s2.DeletePrompt(p.ID) || s2.DeletePrompt(p.ID) {
		t.Fatal("DeletePrompt 返回值不对")
	}
	if !s2.DeleteMCP(m.ID) || !s2.DeleteSkill(sk.ID) {
		t.Fatal("DeleteMCP/DeleteSkill 失败")
	}
	if got := s2.List(); len(got.Prompts)+len(got.MCPServers)+len(got.Skills) != 0 {
		t.Fatalf("删除后仍有残留: %+v", got)
	}

	// 校验：name / content / config 必填。
	if _, err := s2.SavePrompt(PromptEntry{Content: "x"}); err == nil {
		t.Fatal("空 name 应报错")
	}
	if _, err := s2.SaveMCP(MCPEntry{Name: "x", Config: json.RawMessage(`[]`)}); err == nil {
		t.Fatal("非对象 config 应报错")
	}
	if _, err := s2.SaveSkill(SkillEntry{Name: "x", Content: ""}); err == nil {
		t.Fatal("空 content 应报错")
	}
}

func TestStoreLoadCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.json")
	if err := os.WriteFile(path, []byte("{ 不是 JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStoreWithHome(path, t.TempDir())
	if got := s.List(); len(got.Prompts)+len(got.MCPServers)+len(got.Skills) != 0 {
		t.Fatal("损坏文件应按空库开始")
	}
	// 损坏文件不被覆盖性删除：Save 之后的落盘属于用户可预期的新状态，此处只验证启动行为。
}

// --- Prompt apply：托管块 ---

func TestApplyPromptManagedBlock(t *testing.T) {
	s, home := newTestStore(t)
	entry := mustSavePrompt(t, s, "风格指南", "使用中文回复。\n简洁优先。")

	target := filepath.Join(home, ".claude", "CLAUDE.md")
	userContent := "# 我的项目\n\n这里是用户自己的说明，不许动。\n"
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(userContent), 0o600); err != nil {
		t.Fatal(err)
	}

	begin := promptBegin("风格指南")
	end := promptEnd("风格指南")

	path, err := s.ApplyPrompt(entry.ID, "claude")
	if err != nil {
		t.Fatalf("ApplyPrompt: %v", err)
	}
	if path != target {
		t.Fatalf("路径不对: %s", path)
	}
	text := readFile(t, target)
	// 用户内容一个字节都没动（出现在开头且完整）。
	if !strings.HasPrefix(text, userContent) {
		t.Fatalf("用户内容被改动了:\n%s", text)
	}
	if !strings.Contains(text, "使用中文回复。") {
		t.Fatal("块内容缺失")
	}
	if !strings.HasSuffix(text, end+"\n") {
		t.Fatalf("块不在文件末尾:\n%s", text)
	}

	// 二次 apply 幂等：不产生重复块，用户内容仍原样。
	if _, err := s.ApplyPrompt(entry.ID, "claude"); err != nil {
		t.Fatalf("二次 ApplyPrompt: %v", err)
	}
	text2 := readFile(t, target)
	if strings.Count(text2, begin) != 1 || strings.Count(text2, end) != 1 {
		t.Fatalf("二次 apply 后标记数量不对: begin=%d end=%d",
			strings.Count(text2, begin), strings.Count(text2, end))
	}
	if !strings.HasPrefix(text2, userContent) {
		t.Fatal("二次 apply 改动了用户内容")
	}

	// 更新条目后 apply：旧内容被替换为新内容。
	entry.Content = "使用英文回复。"
	if _, err := s.SavePrompt(entry); err != nil {
		t.Fatalf("SavePrompt(update): %v", err)
	}
	if _, err := s.ApplyPrompt(entry.ID, "claude"); err != nil {
		t.Fatalf("更新后 ApplyPrompt: %v", err)
	}
	text3 := readFile(t, target)
	if strings.Contains(text3, "使用中文回复。") || !strings.Contains(text3, "使用英文回复。") {
		t.Fatalf("块内容未更新:\n%s", text3)
	}
	if strings.Count(text3, begin) != 1 {
		t.Fatal("更新后出现了重复块")
	}

	// 备份产生。
	matches, _ := filepath.Glob(target + backupSuffix + "*")
	if len(matches) == 0 {
		t.Fatal("apply 后没有产生备份")
	}
}

func TestApplyPromptNewFileAndTargets(t *testing.T) {
	s, home := newTestStore(t)
	entry := mustSavePrompt(t, s, "临场提示", "保持冷静。")

	// 文件不存在：新建并带标题行。
	path, err := s.ApplyPrompt(entry.ID, "gemini")
	if err != nil {
		t.Fatalf("ApplyPrompt(gemini): %v", err)
	}
	if path != filepath.Join(home, ".gemini", "GEMINI.md") {
		t.Fatalf("gemini 路径不对: %s", path)
	}
	text := readFile(t, path)
	if !strings.HasPrefix(text, "# 临场提示\n\n") {
		t.Fatalf("新文件缺少标题行:\n%s", text)
	}
	if !strings.Contains(text, promptBegin("临场提示")) {
		t.Fatal("新文件缺少托管块")
	}

	// codex 目标路径。
	path2, err := s.ApplyPrompt(entry.ID, "codex")
	if err != nil {
		t.Fatalf("ApplyPrompt(codex): %v", err)
	}
	if path2 != filepath.Join(home, ".codex", "AGENTS.md") {
		t.Fatalf("codex 路径不对: %s", path2)
	}

	// 未知目标。
	if _, err := s.ApplyPrompt(entry.ID, "agents"); err == nil {
		t.Fatal("prompts 不支持 agents 目标，应报错")
	}
}

func TestApplyPromptOrphanBeginRefusesWrite(t *testing.T) {
	s, home := newTestStore(t)
	entry := mustSavePrompt(t, s, "半截块", "新内容")

	target := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := "# 说明\n\n" + promptBegin("半截块") + "\n用户改坏了一半\n"
	if err := os.WriteFile(target, []byte(orphan), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ApplyPrompt(entry.ID, "claude"); err == nil {
		t.Fatal("孤立 begin 应报错")
	}
	// 报错即不改写：文件保持原样，等用户手工处理。
	if got := readFile(t, target); got != orphan {
		t.Fatalf("孤立 begin 时文件被改写了:\n%s", got)
	}
}

// --- MCP apply：JSON 合并 ---

func TestApplyMCPJSONMerge(t *testing.T) {
	s, home := newTestStore(t)
	entry := mustSaveMCP(t, s, "fetch", `{"command":"uvx","args":["mcp-server-fetch"],"env":{"API_KEY":"k"}}`)

	claudeJSON := filepath.Join(home, ".claude.json")
	preset := `{"num":42,"theme":"dark","nested":{"deep":[1,2,3]},"mcpServers":{"old":{"command":"x"}},"projects":{}}`
	if err := os.WriteFile(claudeJSON, []byte(preset), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := s.ApplyMCP(entry.ID, "claude")
	if err != nil {
		t.Fatalf("ApplyMCP(claude): %v", err)
	}
	if path != claudeJSON {
		t.Fatalf("路径不对: %s", path)
	}

	var root map[string]any
	if err := json.Unmarshal([]byte(readFile(t, claudeJSON)), &root); err != nil {
		t.Fatalf("写出的文件不是合法 JSON: %v", err)
	}
	// 其余键原样保留。
	if root["num"] != float64(42) || root["theme"] != "dark" {
		t.Fatalf("顶层键丢失: num=%v theme=%v", root["num"], root["theme"])
	}
	if _, ok := root["nested"].(map[string]any); !ok {
		t.Fatalf("nested 丢失: %v", root["nested"])
	}
	servers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers 形态不对: %T", root["mcpServers"])
	}
	if _, ok := servers["old"]; !ok {
		t.Fatal("原有的 mcpServers.old 被删除了")
	}
	got, ok := servers["fetch"].(map[string]any)
	if !ok || got["command"] != "uvx" {
		t.Fatalf("mcpServers.fetch 未写入: %v", servers["fetch"])
	}

	// 二次 apply 幂等。
	if _, err := s.ApplyMCP(entry.ID, "claude"); err != nil {
		t.Fatalf("二次 ApplyMCP: %v", err)
	}

	// 现有文件非法 JSON：拒绝改写，文件原样。
	if err := os.WriteFile(claudeJSON, []byte("{ 坏掉的"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyMCP(entry.ID, "claude"); err == nil {
		t.Fatal("非法 JSON 应拒绝改写")
	}
	if got := readFile(t, claudeJSON); got != "{ 坏掉的" {
		t.Fatalf("拒绝改写时文件被动了: %q", got)
	}
}

func TestApplyMCPGeminiSettings(t *testing.T) {
	s, home := newTestStore(t)
	entry := mustSaveMCP(t, s, "搜索", `{"command":"npx","args":["-y","search-mcp"]}`)

	settings := filepath.Join(home, ".gemini", "settings.json")
	preset := `{"theme":"dracula","mcpServers":{"other":{"command":"a"}}}`
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(preset), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := s.ApplyMCP(entry.ID, "gemini")
	if err != nil {
		t.Fatalf("ApplyMCP(gemini): %v", err)
	}
	if path != settings {
		t.Fatalf("路径不对: %s", path)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(readFile(t, settings)), &root); err != nil {
		t.Fatalf("非法 JSON: %v", err)
	}
	if root["theme"] != "dracula" {
		t.Fatal("theme 丢失")
	}
	servers := root["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Fatal("原有 other server 丢失")
	}
	if _, ok := servers["搜索"]; !ok {
		t.Fatal("新 server 未写入")
	}
}

// --- MCP apply：TOML 托管块 ---

func TestApplyMCPTOML(t *testing.T) {
	s, home := newTestStore(t)
	entry := mustSaveMCP(t, s, "fetch", `{
		"command":"uvx",
		"args":["mcp-server-fetch","--verbose"],
		"env":{"API_KEY":"abc","RETRIES":2},
		"timeout":40,
		"trust":true
	}`)

	tomlPath := filepath.Join(home, ".codex", "config.toml")
	preset := "# 用户的注释\nmodel = \"gpt-5\"\n\n[model_providers.aether]\nname = \"A\"\n"
	if err := os.MkdirAll(filepath.Dir(tomlPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tomlPath, []byte(preset), 0o600); err != nil {
		t.Fatal(err)
	}

	path, err := s.ApplyMCP(entry.ID, "codex")
	if err != nil {
		t.Fatalf("ApplyMCP(codex): %v", err)
	}
	if path != tomlPath {
		t.Fatalf("路径不对: %s", path)
	}
	text := readFile(t, tomlPath)
	// 用户内容原样保留。
	for _, want := range []string{"# 用户的注释", `model = "gpt-5"`, "[model_providers.aether]"} {
		if !strings.Contains(text, want) {
			t.Fatalf("用户内容 %q 丢失:\n%s", want, text)
		}
	}
	// 托管块：标量、数组、子表、数字/布尔。
	for _, want := range []string{
		"[mcp_servers.fetch]",
		`command = "uvx"`,
		`args = ["mcp-server-fetch", "--verbose"]`,
		"[mcp_servers.fetch.env]",
		`API_KEY = "abc"`,
		"RETRIES = 2",
		"timeout = 40",
		"trust = true",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("TOML 块缺少 %q:\n%s", want, text)
		}
	}

	// 二次 apply 幂等（旧块被删除再写）。
	if _, err := s.ApplyMCP(entry.ID, "codex"); err != nil {
		t.Fatalf("二次 ApplyMCP: %v", err)
	}
	text2 := readFile(t, tomlPath)
	if strings.Count(text2, "[mcp_servers.fetch]") != 1 {
		t.Fatalf("二次 apply 出现重复块:\n%s", text2)
	}
	if strings.Count(text2, "[model_providers.aether]") != 1 {
		t.Fatal("二次 apply 动了用户的其它 section")
	}

	// 同前缀的其它 server 不受影响。
	other := mustSaveMCP(t, s, "fetch-extra", `{"command":"x"}`)
	if _, err := s.ApplyMCP(other.ID, "codex"); err != nil {
		t.Fatalf("ApplyMCP(fetch-extra): %v", err)
	}
	text3 := readFile(t, tomlPath)
	if !strings.Contains(text3, "[mcp_servers.fetch-extra]") ||
		strings.Count(text3, "[mcp_servers.fetch]") != 1 {
		t.Fatalf("同前缀 server 互相影响:\n%s", text3)
	}

	// 超过一层嵌套：报错。
	deep := mustSaveMCP(t, s, "deep", `{"a":{"b":{"c":1}}}`)
	if _, err := s.ApplyMCP(deep.ID, "codex"); err == nil {
		t.Fatal("两层嵌套对象应报错")
	}
	// null 值：报错。
	nullEntry := mustSaveMCP(t, s, "withnull", `{"k":null}`)
	if _, err := s.ApplyMCP(nullEntry.ID, "codex"); err == nil {
		t.Fatal("null 值应报错")
	}
}

func TestApplyMCPTOMLQuotedKeys(t *testing.T) {
	s, _ := newTestStore(t)
	// 名称含空格：裸键不合法，应写成引号形式。
	entry := mustSaveMCP(t, s, "my server", `{"command":"x"}`)
	path, err := s.ApplyMCP(entry.ID, "codex")
	if err != nil {
		t.Fatalf("ApplyMCP: %v", err)
	}
	text := readFile(t, path)
	if !strings.Contains(text, `[mcp_servers."my server"]`) {
		t.Fatalf("引号形式的 section 缺失:\n%s", text)
	}
	// 值中的反斜杠与引号需要转义。
	win := mustSaveMCP(t, s, "paths", `{"command":"C:\\tools\\run.exe","note":"say \"hi\""}`)
	if _, err := s.ApplyMCP(win.ID, "codex"); err != nil {
		t.Fatalf("ApplyMCP(paths): %v", err)
	}
	text2 := readFile(t, path)
	if !strings.Contains(text2, `command = "C:\\tools\\run.exe"`) {
		t.Fatalf("反斜杠未转义:\n%s", text2)
	}
	if !strings.Contains(text2, `note = "say \"hi\""`) {
		t.Fatalf("引号未转义:\n%s", text2)
	}
}

// --- Skill apply ---

func TestApplySkillDir(t *testing.T) {
	s, home := newTestStore(t)
	skill := mustSaveSkill(t, s, "pdf 工具", "---\nname: pdf\n---\n# PDF 处理")

	// claude 目标：目录名把空格替换为 '-'。
	path, err := s.ApplySkill(skill.ID, "claude")
	if err != nil {
		t.Fatalf("ApplySkill(claude): %v", err)
	}
	want := filepath.Join(home, ".claude", "skills", "pdf-工具", "SKILL.md")
	if path != want {
		t.Fatalf("路径不对: %s", path)
	}
	if got := readFile(t, path); got != skill.Content {
		t.Fatalf("SKILL.md 内容不一致:\n%s", got)
	}

	// agents 目标。
	path2, err := s.ApplySkill(skill.ID, "agents")
	if err != nil {
		t.Fatalf("ApplySkill(agents): %v", err)
	}
	if path2 != filepath.Join(home, ".agents", "skills", "pdf-工具", "SKILL.md") {
		t.Fatalf("agents 路径不对: %s", path2)
	}

	// 未知目标报错。
	if _, err := s.ApplySkill(skill.ID, "codex"); err == nil {
		t.Fatal("skills 不支持 codex 目标，应报错")
	}

	// 不存在的条目报错。
	if _, err := s.ApplySkill("nope", "claude"); err == nil {
		t.Fatal("不存在的条目应报错")
	}
}

func TestApplySkillOverwriteBacksUp(t *testing.T) {
	s, _ := newTestStore(t)
	skill := mustSaveSkill(t, s, "backup-probe", "v1")
	skillFile, err := s.ApplySkill(skill.ID, "claude")
	if err != nil {
		t.Fatalf("首次 ApplySkill: %v", err)
	}
	// 目录不存在时首次写入不应产生备份。
	if matches, _ := filepath.Glob(skillFile + backupSuffix + "*"); len(matches) != 0 {
		t.Fatalf("首次写入不应产生备份: %v", matches)
	}
	// 更新内容后再次 apply：覆盖前备份旧文件。
	skill.Content = "v2"
	if _, err := s.SaveSkill(skill); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplySkill(skill.ID, "claude"); err != nil {
		t.Fatalf("二次 ApplySkill: %v", err)
	}
	matches, _ := filepath.Glob(skillFile + backupSuffix + "*")
	if len(matches) != 1 {
		t.Fatalf("覆盖前应产生 1 份备份，实际 %d", len(matches))
	}
	if got := readFile(t, matches[0]); got != "v1" {
		t.Fatalf("备份内容不对: %q", got)
	}
	if got := readFile(t, skillFile); got != "v2" {
		t.Fatalf("新内容未写入: %q", got)
	}
}

// --- Agent：存储 ---

func TestSaveAgentCRUDAndPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "library.json")
	s := NewStoreWithHome(path, t.TempDir())

	// 新建：生成 ID 与时间戳。
	a, err := s.SaveAgent(AgentEntry{
		Name:        "code-reviewer",
		Description: "审查代码",
		Tools:       "Read, Grep",
		Model:       "sonnet",
		Content:     "审查提示词",
	})
	if err != nil {
		t.Fatalf("SaveAgent: %v", err)
	}
	if a.ID == "" || a.CreatedAt == 0 {
		t.Fatalf("新建未生成 id/created_at: %+v", a)
	}
	if !strings.HasPrefix(a.ID, "code-reviewer-") {
		t.Fatalf("英文名 ID 应有 slug 前缀，实际 %q", a.ID)
	}

	// 同 ID 覆盖：视为更新，CreatedAt 沿用，可选字段整体覆盖（可为空）。
	a2, err := s.SaveAgent(AgentEntry{ID: a.ID, Name: "code-reviewer", Content: "新提示词"})
	if err != nil {
		t.Fatalf("SaveAgent(upsert): %v", err)
	}
	if a2.ID != a.ID || a2.CreatedAt != a.CreatedAt {
		t.Fatalf("upsert 改变了 id/created_at: %+v -> %+v", a, a2)
	}
	snap := s.List()
	if len(snap.Agents) != 1 || snap.Agents[0].Content != "新提示词" {
		t.Fatalf("upsert 后状态不对: %+v", snap.Agents)
	}
	if snap.Agents[0].Tools != "" || snap.Agents[0].Model != "" {
		t.Fatalf("upsert 未清空可选字段: %+v", snap.Agents[0])
	}

	// List 是深拷贝：改快照不影响内部状态。
	snap.Agents[0].Name = "mutated"
	if s.List().Agents[0].Name == "mutated" {
		t.Fatal("List 返回的不是深拷贝")
	}

	// 落盘后重开：持久化往返。
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	s2 := NewStoreWithHome(path, t.TempDir())
	snap2 := s2.List()
	if len(snap2.Agents) != 1 || snap2.Agents[0].Content != "新提示词" {
		t.Fatalf("agent 往返不一致: %+v", snap2.Agents)
	}

	// 删除：删一次成功，再删返回 false。
	if !s2.DeleteAgent(a.ID) || s2.DeleteAgent(a.ID) {
		t.Fatal("DeleteAgent 返回值不对")
	}
	if got := s2.List(); len(got.Agents) != 0 {
		t.Fatalf("删除后仍有残留: %+v", got.Agents)
	}

	// 校验：name / content 必填。
	if _, err := s2.SaveAgent(AgentEntry{Content: "x"}); err == nil {
		t.Fatal("空 name 应报错")
	}
	if _, err := s2.SaveAgent(AgentEntry{Name: "x"}); err == nil {
		t.Fatal("空 content 应报错")
	}
}

// --- Agent apply：子代理定义文件 ---

func TestApplyAgentFrontmatter(t *testing.T) {
	s, home := newTestStore(t)

	// 含 tools / model：frontmatter 按顺序完整生成。
	full, err := s.SaveAgent(AgentEntry{
		Name:        "code-reviewer",
		Description: "逐行审查代码变更",
		Tools:       "Read, Grep, Glob",
		Model:       "sonnet",
		Content:     "你是一名严格的代码审查员。\n只报告缺陷，不做风格评论。",
	})
	if err != nil {
		t.Fatalf("SaveAgent: %v", err)
	}
	path, err := s.ApplyAgent(full.ID, "claude")
	if err != nil {
		t.Fatalf("ApplyAgent: %v", err)
	}
	if want := filepath.Join(home, ".claude", "agents", "code-reviewer.md"); path != want {
		t.Fatalf("路径不对: %s", path)
	}
	want := "---\n" +
		"name: code-reviewer\n" +
		"description: 逐行审查代码变更\n" +
		"tools: Read, Grep, Glob\n" +
		"model: sonnet\n" +
		"---\n\n" +
		"你是一名严格的代码审查员。\n只报告缺陷，不做风格评论。"
	if got := readFile(t, path); got != want {
		t.Fatalf("文件内容不一致:\n%s", got)
	}

	// 不含 tools / model：两行整行省略；description 缺省时保留空值行。
	plain := mustSaveAgent(t, s, "笔记助手", "帮忙整理笔记")
	path2, err := s.ApplyAgent(plain.ID, "claude")
	if err != nil {
		t.Fatalf("ApplyAgent(笔记助手): %v", err)
	}
	if want := filepath.Join(home, ".claude", "agents", "笔记助手.md"); path2 != want {
		t.Fatalf("路径不对: %s", path2)
	}
	want2 := "---\nname: 笔记助手\ndescription: \n---\n\n帮忙整理笔记"
	if got := readFile(t, path2); got != want2 {
		t.Fatalf("文件内容不一致:\n%s", got)
	}

	// 名称带空格：文件名按 skill 的清理规则替换。
	spaced := mustSaveAgent(t, s, "data analyzer", "x")
	path3, err := s.ApplyAgent(spaced.ID, "claude")
	if err != nil {
		t.Fatalf("ApplyAgent(data analyzer): %v", err)
	}
	if want := filepath.Join(home, ".claude", "agents", "data-analyzer.md"); path3 != want {
		t.Fatalf("路径不对: %s", path3)
	}

	// 只有 Claude Code 有子代理文件约定：其它目标报错并指出可用目标。
	if _, err := s.ApplyAgent(plain.ID, "codex"); err == nil || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("codex 目标应报错并列出 claude: %v", err)
	}
	// 不存在的条目报错。
	if _, err := s.ApplyAgent("nope", "claude"); err == nil {
		t.Fatal("不存在的条目应报错")
	}
}

func TestApplyAgentBacksUpExistingFile(t *testing.T) {
	s, home := newTestStore(t)
	target := filepath.Join(home, ".claude", "agents", "backup-probe.md")
	// 预置用户已有的同名子代理文件。
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("旧版本"), 0o600); err != nil {
		t.Fatal(err)
	}

	entry, err := s.SaveAgent(AgentEntry{Name: "backup-probe", Description: "测试用", Content: "新版系统提示词"})
	if err != nil {
		t.Fatalf("SaveAgent: %v", err)
	}
	path, err := s.ApplyAgent(entry.ID, "claude")
	if err != nil {
		t.Fatalf("ApplyAgent: %v", err)
	}
	if path != target {
		t.Fatalf("路径不对: %s", path)
	}

	// 备份恰好 1 份，内容可还原。
	matches, _ := filepath.Glob(target + backupSuffix + "*")
	if len(matches) != 1 {
		t.Fatalf("应产生 1 份备份，实际 %d", len(matches))
	}
	if got := readFile(t, matches[0]); got != "旧版本" {
		t.Fatalf("备份内容不对: %q", got)
	}
	// 新内容写入。
	want := "---\nname: backup-probe\ndescription: 测试用\n---\n\n新版系统提示词"
	if got := readFile(t, target); got != want {
		t.Fatalf("新内容未写入:\n%s", got)
	}
}

// --- 备份裁剪 ---

func TestPruneBackups(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "x.md")
	if err := os.WriteFile(f, []byte("v"), 0o600); err != nil {
		t.Fatal(err)
	}
	stamps := []string{
		"20240101-000001", "20240101-000002", "20240101-000003",
		"20240101-000004", "20240101-000005", "20240101-000006", "20240101-000007",
	}
	for _, ts := range stamps {
		if err := os.WriteFile(f+backupSuffix+ts, []byte(ts), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pruneBackups(f)
	matches, _ := filepath.Glob(f + backupSuffix + "*")
	if len(matches) != maxBackups {
		t.Fatalf("应保留 %d 份备份，实际 %d", maxBackups, len(matches))
	}
	// 必须是最新的 5 份。
	for _, ts := range stamps[:2] {
		if _, err := os.Stat(f + backupSuffix + ts); !os.IsNotExist(err) {
			t.Fatalf("旧备份 %s 应被裁剪", ts)
		}
	}
}

// --- apply 目标对不存在的条目 ---

func TestApplyUnknownEntry(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.ApplyPrompt("nope", "claude"); err == nil {
		t.Fatal("ApplyPrompt 不存在条目应报错")
	}
	if _, err := s.ApplyMCP("nope", "claude"); err == nil {
		t.Fatal("ApplyMCP 不存在条目应报错")
	}
}

func TestApplyMCPOpenCode(t *testing.T) {
	s, home := newTestStore(t)
	entry := mustSaveMCP(t, s, "fetch", `{"command":"uvx","args":["mcp-server-fetch"],"env":{"API_KEY":"k"}}`)
	path := filepath.Join(home, ".config", "opencode", "opencode.json")
	preset := `{"$schema":"https://opencode.ai/config.json","theme":"x","provider":{"mine":{}},"mcp":{"old":{"type":"local","command":["a"]}}}`
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(preset), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := s.ApplyMCP(entry.ID, "opencode")
	if err != nil {
		t.Fatalf("ApplyMCP(opencode): %v", err)
	}
	if got != path {
		t.Fatalf("路径不对: %s", got)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(readFile(t, path)), &root); err != nil {
		t.Fatalf("非法 JSON: %v", err)
	}
	if root["$schema"] != "https://opencode.ai/config.json" || root["theme"] != "x" {
		t.Fatalf("顶层键丢失: %v", root)
	}
	if _, ok := root["provider"].(map[string]any)["mine"]; !ok {
		t.Fatalf("provider 丢失: %v", root["provider"])
	}
	mcp := root["mcp"].(map[string]any)
	if _, ok := mcp["old"]; !ok {
		t.Fatal("原有 mcp.old 被删除")
	}
	fetch := mcp["fetch"].(map[string]any)
	if fetch["type"] != "local" || fetch["enabled"] != true {
		t.Fatalf("local 形状不对: %v", fetch)
	}
	cmd := fetch["command"].([]any)
	if len(cmd) != 2 || cmd[0] != "uvx" || cmd[1] != "mcp-server-fetch" {
		t.Fatalf("command = %v", cmd)
	}
	if fetch["environment"].(map[string]any)["API_KEY"] != "k" {
		t.Fatalf("environment = %v", fetch["environment"])
	}

	remote := mustSaveMCP(t, s, "web", `{"type":"http","url":"https://example.com/mcp","headers":{"X":"y"}}`)
	if _, err := s.ApplyMCP(remote.ID, "opencode"); err != nil {
		t.Fatalf("ApplyMCP(opencode remote): %v", err)
	}
	var root2 map[string]any
	if err := json.Unmarshal([]byte(readFile(t, path)), &root2); err != nil {
		t.Fatal(err)
	}
	web := root2["mcp"].(map[string]any)["web"].(map[string]any)
	if web["type"] != "remote" || web["url"] != "https://example.com/mcp" || web["enabled"] != true {
		t.Fatalf("remote 形状不对: %v", web)
	}
	if _, bad := web["command"]; bad {
		t.Fatalf("remote 不应带 command: %v", web)
	}

	jsonc := "{ // config\n \"theme\": \"x\"\n}\n"
	if err := os.WriteFile(path, []byte(jsonc), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyMCP(entry.ID, "opencode"); err != nil {
		t.Fatalf("JSONC apply: %v", err)
	}
	rawJSONC := readFile(t, path)
	if !strings.Contains(rawJSONC, "// config") {
		t.Fatalf("JSONC 注释丢失: %s", rawJSONC)
	}
	js, err := jsoncutil.Parse([]byte(rawJSONC))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(js, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["theme"] != "x" {
		t.Fatalf("theme = %v", doc["theme"])
	}
	mcp = doc["mcp"].(map[string]any)
	if _, ok := mcp["fetch"]; !ok {
		t.Fatalf("fetch 未写入: %v", mcp)
	}
}
