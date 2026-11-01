package gateway

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

// 同步恢复的"撤销"与本地副本。
//
// 从服务器恢复会覆盖本机设置；恢复前先把本机现状写成一份备份
// （与 push 用的同一份 bundle，不含凭证），撤消时再套回去。
// 文件只保留最后一次恢复前的一份，权限 0600，位置在配置目录。

func (g *Gateway) undoSnapshotPath() string {
	return filepath.Join(g.configDir(), "sync-undo.json")
}

// writeUndoSnapshot 原子写入恢复前快照。
func (g *Gateway) writeUndoSnapshot() error {
	bundle := g.buildBackupBundle()
	b, err := json.Marshal(&bundle)
	if err != nil {
		return err
	}
	dir := g.configDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := g.undoSnapshotPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, g.undoSnapshotPath())
}

// hasUndoSnapshot 报告是否有可撤销的恢复。
func (g *Gateway) hasUndoSnapshot() bool {
	_, err := os.Stat(g.undoSnapshotPath())
	return err == nil
}

// handleDavSyncUndo POST /api/davsync/undo
func (g *Gateway) handleDavSyncUndo(w http.ResponseWriter, r *http.Request) {
	path := g.undoSnapshotPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		view := g.davsyncView("没有可撤销的恢复")
		writeJSON(w, http.StatusOK, view)
		return
	}
	var bundle backupBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		view := g.davsyncView("撤销副本已损坏: " + err.Error())
		writeJSON(w, http.StatusOK, view)
		return
	}
	applied := g.applyBundle(&bundle)
	if err := os.Remove(path); err != nil {
		log.Printf("[WARN] 撤销副本删除失败: %v", err)
	}
	view := g.davsyncView("")
	view["undoed"] = applied
	view["notice"] = map[string]any{"restored": false, "here": []string{}}
	writeJSON(w, http.StatusOK, view)
}

// handleDavSyncDismiss POST /api/davsync/dismiss
// 恢复提示只是一次响应里的字段，没有留存状态；确认即回到普通视图。
func (g *Gateway) handleDavSyncDismiss(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.davsyncView(""))
}

// handleDavSyncUpload POST /api/davsync/upload
// 用本机的设置覆盖服务器上的文件（对方不是可读备份时的那条路）。
func (g *Gateway) handleDavSyncUpload(w http.ResponseWriter, r *http.Request) {
	size, remote, err := g.pushBackupToTarget()
	if err != nil {
		writeJSON(w, http.StatusOK, g.davsyncView(err.Error()))
		return
	}
	view := g.davsyncView("")
	view["uploaded"] = true
	view["bytes"] = size
	view["remote"] = remote
	writeJSON(w, http.StatusOK, view)
}

// handleDavSyncReveal POST /api/davsync/reveal
// 打开配置目录（撤销副本与本机备份都在这里）。
func (g *Gateway) handleDavSyncReveal(w http.ResponseWriter, r *http.Request) {
	dir := g.configDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "无法创建配置目录: " + err.Error()})
		return
	}
	if err := openConfigDir(dir); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "dir": dir})
}
