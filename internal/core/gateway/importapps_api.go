package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"aether-cockpit/internal/provider"
)

// 「从其他应用导入」——把本机已有登录态做成 aether 的 importapps 对话框。
//
// 数据来自各渠道自己的 LocalImportCapability（Codex / OpenCode / Antigravity /
// Qoder / Trae / Claude …），只读本机凭据文件；导入时重新扫描并把凭证直接
// 写进内核账号池，对话框与浏览器始终看不到明文。

// localDraft 是扫描到的一条可导入登录态（不含凭证）。
type localDraft struct {
	ProviderID     string
	ProviderName   string
	Index          int
	Label          string
	Source         string
	CredentialKeys []string
}

// scanLocalDrafts 并行扫描所有支持本机导入的渠道，返回完整凭据草稿。
//
// 只允许内核内部使用：结果含明文凭据，HTTP 层只暴露 discoverLocalAll
// 生成的摘要（标签/来源/字段名）。
func (g *Gateway) scanLocalDrafts(parent context.Context, timeout time.Duration) map[string][]*provider.CredentialDraft {
	// 移动端不做跨 App / PC 客户端扫描；调用方（自动导入与发现接口）
	// 都应在能力层被拒绝，这里再兜底一次，确保没有任何路径会触发扫描。
	if !g.platform.WindowsAutoImport {
		return map[string][]*provider.CredentialDraft{}
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	out := make(map[string][]*provider.CredentialDraft, 4)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, m := range provider.AllManifests() {
		p, ok := provider.Get(m.ID)
		if !ok {
			continue
		}
		cap, ok := p.(provider.LocalImportCapability)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(id string, cap provider.LocalImportCapability) {
			defer wg.Done()
			found, err := cap.DiscoverLocalAccounts(ctx)
			if err != nil || len(found) == 0 {
				return
			}
			mu.Lock()
			out[id] = found
			mu.Unlock()
		}(m.ID, cap)
	}
	wg.Wait()
	return out
}

// discoverLocalAll 并行扫描所有支持本机导入的渠道，总上限 8 秒。
func (g *Gateway) discoverLocalAll(parent context.Context) map[string][]localDraft {
	found := g.scanLocalDrafts(parent, 8*time.Second)
	out := make(map[string][]localDraft, len(found))
	for id, drafts := range found {
		m, ok := provider.Get(id)
		if !ok {
			continue
		}
		name := m.Manifest().DisplayName
		list := make([]localDraft, 0, len(drafts))
		for i, d := range drafts {
			if d == nil {
				continue
			}
			keys := make([]string, 0, len(d.Credentials))
			for k := range d.Credentials {
				keys = append(keys, k)
			}
			list = append(list, localDraft{
				ProviderID: id, ProviderName: name, Index: i,
				Label: d.Label, Source: d.Source, CredentialKeys: keys,
			})
		}
		out[id] = list
	}
	return out
}

func (g *Gateway) localFingerprint(providerID, label string) string {
	return shortHash(providerID + "|" + label)
}

// handleImportAppsDiscovery GET /api/importapps/discovery
//
// 渠道页顶部的"发现本机配置"提示：只给指纹与来源，不含任何凭证。
func (g *Gateway) handleImportAppsDiscovery(w http.ResponseWriter, r *http.Request) {
	if !g.platform.WindowsAutoImport {
		writePlatformUnsupported(w, "本机客户端扫描")
		return
	}
	found := g.discoverLocalAll(r.Context())
	out := make([]map[string]any, 0, 8)
	for id, drafts := range found {
		for _, d := range drafts {
			out = append(out, map[string]any{
				"fingerprint": g.localFingerprint(id, d.Label),
				"source":      d.ProviderName,
				"name":        d.Label,
			})
		}
	}
	for _, e := range g.localVaultEntries() {
		out = append(out, map[string]any{
			"fingerprint": e.Fingerprint,
			"source":      "本机加密凭据库",
			"name":        e.Label,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleImportApps GET /api/importapps
func (g *Gateway) handleImportApps(w http.ResponseWriter, r *http.Request) {
	if !g.platform.WindowsAutoImport {
		writePlatformUnsupported(w, "本机客户端扫描")
		return
	}
	found := g.discoverLocalAll(r.Context())
	// 本机配置库 的配置是"另一个应用里的账号"，与本机登录态一起列出；
	// 数据库不存在时静默跳过。
	ccEntries, _ := readLocalSQLiteEntries()
	localVaultEntries := g.localVaultEntries()
	sources := make([]map[string]any, 0, 8)
	for _, m := range provider.AllManifests() {
		p, ok := provider.Get(m.ID)
		if !ok {
			continue
		}
		_, localOK := p.(provider.LocalImportCapability)
		drafts := found[m.ID]
		var platformCockpit []localVaultEntry
		for _, e := range localVaultEntries {
			if e.ProviderID == m.ID {
				platformCockpit = append(platformCockpit, e)
			}
		}
		if !localOK && len(drafts) == 0 && len(platformCockpit) == 0 {
			continue
		}
		items := make([]map[string]any, 0, len(drafts)+len(platformCockpit))
		for _, d := range drafts {
			skip := ""
			for _, e := range g.pool.ListByProvider(m.ID) {
				if e.Label == d.Label {
					skip = "已导入过这个账号"
					break
				}
			}
			items = append(items, map[string]any{
				"ref":         strconv.Itoa(d.Index),
				"fingerprint": g.localFingerprint(m.ID, d.Label),
				"status":      "new",
				"from":        d.Label,
				"skip":        skip,
				"provider": map[string]any{
					"name":   d.Label,
					"chat":   "",
					"key":    "本机登录态",
					"models": []string{},
				},
			})
		}
		for _, e := range ccEntries {
			if e.Target != m.ID {
				continue
			}
			skip := e.Skip
			if skip == "" {
				for _, acc := range g.pool.ListByProvider(m.ID) {
					if acc.Label == e.Label {
						skip = "已导入过这个账号"
						break
					}
				}
			}
			items = append(items, map[string]any{
				"ref":         e.Ref(),
				"fingerprint": g.localFingerprint(m.ID, e.Label),
				"status":      "new",
				"from":        e.Label,
				"skip":        skip,
				"provider": map[string]any{
					"name":   e.Label,
					"chat":   "",
					"key":    "本机 SQLite 配置",
					"models": []string{},
				},
			})
		}
		existingIDs := map[string]bool{}
		for _, acc := range g.pool.ListByProvider(m.ID) {
			existingIDs[envLocalIdentity(acc)] = true
		}
		for _, e := range platformCockpit {
			skip := ""
			if existingIDs[e.Fingerprint] {
				skip = "已导入过这个账号"
			}
			items = append(items, map[string]any{
				"ref":         e.Ref,
				"fingerprint": e.Fingerprint,
				"status":      "new",
				"from":        e.Label,
				"skip":        skip,
				"provider": map[string]any{
					"name":   e.Label,
					"chat":   "",
					"key":    "本机加密凭据库",
					"models": []string{},
				},
			})
		}
		path := ""
		if len(drafts) > 0 {
			path = drafts[0].Source
		}
		if path == "" && len(platformCockpit) > 0 {
			path = platformCockpit[0].SourcePath
		}
		if path == "" && len(items) > 0 {
			path = localSQLiteDBPath()
		}
		sources = append(sources, map[string]any{
			"id": m.ID, "name": m.DisplayName, "found": len(items) > 0,
			"path": path, "items": items,
		})
	}
	writeJSON(w, http.StatusOK, sources)
}

// handleImportAppsSubmit POST /api/importapps
//
//	{"picks":[{"source":"codex","ref":"0","mode":"add","name":"可选的新名称"}]}
func (g *Gateway) handleImportAppsSubmit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Picks []struct {
			Source string `json:"source"`
			Ref    string `json:"ref"`
			Mode   string `json:"mode"`
			Name   string `json:"name"`
		} `json:"picks"`
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "读取请求体失败: " + err.Error()})
		return
	}
	// 非 UTF-8 的请求体会被 json 解码成 U+FFFD 并**静默**写进账号标签（中文
	// 标签在非 UTF-8 终端里最容易踩：我曾把一个中文名存成"����"）。先拒掉。
	if !utf8.Valid(raw) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "请求体不是合法的 UTF-8（终端可能是 GBK）：请以 UTF-8 发送，或省略 name 使用扫描到的名字",
		})
		return
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	added := make([]string, 0, len(body.Picks))
	found := g.discoverLocalAll(r.Context())
	ccEntries, _ := readLocalSQLiteEntries()
	localVaultEntries := g.localVaultEntries()
	for _, pick := range body.Picks {
		if strings.HasPrefix(pick.Ref, "localsqlite:") {
			id := strings.TrimPrefix(pick.Ref, "localsqlite:")
			for _, e := range ccEntries {
				if e.ID != id || e.Target != pick.Source || e.Skip != "" {
					continue
				}
				label := pick.Name
				if label == "" {
					label = e.Label
				}
				if _, apiErr := g.createAccount(pick.Source, label, e.credentials(), ""); apiErr != nil {
					continue
				}
				added = append(added, label)
				break
			}
			continue
		}
		if strings.HasPrefix(pick.Ref, "localvault:") {
			for _, e := range localVaultEntries {
				if e.Ref != pick.Ref || e.ProviderID != pick.Source {
					continue
				}
				label := pick.Name
				if label == "" {
					label = e.Label
				}
				if _, apiErr := g.createAccount(e.ProviderID, label, e.credentials, ""); apiErr == nil {
					added = append(added, label)
				}
				break
			}
			continue
		}
		drafts := found[pick.Source]
		if len(drafts) == 0 {
			continue
		}
		idx, err := strconv.Atoi(pick.Ref)
		if err != nil || idx < 0 || idx >= len(drafts) {
			continue
		}
		// 重新扫描拿凭证明文（discovery 结果里刻意不含凭证）。
		p, ok := provider.Get(pick.Source)
		if !ok {
			continue
		}
		cap, ok := p.(provider.LocalImportCapability)
		if !ok {
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		list, err := cap.DiscoverLocalAccounts(ctx)
		cancel()
		if err != nil || idx >= len(list) {
			continue
		}
		label := pick.Name
		if label == "" {
			label = list[idx].Label
		}
		if _, apiErr := g.createAccount(pick.Source, label, list[idx].Credentials, ""); apiErr != nil {
			continue
		}
		added = append(added, label)
	}
	g.saveAccountsNow()

	rec := &discardResponse{header: http.Header{}}
	g.handleProviders(rec, r)
	var state any
	_ = json.Unmarshal(rec.body, &state)
	writeJSON(w, http.StatusOK, map[string]any{"state": state, "added": added})
}
