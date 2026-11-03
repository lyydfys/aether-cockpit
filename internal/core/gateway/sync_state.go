package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// 同步的本地状态：底稿（上一次成功同步的内容）、part 摘要、跨进程锁。
//
// 为什么需要底稿：三路合并必须有"共同祖先"，否则分不清"谁改了什么"。
// 底稿只在本机，不参与上传；每次成功同步后覆盖。

// syncState 是落盘的同步状态。
type syncState struct {
	// Base 是上次成功同步时的备份明文（用于三路合并的 base）。
	Base json.RawMessage `json:"base,omitempty"`
	// BaseAt 是底稿的取得时间。
	BaseAt int64 `json:"base_at,omitempty"`
	// Parts 是上次成功同步时各 part 的摘要（用于跳过无变化上传）。
	Parts map[string]string `json:"parts,omitempty"`
	// ETag 是上次成功写入后远端返回的版本标识（WebDAV ETag / S3 ETag / GitHub sha）。
	ETag string `json:"etag,omitempty"`
	// Blind 表示"远端不支持条件写"：下次同步要重新合并一次再写。
	Blind bool `json:"blind,omitempty"`
	// LastConflicts 是最近一次合并保留本机值的冲突路径（页面展示用）。
	LastConflicts []string `json:"last_conflicts,omitempty"`
}

func (g *Gateway) syncStatePath() string {
	if g.cfg.SettingsPath != "" {
		return filepath.Join(filepath.Dir(g.cfg.SettingsPath), "sync-state.json")
	}
	return "sync-state.json"
}

// loadSyncState 读本机同步状态；不存在或损坏都按空处理（退化成首次同步）。
func (g *Gateway) loadSyncState() *syncState {
	st := &syncState{}
	raw, err := os.ReadFile(g.syncStatePath())
	if err != nil {
		return st
	}
	if json.Unmarshal(raw, st) != nil {
		return &syncState{}
	}
	return st
}

// saveSyncState 原子写回同步状态。
func (g *Gateway) saveSyncState(st *syncState) {
	if st == nil {
		return
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	path := g.syncStatePath()
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

/* ---------------- 跨进程锁 ---------------- */

// syncLockTTL 是锁的存活时间。持锁进程崩溃后靠它自动失效。
const syncLockTTL = 2 * time.Minute

// acquireSyncLock 尝试获取跨进程同步锁。
//
// 为什么需要：GUI 与 CLI（或两个实例）可能同时触发同步，各自算出的合并结果
// 会互相覆盖。锁文件里带 PID 与时间，进程死掉后靠 TTL 自动释放。
func (g *Gateway) acquireSyncLock() (release func(), err error) {
	path := g.syncStatePath() + ".lock"
	if raw, rerr := os.ReadFile(path); rerr == nil {
		holder, stamp := parseLockFile(string(raw))
		if time.Since(stamp) < syncLockTTL && lockAlive(holder) {
			return nil, fmt.Errorf("另一个进程正在同步（PID %d），请稍后再试", holder)
		}
	}
	content := fmt.Sprintf("%d\n%d\n", os.Getpid(), time.Now().Unix())
	if werr := os.WriteFile(path, []byte(content), 0o600); werr != nil {
		return nil, fmt.Errorf("无法创建同步锁: %w", werr)
	}
	return func() { _ = os.Remove(path) }, nil
}

func parseLockFile(raw string) (pid int, at time.Time) {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) > 0 {
		pid, _ = strconv.Atoi(strings.TrimSpace(lines[0]))
	}
	if len(lines) > 1 {
		if sec, err := strconv.ParseInt(strings.TrimSpace(lines[1]), 10, 64); err == nil {
			at = time.Unix(sec, 0)
		}
	}
	if at.IsZero() {
		at = time.Now() // 解析失败按"刚创建"处理，宁可多等一次
	}
	return pid, at
}

// lockAlive 判断持锁进程是否还在。
//
// 只做"同 PID 是否存活"的判断：跨平台进程存活检查各有差异，这里保守地
// 认为"进程号存在就在跑"；进程号被复用最多让我们多等一个 TTL。
func lockAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if pid == os.Getpid() {
		return true
	}
	_, err := os.FindProcess(pid)
	return err == nil
}

/* ---------------- 冲突留档 ---------------- */

// archiveSyncConflicts 把被舍弃的远端值写进 sync/conflicts-<时间>.json。
//
// 为什么留档而不只是报一句"有冲突"：用户需要能找回"另一台机器上的那个值"，
// 否则"保留本机"对他们来说仍然等于丢失。
func (g *Gateway) archiveSyncConflicts(list []mergeConflict) string {
	if len(list) == 0 {
		return ""
	}
	dir := filepath.Join(filepath.Dir(g.syncStatePath()), "sync")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	name := filepath.Join(dir, "conflicts-"+time.Now().Format("20060102-150405")+".json")
	payload := map[string]any{
		"at":        time.Now().Unix(),
		"note":      "云同步冲突：这些路径两边都改过，保留了本机值；remote 是被舍弃的远端值。",
		"conflicts": list,
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return ""
	}
	if os.WriteFile(name, raw, 0o600) != nil {
		return ""
	}
	return name
}

// errSyncConflict 表示条件写失败（远端在合并之后又被改过）。
var errSyncConflict = errors.New("远端内容在合并后发生变化")
