// Package sessions 扫描各 Agent 自己记录的会话。
//
// 每个 Agent 都把对话写在本地：Claude Code 在 ~/.claude/projects/<编码路径>/<uuid>.jsonl，
// Codex 在 ~/.codex/sessions/。这些文件是「这个 Agent 做过什么」的唯一记录，
// 也是用户想回看、想重开的入口。
//
// 性能约束：单个 jsonl 可能有几十 MB、上万行。这里只读文件头若干行取标题与工作目录，
// 用文件的修改时间当"最后活动"，不把整个文件读进内存。
package sessions

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// peek 的结果按 (路径, mtime, 大小) 缓存。会话文件只会追加，所以 mtime/size
// 没变时内容头也不会变——375 个会话每次请求都重开文件取标题要 5 秒
// （§二百九十七），缓存后只读新增/改动过的。
type peekKey struct {
	Path  string
	Mtime int64
	Size  int64
}

var (
	peekMu    sync.Mutex
	peekCache = map[peekKey][2]string{}
)

func peekCached(path string, mtime, size int64, read func() (string, string)) (string, string) {
	k := peekKey{Path: path, Mtime: mtime, Size: size}
	peekMu.Lock()
	if v, ok := peekCache[k]; ok {
		peekMu.Unlock()
		return v[0], v[1]
	}
	peekMu.Unlock()

	title, cwd := read()

	peekMu.Lock()
	if len(peekCache) > 4096 {
		peekCache = map[peekKey][2]string{}
	}
	peekCache[k] = [2]string{title, cwd}
	peekMu.Unlock()
	return title, cwd
}

// Session 是一个会话的摘要。
type Session struct {
	ID       string `json:"id"`
	Agent    string `json:"agent"`
	Title    string `json:"title,omitempty"`
	Cwd      string `json:"cwd,omitempty"`
	Last     int64  `json:"last"`
	Path     string `json:"-"`
	Size     int64  `json:"size,omitempty"`
	Provider string `json:"provider,omitempty"`
	// WSL 是所在发行版（空表示本机）；资源库/会话页用它显示 WSL 徽标。
	WSL string `json:"-"`
}

// sessionRoot 是一个用户目录；WSL 非空表示它来自 WSL 发行版。
type sessionRoot struct {
	Home string
	WSL  string
}

// Scan 扫描本机（含 WSL 发行版，只读）全部已知 Agent 的会话，按最后活动时间倒序。
//
// 任何一个目录读失败都只是少几条记录，不影响其他来源。
func Scan() []Session {
	var out []Session
	for _, root := range scanRoots() {
		out = append(out, scanClaude(root)...)
		out = append(out, scanCodex(root)...)
		out = append(out, scanGemini(root)...)
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Last > out[j].Last })
	return out
}

// wslEnabled 控制是否扫描 WSL 家目录（网关的"WSL 接入"开关会同步它）。
// 关掉后连 WSL 根都不枚举：不跑 wsl.exe、不 stat UNC。
var wslEnabled atomic.Bool

func init() { wslEnabled.Store(true) }

// SetWSLEnabled 由网关在读取/修改设置时调用。
func SetWSLEnabled(on bool) { wslEnabled.Store(on) }

// scanRoots 返回本机用户目录与 WSL 里的用户目录（UNC 路径）。
func scanRoots() []sessionRoot {
	seen := map[string]bool{}
	out := []sessionRoot{}
	add := func(h, wsl string) {
		h = strings.TrimSpace(h)
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		out = append(out, sessionRoot{Home: h, WSL: wsl})
	}
	if h, err := os.UserHomeDir(); err == nil {
		add(h, "")
	}
	if wslEnabled.Load() {
		for _, r := range wslRoots() {
			add(r.Home, r.WSL)
		}
	}
	return out
}

/* ---------------- Claude Code ---------------- */

// scanClaude 扫描 <home>/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl
func scanClaude(root sessionRoot) []Session {
	home := root.Home
	rootDir := filepath.Join(home, ".claude", "projects")
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return nil
	}

	var out []Session
	for _, dir := range entries {
		if !dir.IsDir() {
			continue
		}
		dirPath := filepath.Join(rootDir, dir.Name())
		files, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			p := filepath.Join(dirPath, f.Name())
			info, err := f.Info()
			if err != nil {
				continue
			}
			s := Session{
				ID:    strings.TrimSuffix(f.Name(), ".jsonl"),
				Agent: "claude-code",
				Last:  info.ModTime().Unix(),
				Size:  info.Size(),
				Path:  p,
				WSL:   root.WSL,
			}
			// 从编码后的目录名还原工作目录，比读文件便宜
			s.Cwd = decodeClaudeDir(dir.Name())

			title, cwd := peekCached(p, info.ModTime().Unix(), info.Size(), func() (string, string) {
				return peekClaude(p, 60)
			})
			if title != "" {
				s.Title = title
			}
			if cwd != "" {
				s.Cwd = cwd
			}
			if s.Title == "" {
				s.Title = "(无标题会话)"
			}
			out = append(out, s)
		}
	}
	return out
}

// peekClaude 读文件前 n 行，取第一条用户消息作为标题，并取 cwd。
func peekClaude(path string, n int) (title, cwd string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	for i := 0; i < n && sc.Scan(); i++ {
		var ev struct {
			Type    string          `json:"type"`
			Cwd     string          `json:"cwd"`
			Message json.RawMessage `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		if cwd == "" && ev.Cwd != "" {
			cwd = ev.Cwd
		}
		if title == "" && ev.Type == "user" {
			title = extractText(ev.Message)
		}
		if title != "" && cwd != "" {
			break
		}
	}
	return title, cwd
}

// extractText 从 message 里取纯文本，兼容两种形态：
//
//	{"content": "字符串"}
//	{"content": [{"type":"text","text":"..."}]}
func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil || len(m.Content) == 0 {
		return ""
	}

	// 形态一：字符串
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return clip(s)
	}

	// 形态二：内容块数组
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(m.Content, &blocks) == nil {
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				return clip(b.Text)
			}
		}
	}
	return ""
}

// clip 把标题压成一行、截断到 80 字符。
func clip(s string) string {
	s = strings.TrimSpace(s)
	// 去掉换行与多余空白，标题一行显示
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > 80 {
		return string(r[:80]) + "…"
	}
	return s
}

// decodeClaudeDir 把 Claude Code 的目录编码还原成路径。
//
// 编码规则：把路径里的分隔符与冒号替换成 '-'，所以 "C:\Users\me\x" -> "C--Users-me-x"。
// 这个编码有损（原本的 '-' 和分隔符无法区分），因此只在前 60 行里读不到 cwd 时才用，
// 且仅作展示用途。
func decodeClaudeDir(name string) string {
	if name == "" {
		return ""
	}
	s := strings.ReplaceAll(name, "--", ":/")
	s = strings.ReplaceAll(s, "-", "/")
	s = strings.ReplaceAll(s, ":/", ":/")
	return s
}

/* ---------------- Codex ---------------- */

// scanCodex 扫描 <home>/.codex/sessions 下的 jsonl。
func scanCodex(root sessionRoot) []Session {
	home := root.Home
	rootDir := filepath.Join(home, ".codex", "sessions")
	if info, err := os.Stat(rootDir); err != nil || !info.IsDir() {
		return nil
	}

	var out []Session
	// 不用 filepath.WalkDir：Windows 上 ~/.codex/sessions 常是指向其它盘的
	// 目录联接（junction），Lstat 会把它当成普通文件，WalkDir 因此一条都
	// 不往下走。os.ReadDir + os.Stat 会跟随联接，统一 Codex 会话历史也能扫到。
	walkSessionFiles(rootDir, 0, func(p string, info os.FileInfo) {
		if !strings.HasSuffix(strings.ToLower(info.Name()), ".jsonl") {
			return
		}
		s := Session{
			ID:    strings.TrimSuffix(info.Name(), ".jsonl"),
			Agent: "codex",
			Last:  info.ModTime().Unix(),
			Size:  info.Size(),
			Path:  p,
			WSL:   root.WSL,
		}
		if t, cwd := peekCached(p, info.ModTime().Unix(), info.Size(), func() (string, string) {
			return peekCodex(p, 40)
		}); t != "" {
			s.Title = t
			s.Cwd = cwd
		}
		if s.Title == "" {
			s.Title = "(无标题会话)"
		}
		out = append(out, s)
	})
	return out
}

// walkSessionFiles 递归遍历目录，跟随符号链接/目录联接；深度上限防止环。
func walkSessionFiles(root string, depth int, fn func(path string, info os.FileInfo)) {
	if depth > 8 {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		p := filepath.Join(root, e.Name())
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.IsDir() {
			walkSessionFiles(p, depth+1, fn)
			continue
		}
		fn(p, info)
	}
}

// peekCodex 从 Codex 的 rollout 文件里取首条 user 消息。
func peekCodex(path string, n int) (title, cwd string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	for i := 0; i < n && sc.Scan(); i++ {
		var ev struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		var pl struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Cwd     string          `json:"cwd"`
			Message string          `json:"message"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(ev.Payload, &pl) != nil {
			continue
		}
		if cwd == "" && pl.Cwd != "" {
			cwd = pl.Cwd
		}
		if title == "" && (pl.Role == "user" || pl.Type == "user_message") {
			title = extractText(pl.Content)
			// event_msg 的 user_message 把正文放在 payload.message，
			// 不是 response_item 的 content 数组。
			if title == "" && strings.TrimSpace(pl.Message) != "" {
				title = clip(strings.TrimSpace(pl.Message))
			}
		}
		if title != "" && cwd != "" {
			break
		}
	}
	return title, cwd
}

// scanGemini 扫描 <home>/.gemini/tmp/<project>/chats/session-*.jsonl。
// Gemini CLI 的 project 目录名就是项目标签；会话文件是 JSONL 快照流。
func scanGemini(root sessionRoot) []Session {
	base := filepath.Join(root.Home, ".gemini", "tmp")
	projects, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	// Gemini CLI 不自动清理旧聊天：本机可能有数千个历史文件。页面只展示
	// 最近 90 天的会话，避免一次读取 700MB+ 的头信息。
	cutoff := time.Now().AddDate(0, 0, -90).Unix()
	var out []Session
	for _, proj := range projects {
		if !proj.IsDir() {
			continue
		}
		chats := filepath.Join(base, proj.Name(), "chats")
		files, err := os.ReadDir(chats)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			p := filepath.Join(chats, f.Name())
			info, err := f.Info()
			if err != nil || info.ModTime().Unix() < cutoff {
				continue
			}
			title, _ := peekCached(p, info.ModTime().Unix(), info.Size(), func() (string, string) {
				return peekGemini(p), ""
			})
			if title == "" {
				title = "(无标题会话)"
			}
			out = append(out, Session{
				ID:    proj.Name() + "/" + strings.TrimSuffix(f.Name(), ".jsonl"),
				Agent: "gemini-cli", Title: title, Cwd: proj.Name(),
				Last: info.ModTime().Unix(), Size: info.Size(), Path: p, WSL: root.WSL,
			})
		}
	}
	return out
}

func peekGemini(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for i := 0; i < 40 && sc.Scan(); i++ {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if t := geminiUserText(m); t != "" {
			return t
		}
	}
	return ""
}

func geminiUserText(m map[string]any) string {
	if typ, _ := m["type"].(string); typ == "user" {
		if t := cleanGeminiText(geminiContentText(m["content"])); t != "" {
			return t
		}
	}
	set, _ := m["$set"].(map[string]any)
	if set == nil {
		return ""
	}
	msgs, _ := set["messages"].([]any)
	for _, raw := range msgs {
		mm, _ := raw.(map[string]any)
		if mm == nil {
			continue
		}
		if typ, _ := mm["type"].(string); typ == "user" {
			if t := cleanGeminiText(geminiContentText(mm["content"])); t != "" {
				return t
			}
		}
	}
	return ""
}

func geminiContentText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var b strings.Builder
		for _, raw := range x {
			if item, ok := raw.(map[string]any); ok {
				if t, _ := item["text"].(string); t != "" {
					b.WriteString(t)
					b.WriteByte('\n')
				}
			}
		}
		return b.String()
	}
	return ""
}

func cleanGeminiText(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "</session_context>"); i >= 0 {
		s = strings.TrimSpace(s[i+len("</session_context>"):])
	}
	if len([]rune(s)) > 120 {
		s = string([]rune(s)[:120]) + "…"
	}
	return s
}

// Stat 汇总会话数量与活跃情况。
func Stat(list []Session, days int) map[string]any {
	since := time.Now().AddDate(0, 0, -days).Unix()
	byAgent := make(map[string]int, 4)
	n := 0
	for _, s := range list {
		if s.Last < since {
			continue
		}
		n++
		byAgent[s.Agent]++
	}
	return map[string]any{
		"days":     days,
		"sessions": n,
		"byAgent":  byAgent,
		"total":    len(list),
	}
}
