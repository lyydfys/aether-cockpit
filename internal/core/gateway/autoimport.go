package gateway

// 本机账号自动导入（对齐 local-vault 的 Auto Local Import）。
//
// 与「从其他应用导入」的区别：那个是用户点一次、扫一次；这里是后台按固定
// 间隔扫描本机已登录的客户端，把新出现的身份自动入库。必须解决三件事：
//  1. 去重：同一账号换 token 后不能反复导入；
//  2. 墓碑：用户删掉的账号不能下一轮又复活；
//  3. 不打扰：默认关闭，开启后才扫描；扫描结果只暴露数量与标签。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/provider"
)

const (
	autoImportSettingKey = "auto_import_local"
	autoImportInterval   = 60 * time.Second
	autoImportFirstDelay = 15 * time.Second
	autoImportScanLimit  = 25 * time.Second
	autoImportSeenMax    = 5000
)

type autoImportRuntime struct {
	mu        sync.Mutex
	seen      map[string]int64
	path      string
	running   bool
	lastScan  int64
	lastAdded []string
	lastError string
	scan      func(context.Context) map[string][]*provider.CredentialDraft
}

type autoImportStatus struct {
	Enabled     bool     `json:"enabled"`
	Running     bool     `json:"running"`
	IntervalSec int      `json:"interval_sec"`
	Seen        int      `json:"seen"`
	LastScan    int64    `json:"last_scan,omitempty"`
	LastAdded   []string `json:"last_added,omitempty"`
	LastError   string   `json:"last_error,omitempty"`
}

func (g *Gateway) initAutoImport() {
	g.autoImp.path = filepath.Join(filepath.Dir(g.settingsPath), "autoimport.json")
	g.autoImp.scan = func(ctx context.Context) map[string][]*provider.CredentialDraft {
		return g.scanLocalDrafts(ctx, autoImportScanLimit)
	}
	g.loadAutoImport()
	g.goBackground(g.autoImportLoop)
}

func (g *Gateway) autoImportLoop() {
	timer := time.NewTimer(autoImportFirstDelay)
	defer timer.Stop()
	select {
	case <-g.ctx.Done():
		return
	case <-timer.C:
	}
	t := time.NewTicker(autoImportInterval)
	defer t.Stop()
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-t.C:
		}
		if g.autoImportEnabled() {
			g.scanAutoImport(g.ctx)
		}
	}
}

func (g *Gateway) autoImportEnabled() bool {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.UI == nil {
		return false
	}
	switch v := g.settings.UI[autoImportSettingKey].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true") || v == "1" || v == "on"
	}
	return false
}

func (g *Gateway) setAutoImportEnabled(on bool) error {
	g.settingsMu.Lock()
	if g.settings.UI == nil {
		g.settings.UI = map[string]any{}
	}
	g.settings.UI[autoImportSettingKey] = on
	err := g.saveSettings()
	g.settingsMu.Unlock()
	return err
}

func (g *Gateway) loadAutoImport() {
	g.autoImp.mu.Lock()
	defer g.autoImp.mu.Unlock()
	g.autoImp.seen = map[string]int64{}
	raw, err := os.ReadFile(g.autoImp.path)
	if err != nil {
		return
	}
	var file struct {
		Seen      map[string]int64 `json:"seen"`
		LastScan  int64            `json:"last_scan,omitempty"`
		LastAdded []string         `json:"last_added,omitempty"`
		LastError string           `json:"last_error,omitempty"`
	}
	if json.Unmarshal(raw, &file) != nil {
		return
	}
	if file.Seen != nil {
		g.autoImp.seen = file.Seen
	}
	g.autoImp.lastScan = file.LastScan
	g.autoImp.lastAdded = file.LastAdded
	g.autoImp.lastError = file.LastError
}

// saveAutoImportLocked 原子落盘；调用方持有 autoImp.mu。
func (g *Gateway) saveAutoImportLocked() {
	if g.autoImp.path == "" {
		return
	}
	file := map[string]any{
		"seen":       g.autoImp.seen,
		"last_scan":  g.autoImp.lastScan,
		"last_added": g.autoImp.lastAdded,
		"last_error": g.autoImp.lastError,
	}
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return
	}
	dir := filepath.Dir(g.autoImp.path)
	if dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	tmp := g.autoImp.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, g.autoImp.path)
	}
}

func (g *Gateway) autoImportStatus() autoImportStatus {
	g.autoImp.mu.Lock()
	seen := len(g.autoImp.seen)
	running := g.autoImp.running
	lastScan := g.autoImp.lastScan
	lastAdded := append([]string(nil), g.autoImp.lastAdded...)
	lastErr := g.autoImp.lastError
	g.autoImp.mu.Unlock()
	return autoImportStatus{
		Enabled:     g.autoImportEnabled(),
		Running:     running,
		IntervalSec: int(autoImportInterval / time.Second),
		Seen:        seen,
		LastScan:    lastScan,
		LastAdded:   lastAdded,
		LastError:   lastErr,
	}
}

func (g *Gateway) markAutoImportSeen(id string) {
	if id == "" {
		return
	}
	g.autoImp.mu.Lock()
	if g.autoImp.seen == nil {
		g.autoImp.seen = map[string]int64{}
	}
	g.autoImp.seen[id] = time.Now().Unix()
	g.pruneAutoImportSeenLocked()
	g.saveAutoImportLocked()
	g.autoImp.mu.Unlock()
}

func (g *Gateway) autoImportSeen(id string) bool {
	g.autoImp.mu.Lock()
	defer g.autoImp.mu.Unlock()
	_, ok := g.autoImp.seen[id]
	return ok
}

// pruneAutoImportSeenLocked 只保留最近的一批身份，避免长期运行无限增长。
func (g *Gateway) pruneAutoImportSeenLocked() {
	if len(g.autoImp.seen) <= autoImportSeenMax {
		return
	}
	type pair struct {
		k string
		t int64
	}
	list := make([]pair, 0, len(g.autoImp.seen))
	for k, t := range g.autoImp.seen {
		list = append(list, pair{k, t})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].t < list[j].t })
	for i := 0; i < len(list)-autoImportSeenMax; i++ {
		delete(g.autoImp.seen, list[i].k)
	}
}

// scanAutoImport 扫描一轮并导入新身份。并发调用时后来的调用直接返回当前状态。
func (g *Gateway) scanAutoImport(ctx context.Context) autoImportStatus {
	g.autoImp.mu.Lock()
	if g.autoImp.running {
		g.autoImp.mu.Unlock()
		return g.autoImportStatus()
	}
	g.autoImp.running = true
	g.autoImp.mu.Unlock()

	added, firstErr := 0, ""
	g.autoImp.mu.Lock()
	scan := g.autoImp.scan
	g.autoImp.mu.Unlock()
	if scan == nil {
		scan = func(c context.Context) map[string][]*provider.CredentialDraft {
			return g.scanLocalDrafts(c, autoImportScanLimit)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, autoImportScanLimit)
	defer cancel()
	found := scan(ctx)

	existing := map[string]bool{}
	for _, env := range g.pool.All() {
		existing[envLocalIdentity(env)] = true
	}

	var names []string
	for providerID, drafts := range found {
		p, ok := provider.Get(providerID)
		if !ok {
			continue
		}
		display := p.Manifest().DisplayName
		for _, d := range drafts {
			if d == nil || len(d.Credentials) == 0 {
				continue
			}
			id := localDraftIdentity(providerID, d)
			if id == "" || g.autoImportSeen(id) {
				continue
			}
			if existing[id] {
				g.markAutoImportSeen(id)
				continue
			}
			label := g.uniqueLocalLabel(providerID, d.Label, display)
			env, apiErr := g.createAccount(providerID, label, d.Credentials, "")
			if apiErr != nil {
				if firstErr == "" {
					firstErr = providerID + ": " + apiErr.Message
				}
				continue
			}
			added++
			names = append(names, label)
			g.markAutoImportSeen(id)
			existing[id] = true
			if g.providerQuotaReadable(providerID) {
				g.ensureQuotaRead(env)
			}
			eventbus.Emit(eventbus.TopicAccount+".imported", map[string]any{
				"provider_id": providerID, "count": 1, "auto": true, "label": label,
			})
		}
	}
	if added > 0 {
		g.saveAccountsNow()
	}
	sort.Strings(names)
	if len(names) > 20 {
		names = names[:20]
	}
	g.autoImp.mu.Lock()
	g.autoImp.running = false
	g.autoImp.lastScan = time.Now().Unix()
	g.autoImp.lastAdded = names
	g.autoImp.lastError = firstErr
	g.saveAutoImportLocked()
	g.autoImp.mu.Unlock()
	return g.autoImportStatus()
}

// localDraftIdentity 把本机登录态折成稳定身份：优先用户/账号字段，
// 没有再退到长期密钥。token 轮换不会改变身份。
func localDraftIdentity(providerID string, d *provider.CredentialDraft) string {
	if d == nil {
		return ""
	}
	return localIdentity(providerID, d.Label, func(k string) string { return d.Credentials[k] })
}

func envLocalIdentity(env *provider.AccountEnv) string {
	if env == nil {
		return ""
	}
	return localIdentity(env.ProviderID, env.Label, env.GetCredential)
}

func localIdentity(providerID, label string, get func(string) string) string {
	clean := func(k string) string { return strings.TrimSpace(get(k)) }
	for _, k := range []string{"user_id", "account_id", "email", "chatgpt_account_id", "tenant_id", "uid"} {
		if v := clean(k); v != "" {
			return shortHash(providerID + "|id|" + v)
		}
	}
	for _, k := range []string{"refresh_token", "access_token", "token", "api_key", "session_key", "auth1_token", "cookie", "credentials"} {
		if v := clean(k); v != "" {
			return shortHash(providerID + "|key|" + v)
		}
	}
	if label = strings.ToLower(strings.TrimSpace(label)); label != "" {
		return shortHash(providerID + "|label|" + label)
	}
	return ""
}

func (g *Gateway) uniqueLocalLabel(providerID, base, fallback string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = strings.TrimSpace(fallback)
	}
	if base == "" {
		base = providerID
	}
	if utf8.RuneCountInString(base) > 60 {
		base = string([]rune(base)[:60])
	}
	used := map[string]bool{}
	for _, e := range g.pool.ListByProvider(providerID) {
		used[e.Label] = true
	}
	if !used[base] {
		return base
	}
	for i := 2; i < 1000; i++ {
		cand := fmt.Sprintf("%s #%d", base, i)
		if !used[cand] {
			return cand
		}
	}
	return fmt.Sprintf("%s %s", base, shortHash(time.Now().String()))
}

// --- HTTP API ---

func (g *Gateway) handleAutoImportStatus(w http.ResponseWriter, r *http.Request) {
	if !g.platform.WindowsAutoImport {
		writePlatformUnsupported(w, "本机客户端自动导入")
		return
	}
	writeJSON(w, http.StatusOK, g.autoImportStatus())
}

func (g *Gateway) handleAutoImportToggle(w http.ResponseWriter, r *http.Request) {
	if !g.platform.WindowsAutoImport {
		writePlatformUnsupported(w, "本机客户端自动导入")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	if err := g.setAutoImportEnabled(body.Enabled); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存设置失败: " + err.Error()})
		return
	}
	if body.Enabled {
		g.goBackground(func() { g.scanAutoImport(g.ctx) })
	}
	writeJSON(w, http.StatusOK, g.autoImportStatus())
}

func (g *Gateway) handleAutoImportScan(w http.ResponseWriter, r *http.Request) {
	if !g.platform.WindowsAutoImport {
		writePlatformUnsupported(w, "本机客户端自动导入")
		return
	}
	status := g.scanAutoImport(r.Context())
	writeJSON(w, http.StatusOK, status)
}

func (g *Gateway) handleAutoImportReset(w http.ResponseWriter, r *http.Request) {
	if !g.platform.WindowsAutoImport {
		writePlatformUnsupported(w, "本机客户端自动导入")
		return
	}
	g.autoImp.mu.Lock()
	g.autoImp.seen = map[string]int64{}
	g.autoImp.lastAdded = nil
	g.autoImp.lastError = ""
	g.saveAutoImportLocked()
	g.autoImp.mu.Unlock()
	writeJSON(w, http.StatusOK, g.autoImportStatus())
}
