package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/sessions"
)

// 迁移类写入仍是只读（501）：跨机器搬会话记录不在网关职责内。
func TestSessionsRelocateStaysReadOnly(t *testing.T) {
	g := New(Config{SettingsPath: t.TempDir() + "/settings.json", UsagePath: "-"})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions/relocate-claude", g.handleSessionsWriteReadOnly)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/sessions/relocate-claude", strings.NewReader(`{}`)))
	if rec.Code != http.StatusNotImplemented || !strings.Contains(rec.Body.String(), "只读") {
		t.Fatalf("relocate = %d %s", rec.Code, rec.Body.String())
	}
}

// 删除 → 回收站 → 恢复 是一条闭环：删掉的文件必须能原样捡回来。
func TestSessionTrashDeleteAndRestore(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(src, []byte(`{"m":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	trash := newSessionTrash(filepath.Join(dir, "data"))

	if _, err := trash.Trash(sessions.Session{ID: "s1", Agent: "claude-code", Path: src, Size: 7}); err != nil {
		t.Fatalf("Trash: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("删除后原文件应不存在")
	}
	entries := trash.List()
	if len(entries) != 1 || entries[0].ID != "s1" {
		t.Fatalf("回收站应有 1 条：%+v", entries)
	}

	if err := trash.Restore("s1"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	raw, err := os.ReadFile(src)
	if err != nil || string(raw) != `{"m":1}` {
		t.Fatalf("恢复后内容不对：%q err=%v", raw, err)
	}
	if len(trash.List()) != 0 {
		t.Fatal("恢复后应清掉回收站条目")
	}
}

// 原位置已有同名文件时不覆盖（数据安全优先）。
func TestSessionTrashRestoreRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(src, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	trash := newSessionTrash(filepath.Join(dir, "data"))
	if _, err := trash.Trash(sessions.Session{ID: "s1", Agent: "codex", Path: src}); err != nil {
		t.Fatal(err)
	}
	// 用户在原位置又建了一个同名文件。
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := trash.Restore("s1"); err == nil {
		t.Fatal("原位置已存在时应拒绝恢复")
	}
	raw, _ := os.ReadFile(src)
	if string(raw) != "new" {
		t.Fatalf("不能覆盖用户新建的文件：%q", raw)
	}
}

// 彻底清除会删掉回收站里的副本；id 为空时清空整个回收站。
func TestSessionTrashPurge(t *testing.T) {
	dir := t.TempDir()
	trash := newSessionTrash(filepath.Join(dir, "data"))
	for _, id := range []string{"a", "b"} {
		src := filepath.Join(dir, id+".jsonl")
		if err := os.WriteFile(src, []byte(id), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := trash.Trash(sessions.Session{ID: id, Agent: "codex", Path: src}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := trash.Purge("a"); err != nil || n != 1 {
		t.Fatalf("Purge(a) = %d, %v", n, err)
	}
	if len(trash.List()) != 1 {
		t.Fatalf("应剩 1 条：%+v", trash.List())
	}
	if n, err := trash.Purge(""); err != nil || n != 1 {
		t.Fatalf("Purge(all) = %d, %v", n, err)
	}
	if len(trash.List()) != 0 {
		t.Fatal("清空后应为空")
	}
}

// manifest 要能跨进程恢复：重建一个 store 指向同目录也能读到。
func TestSessionTrashManifestPersists(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newSessionTrash(filepath.Join(dir, "data"))
	if _, err := a.Trash(sessions.Session{ID: "s1", Agent: "codex", Path: src}); err != nil {
		t.Fatal(err)
	}
	b := newSessionTrash(filepath.Join(dir, "data"))
	if got := b.List(); len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("新实例应读到清单：%+v", got)
	}
}
