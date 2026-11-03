package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/sessions"
)

// 会话删除 / 回收站 / 恢复 / 彻底清除。
//
// 之前的策略是"一律只读、删除返回 501"。理由（不在网关里改写用户会话）站得住，
// 但会话页因此缺了最基本的闭环：看完一个废弃会话想清掉，只能去对应 Agent 里翻。
//
// 这里补的是**可撤销**的删除：
//   - 删除 = 把会话文件搬进回收站目录并记一条 manifest（原始路径、大小、时间）；
//   - 恢复 = 按 manifest 搬回原位（原目录不存在时重建）；
//   - 彻底清除 = 删回收站里的副本，连 manifest 条目一起删；
//   - 只处理本机会话（WSL 会话跨发行版搬运风险太高，标记不可删）。
//
// 安全性取舍：删除的是用户数据，所以这里宁慢勿快——每次操作都先落 manifest、
// 再动文件；恢复时如果原路径已有文件，不覆盖，直接报冲突。

// trashEntry 是回收站里的一条记录。
type trashEntry struct {
	ID        string `json:"id"`
	Agent     string `json:"agent"`
	Title     string `json:"title,omitempty"`
	OrigPath  string `json:"orig_path"`
	TrashPath string `json:"trash_path"`
	Size      int64  `json:"size,omitempty"`
	DeletedAt int64  `json:"deleted_at"`
}

// sessionTrash 是回收站存储（manifest 落盘，文件放在同目录）。
type sessionTrash struct {
	mu       sync.Mutex
	root     string
	manifest string
	entries  []trashEntry
	loaded   bool
}

func newSessionTrash(dataDir string) *sessionTrash {
	root := filepath.Join(dataDir, "sessions-trash")
	return &sessionTrash{
		root:     root,
		manifest: filepath.Join(root, "manifest.json"),
	}
}

func (t *sessionTrash) loadLocked() {
	if t.loaded {
		return
	}
	t.loaded = true
	raw, err := os.ReadFile(t.manifest)
	if err != nil {
		return
	}
	var f struct {
		Entries []trashEntry `json:"entries"`
	}
	if json.Unmarshal(raw, &f) == nil {
		t.entries = f.Entries
	}
}

func (t *sessionTrash) saveLocked() error {
	if err := os.MkdirAll(t.root, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(map[string]any{"entries": t.entries}, "", "  ")
	if err != nil {
		return err
	}
	tmp := t.manifest + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, t.manifest)
}

// trashName 生成回收站内的文件名：保留原名，加时间戳与短哈希避免撞名。
func trashName(origPath string, at int64) string {
	sum := sha256.Sum256([]byte(origPath))
	return fmt.Sprintf("%d-%s-%s", at, hex.EncodeToString(sum[:4]), filepath.Base(origPath))
}

// Trash 把会话文件搬进回收站并记 manifest。
func (t *sessionTrash) Trash(s sessions.Session) (trashEntry, error) {
	at := time.Now().Unix()
	name := trashName(s.Path, at)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.loadLocked()
	if err := os.MkdirAll(t.root, 0o755); err != nil {
		return trashEntry{}, err
	}
	dst := filepath.Join(t.root, name)
	if err := os.Rename(s.Path, dst); err != nil {
		// 跨卷时 Rename 会失败（data 目录与家目录常常不同盘）：复制后删除。
		if cerr := copyFile(s.Path, dst); cerr != nil {
			return trashEntry{}, fmt.Errorf("移入回收站失败: %w", cerr)
		}
		if rerr := os.Remove(s.Path); rerr != nil {
			_ = os.Remove(dst)
			return trashEntry{}, fmt.Errorf("移入回收站后删除原文件失败: %w", rerr)
		}
	}
	entry := trashEntry{
		ID: s.ID, Agent: s.Agent, Title: s.Title,
		OrigPath: s.Path, TrashPath: dst, Size: s.Size, DeletedAt: at,
	}
	t.entries = append(t.entries, entry)
	if err := t.saveLocked(); err != nil {
		return entry, fmt.Errorf("写回收站清单失败: %w", err)
	}
	return entry, nil
}

// Restore 把回收站里的文件搬回原位。原路径已存在时拒绝覆盖。
func (t *sessionTrash) Restore(id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.loadLocked()
	idx := -1
	for i, e := range t.entries {
		if e.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("回收站里没有这个会话: %s", id)
	}
	e := t.entries[idx]
	if _, err := os.Stat(e.OrigPath); err == nil {
		return fmt.Errorf("原位置已存在同名文件，未覆盖: %s", e.OrigPath)
	}
	if err := os.MkdirAll(filepath.Dir(e.OrigPath), 0o755); err != nil {
		return err
	}
	if err := os.Rename(e.TrashPath, e.OrigPath); err != nil {
		if cerr := copyFile(e.TrashPath, e.OrigPath); cerr != nil {
			return fmt.Errorf("恢复失败: %w", cerr)
		}
		_ = os.Remove(e.TrashPath)
	}
	t.entries = append(t.entries[:idx], t.entries[idx+1:]...)
	return t.saveLocked()
}

// Purge 彻底删除：id 为空表示清空整个回收站。
func (t *sessionTrash) Purge(id string) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.loadLocked()
	kept := make([]trashEntry, 0, len(t.entries))
	removed := 0
	for _, e := range t.entries {
		if id != "" && e.ID != id {
			kept = append(kept, e)
			continue
		}
		_ = os.Remove(e.TrashPath)
		removed++
	}
	t.entries = kept
	return removed, t.saveLocked()
}

// List 返回回收站条目（新的在前）。
func (t *sessionTrash) List() []trashEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.loadLocked()
	out := append([]trashEntry(nil), t.entries...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].DeletedAt > out[j].DeletedAt })
	return out
}

func (t *sessionTrash) has(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.loadLocked()
	for _, e := range t.entries {
		if e.ID == id {
			return true
		}
	}
	return false
}

// copyFile 复制文件（跨卷移动时的兜底）。
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

/* ---------------- HTTP ---------------- */

// sessionTrashStore 返回回收站，必要时创建。
func (g *Gateway) sessionTrashStore() *sessionTrash {
	g.trashOnce.Do(func() {
		dir := filepath.Dir(g.cfg.SettingsPath)
		if dir == "" || dir == "." {
			dir = "."
		}
		g.trash = newSessionTrash(dir)
	})
	return g.trash
}

// handleSessionsDelete POST /api/sessions/delete {ids:[...]}
func (g *Gateway) handleSessionsDelete(w http.ResponseWriter, r *http.Request) {
	ids, ok := decodeSessionIDs(w, r)
	if !ok {
		return
	}
	byID := map[string]sessions.Session{}
	for _, s := range sessions.Scan() {
		byID[s.ID] = s
	}
	trash := g.sessionTrashStore()
	deleted := make([]string, 0, len(ids))
	problems := []map[string]any{}
	for _, id := range ids {
		s, ok := byID[id]
		if !ok {
			problems = append(problems, map[string]any{"id": id, "error": "会话不存在"})
			continue
		}
		if s.WSL != "" {
			// 跨发行版搬运风险高：明确不支持，而不是"删了但恢复不回来"。
			problems = append(problems, map[string]any{"id": id, "error": "WSL 里的会话请在对应发行版里管理"})
			continue
		}
		if strings.TrimSpace(s.Path) == "" {
			problems = append(problems, map[string]any{"id": id, "error": "该会话没有可删除的文件"})
			continue
		}
		if _, err := trash.Trash(s); err != nil {
			problems = append(problems, map[string]any{"id": id, "error": err.Error()})
			continue
		}
		deleted = append(deleted, id)
	}
	out := map[string]any{"deleted": deleted, "deletable": true}
	if len(problems) > 0 {
		out["problems"] = problems
	}
	g.writeSessionsView(w, out)
}

// handleSessionsRestore POST /api/sessions/restore {ids:[...]}
func (g *Gateway) handleSessionsRestore(w http.ResponseWriter, r *http.Request) {
	ids, ok := decodeSessionIDs(w, r)
	if !ok {
		return
	}
	trash := g.sessionTrashStore()
	restored := make([]string, 0, len(ids))
	problems := []map[string]any{}
	for _, id := range ids {
		if err := trash.Restore(id); err != nil {
			problems = append(problems, map[string]any{"id": id, "error": err.Error()})
			continue
		}
		restored = append(restored, id)
	}
	out := map[string]any{"restored": restored}
	if len(problems) > 0 {
		out["problems"] = problems
	}
	g.writeSessionsView(w, out)
}

// handleSessionsPurge POST /api/sessions/purge {ids:[...]} 或 {all:true}
func (g *Gateway) handleSessionsPurge(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
		All bool     `json:"all"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	trash := g.sessionTrashStore()
	total := 0
	if body.All {
		n, err := trash.Purge("")
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		total = n
	} else {
		for _, id := range body.IDs {
			n, err := trash.Purge(id)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			total += n
		}
	}
	g.writeSessionsView(w, map[string]any{"purged": total})
}

// handleSessionsTrash GET /api/sessions/trash
func (g *Gateway) handleSessionsTrash(w http.ResponseWriter, r *http.Request) {
	entries := g.sessionTrashStore().List()
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"id": e.ID, "agent": e.Agent, "name": e.Title, "title": e.Title,
			"path": e.OrigPath, "size": e.Size, "deleted_at": e.DeletedAt,
			"deletable": true, "restorable": true,
		})
	}
	g.writeSessionsView(w, map[string]any{"trash": out})
}

// decodeSessionIDs 解析 {"ids":[...]}。
func decodeSessionIDs(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return nil, false
	}
	ids := make([]string, 0, len(body.IDs))
	for _, id := range body.IDs {
		if v := strings.TrimSpace(id); v != "" {
			ids = append(ids, v)
		}
	}
	if len(ids) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "ids 不能为空"})
		return nil, false
	}
	return ids, true
}

// writeSessionsView 把结果合并进会话页视图（前端拿到就重绘，不用二次请求）。
func (g *Gateway) writeSessionsView(w http.ResponseWriter, extra map[string]any) {
	out := g.buildSessionsManage("")
	for k, v := range extra {
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}
