package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSyncUndoRestoresPreRestoreState(t *testing.T) {
	dir := t.TempDir()
	g := New(Config{SettingsPath: filepath.Join(dir, "settings.json"), UsagePath: "-"})

	// 恢复前本机状态。
	g.settingsMu.Lock()
	if g.settings.UI == nil {
		g.settings.UI = map[string]any{}
	}
	g.settings.UI["theme"] = "dark"
	_ = g.saveSettings()
	g.settingsMu.Unlock()

	if err := g.writeUndoSnapshot(); err != nil {
		t.Fatalf("快照写入失败: %v", err)
	}
	if !g.hasUndoSnapshot() {
		t.Fatal("应存在撤销副本")
	}
	if fi, err := os.Stat(g.undoSnapshotPath()); err != nil {
		t.Fatalf("撤销副本不存在: %v", err)
	} else if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("撤销副本权限不对: %v", fi.Mode().Perm())
	}

	// 模拟"从服务器恢复"把本机改掉。
	g.settingsMu.Lock()
	g.settings.UI["theme"] = "light"
	_ = g.saveSettings()
	g.settingsMu.Unlock()

	code, out := redactPost(t, g.handleDavSyncUndo, `{}`)
	if code != http.StatusOK {
		t.Fatalf("undo = %d %v", code, out)
	}
	if g.settingsView()["theme"] != "dark" {
		t.Fatalf("撤销后 theme = %v", g.settingsView()["theme"])
	}
	if g.hasUndoSnapshot() {
		t.Fatal("撤销后副本应删除")
	}
	if _, ok := out["undoed"]; !ok {
		t.Fatalf("响应应带 undoed: %v", out)
	}

	// 没有副本时如实报错，不是静默成功。
	code, out = redactPost(t, g.handleDavSyncUndo, `{}`)
	if code != http.StatusOK || out["error"] == nil {
		t.Fatalf("无副本时应返回错误: %d %v", code, out)
	}
}

func TestSyncDismissRevealAndUpload(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	g := New(Config{SettingsPath: filepath.Join(dir, "settings.json"), UsagePath: "-"})

	code, out := redactPost(t, g.handleDavSyncDismiss, `{}`)
	if code != http.StatusOK || out["on"] != false {
		t.Fatalf("dismiss = %d %v", code, out)
	}

	var opened string
	old := openConfigDir
	openConfigDir = func(d string) error { opened = d; return nil }
	t.Cleanup(func() { openConfigDir = old })
	code, out = redactPost(t, g.handleDavSyncReveal, `{}`)
	if code != http.StatusOK || opened != dir {
		t.Fatalf("reveal = %d opened=%q", code, opened)
	}

	// 没配同步目标时，upload 应把错误带回来（HTTP 200 + error 字段）。
	code, out = redactPost(t, g.handleDavSyncUpload, `{}`)
	if code != http.StatusOK || out["error"] == nil {
		t.Fatalf("upload 无目标应报错: %d %v", code, out)
	}
}

func TestRestoreViewCarriesUndoFlag(t *testing.T) {
	// 只验证视图字段：有副本时 davsyncView 之外，restore 响应会带 undo=true。
	dir := t.TempDir()
	g := New(Config{SettingsPath: filepath.Join(dir, "settings.json"), UsagePath: "-"})
	if err := g.writeUndoSnapshot(); err != nil {
		t.Fatal(err)
	}
	if !g.hasUndoSnapshot() {
		t.Fatal("应有副本")
	}
	rec := httptest.NewRecorder()
	g.handleDavSyncDismiss(rec, httptest.NewRequest(http.MethodPost, "/api/davsync/dismiss", nil))
	var view map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("dismiss 非 JSON: %v", err)
	}
}
