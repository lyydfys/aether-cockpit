package gateway

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"aether-cockpit/internal/core/library"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

// 配置备份导出/导入 —— 对齐 aether 的"分享与同步"能力（本机文件版）。
//
// 明确不导出账号凭证：账号凭证是 AES 本机绑定加密的，离开本机无法解密；
// 把它们写进一个迟早会被分享的文件只会制造虚假的安全感。
// 备份覆盖的是"重装/迁移时要花时间重建的部分"：
// 运行时设置、虚拟组定义、渠道 spec、账号标签。

// backupBundle 是导出文件的形态。
type backupBundle struct {
	Version    int                 `json:"version"`
	ExportedAt int64               `json:"exported_at"`
	Note       string              `json:"note"`
	Settings   *runtimeSettings    `json:"settings,omitempty"`
	Specs      []*spec.Spec        `json:"specs,omitempty"`
	Accounts   []backupAccountMeta `json:"accounts_meta,omitempty"`
	// Library 是资源库快照（prompts / MCP / skills / 子代理）。
	Library *library.Snapshot `json:"library,omitempty"`
	// Icons 是自定义供应商图标：文件名 → base64 内容。
	// 设置里只存 file:<name> 引用，不带文件的话换机恢复会全部退回通用图标。
	Icons map[string]string `json:"icons,omitempty"`
}

type backupAccountMeta struct {
	AccountID  string   `json:"account_id"`
	ProviderID string   `json:"provider_id"`
	Label      string   `json:"label,omitempty"`
	Tags       []string `json:"tags,omitempty"`
}

// handleBackupExport GET /api/backup/export —— 下载备份 JSON。
func (g *Gateway) handleBackupExport(w http.ResponseWriter, r *http.Request) {
	bundle := g.buildBackupBundle()
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=aether-backup-%s.json", time.Now().Format("20060102-150405")))
	writeJSON(w, http.StatusOK, bundle)
}

// buildBackupBundle 生成配置备份（本机导出与 WebDAV push 共用）。
// WebDAV 同步配置本身不进备份：密码是明文存的，写进一个迟早会被
// 上传到那台服务器的文件只会制造泄露面，且导入侧也不应用它。
func (g *Gateway) buildBackupBundle() backupBundle {
	bundle := backupBundle{
		Version:    1,
		ExportedAt: time.Now().Unix(),
		Note:       "Aether Cockpit 配置备份：设置/虚拟组/spec/账号标签。不含账号凭证与代理密码（本机绑定加密，无法跨机还原）。",
	}

	g.settingsMu.Lock()
	st := g.settings
	g.settingsMu.Unlock()
	st.WebDAV = nil // 同步凭证不入备份（st 是副本，不影响真实设置）
	st.Sync = nil   // 同步目标与口令同样不入备份
	st.OTLP = nil   // OTLP headers 可能含鉴权令牌，不入备份
	// 代理 URL 可能含 user:pass；备份会被上传到云端，一律不导出（恢复后需重设）。
	st.GlobalProxy = ""
	st.ProviderProxies = nil
	st.AccountProxies = nil
	// 余额接口令牌同样是本机绑定加密，且 URL 可能是内网地址：不随备份外传。
	st.ProviderBalances = nil
	// 远程实例的访问密钥同样是本机加密；URL 可能是内网地址，不随备份外传。
	st.RemoteQuotas = nil
	// 预设 Header 可能带鉴权；备份会传到云端，只导出打码值（导入时丢弃）。
	st.RequestPresets = maskedRequestPresets(st.RequestPresets)
	bundle.Settings = &st

	// 只导出来自本地文件的 spec；remote 来源的会在有网时自动拉回。
	for _, sp := range spec.Default().All() {
		if sp.Source != "" && !startsWith(sp.Source, "local:") {
			continue
		}
		cp := *sp
		cp.Source = ""
		cp.UpdatedAt = 0
		cp.Revision = 0
		bundle.Specs = append(bundle.Specs, &cp)
	}

	if g.library != nil {
		snap := g.library.List()
		bundle.Library = &snap
	}
	bundle.Icons = g.backupIcons()

	for _, e := range g.poolAll() {
		snap := e.Snapshot()
		bundle.Accounts = append(bundle.Accounts, backupAccountMeta{
			AccountID:  snap.AccountID,
			ProviderID: snap.ProviderID,
			Label:      snap.Label,
			Tags:       snap.Tags,
		})
	}
	return bundle
}

// handleBackupImport POST /api/backup/import
//
// 两种形态：
//   - {"data": base64, "pass": "口令"} —— 桌面壳导出的密封文件；
//   - 直接是导出的 bundle JSON —— 兼容旧流程。
func (g *Gateway) handleBackupImport(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "读取请求体失败"})
		return
	}
	var req struct {
		Data    string `json:"data"`
		Pass    string `json:"pass"`
		Library bool   `json:"library"`
	}
	_ = json.Unmarshal(raw, &req)
	var bundle backupBundle
	sealedFlow := req.Data != ""
	if sealedFlow {
		blob, err := base64.StdEncoding.DecodeString(req.Data)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "文件内容不是合法的 base64"})
			return
		}
		plain, err := openBackup(blob, req.Pass)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := json.Unmarshal(plain, &bundle); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "备份文件解析失败: " + err.Error()})
			return
		}
	} else {
		if err := json.Unmarshal(raw, &bundle); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "备份文件解析失败: " + err.Error()})
			return
		}
	}
	applied := g.applyBundle(&bundle)
	g.saveAccountsNow()

	if !sealedFlow {
		writeJSON(w, http.StatusOK, map[string]any{"imported": applied})
		return
	}
	needKey := make([]string, 0, 4)
	seen := map[string]bool{}
	for _, meta := range bundle.Accounts {
		if seen[meta.ProviderID] {
			continue
		}
		seen[meta.ProviderID] = true
		has := false
		if g.pool != nil {
			for _, e := range g.pool.ListByProvider(meta.ProviderID) {
				if e.GetCredential("api_key") != "" || e.GetCredential("access_token") != "" {
					has = true
					break
				}
			}
		}
		if !has {
			name := meta.ProviderID
			requiresKey := false
			if p, ok := provider.Get(meta.ProviderID); ok {
				m := p.Manifest()
				name = m.DisplayName
				// 只有声明了必填密钥字段的渠道才提醒；mock 之类无需密钥的渠道不吓人。
				for _, f := range m.AuthFormSchema {
					if f.Required && (f.Key == "api_key" || f.Key == "access_token") {
						requiresKey = true
						break
					}
				}
			}
			if requiresKey {
				needKey = append(needKey, name)
			}
		}
	}
	profiles := 0
	if bundle.Settings != nil {
		profiles = len(bundle.Settings.Profiles)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"Added": 0, "Replaced": 0, "Profiles": profiles, "Agents": 0,
		"NeedKey": needKey, "SignIn": []string{},
		"Plugins": []string{}, "PluginErrors": map[string]string{},
		"Library":         applied["library"],
		"LibraryProblems": []any{},
		"imported":        applied,
	})
}

// handleBackupExportFile POST /api/backup/export
//
// 桌面壳的「导出…」：口令密封后落盘成 .aether-backup，返回 {path}。
func (g *Gateway) handleBackupExportFile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pass    string `json:"pass"`
		Library bool   `json:"library"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	if body.Pass == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "口令不能为空：导出文件用它密封"})
		return
	}
	raw, err := json.Marshal(g.buildBackupBundle())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "备份序列化失败: " + err.Error()})
		return
	}
	sealed, err := sealBackup(raw, body.Pass)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "密封失败: " + err.Error()})
		return
	}
	dir := ""
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, "Downloads")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			dir = candidate
		}
	}
	if dir == "" {
		dir = os.TempDir()
	}
	name := fmt.Sprintf("aether-backup-%s.aether-backup", time.Now().Format("20060102-150405"))
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, sealed, 0o600); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "写入失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "bytes": len(sealed)})
}

// applyBundle 把备份 bundle 逐节应用到本机，返回各节是否生效。
// 某节失败不影响其它节。本机导入与 WebDAV pull 共用。
// 注意：bundle.Settings 里的 WebDAV 字段即使存在也不会被应用
// （同步凭证不应随备份流转），只取隐私遮蔽 / 价格表 / 虚拟组覆盖。
func (g *Gateway) applyBundle(bundle *backupBundle) map[string]bool {
	applied := map[string]bool{}

	// 1. 设置（隐私遮蔽 / 价格表 / 虚拟组覆盖）。
	if bundle.Settings != nil {
		g.settingsMu.Lock()
		g.settings.Privacy = bundle.Settings.Privacy
		if bundle.Settings.ModelPrices != nil {
			g.settings.ModelPrices = bundle.Settings.ModelPrices
		}
		if bundle.Settings.ModelGroupsOverride != nil {
			g.settings.ModelGroupsOverride = bundle.Settings.ModelGroupsOverride
		}
		// 迁移/撤销时用户真正想带回来的那部分（凭证与网络出口不在此列：
		// 代理与同步配置在 buildBackupBundle 里已被剔除，端口/监听地址
		// 属于本机运行态，不随备份覆盖）。
		if bundle.Settings.UI != nil {
			g.settings.UI = bundle.Settings.UI
		}
		g.settings.DisabledProviders = bundle.Settings.DisabledProviders
		g.settings.ProviderOrder = bundle.Settings.ProviderOrder
		g.settings.RoutingGroupOrder = bundle.Settings.RoutingGroupOrder
		g.settings.DeletedGroups = bundle.Settings.DeletedGroups
		g.settings.FoundGroups = bundle.Settings.FoundGroups
		g.settings.CORSOrigins = bundle.Settings.CORSOrigins
		g.settings.AllowIPs = bundle.Settings.AllowIPs
		g.settings.Profiles = bundle.Settings.Profiles
		g.settings.ActiveProfile = bundle.Settings.ActiveProfile
		g.settings.LaunchCommands = bundle.Settings.LaunchCommands
		if bundle.Settings.Tools != nil {
			g.settings.Tools = bundle.Settings.Tools
		}
		if bundle.Settings.RequestPresets != nil {
			g.settings.RequestPresets = sanitizeImportedPresets(bundle.Settings.RequestPresets)
			known := make(map[string]bool, len(g.settings.RequestPresets))
			for _, p := range g.settings.RequestPresets {
				known[p.ID] = true
			}
			g.settings.ProviderPresets = filterPresetAttachments(bundle.Settings.ProviderPresets, known)
			g.settings.RoutePresets = filterPresetAttachments(bundle.Settings.RoutePresets, known)
		}
		saveErr := g.saveSettings()
		g.rebuildPrivacyEngine()
		groups := g.settings.ModelGroupsOverride
		g.settingsMu.Unlock()
		g.syncRedactPrefs()

		if saveErr == nil && len(groups) > 0 {
			g.reapplyModelGroups(groups)
		}
		if saveErr == nil && bundle.Settings.RequestPresets != nil {
			g.applyProviderTransportAll()
		}
		applied["settings"] = saveErr == nil
	}

	// 1.5 自定义供应商图标：写回配置目录，设置里的 file: 引用才能解析。
	if len(bundle.Icons) > 0 {
		dir := g.iconsDir()
		if err := os.MkdirAll(dir, 0o755); err == nil {
			n := 0
			for name, b64 := range bundle.Icons {
				if !iconNameRe.MatchString(name) {
					continue
				}
				raw, err := base64.StdEncoding.DecodeString(b64)
				if err != nil || len(raw) > iconMaxBytes {
					continue
				}
				if sniffIconExt(raw) == "" {
					continue
				}
				if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err == nil {
					n++
				}
			}
			applied["icons"] = n > 0
		}
	}

	// 2. 渠道 spec：写回本地文件后整目录重载。
	if len(bundle.Specs) > 0 && g.cfg.SpecDir != "" {
		if err := os.MkdirAll(g.cfg.SpecDir, 0o755); err == nil {
			okN := 0
			for _, sp := range bundle.Specs {
				if sp.ProviderID == "" {
					continue
				}
				b, err := json.MarshalIndent(sp, "", "  ")
				if err != nil {
					continue
				}
				path := filepath.Join(g.cfg.SpecDir, sp.ProviderID+".json")
				if err := os.WriteFile(path, b, 0o600); err == nil {
					okN++
				}
			}
			if err := spec.Default().Reload(); err == nil {
				g.models.Rebuild()
			}
			applied["specs"] = okN > 0
		}
	}

	// 3. 资源库：按 ID 合并（同 ID 覆盖，其余保留）。
	if bundle.Library != nil && g.library != nil {
		n := 0
		for _, p := range bundle.Library.Prompts {
			if _, err := g.library.SavePrompt(p); err == nil {
				n++
			}
		}
		for _, m := range bundle.Library.MCPServers {
			if _, err := g.library.SaveMCP(m); err == nil {
				n++
			}
		}
		for _, sk := range bundle.Library.Skills {
			if _, err := g.library.SaveSkill(sk); err == nil {
				n++
			}
		}
		for _, a := range bundle.Library.Agents {
			if _, err := g.library.SaveAgent(a); err == nil {
				n++
			}
		}
		applied["library"] = n > 0
	}

	// 4. 账号标签：按 account_id 精确匹配，其次 provider+label。
	if len(bundle.Accounts) > 0 {
		okN := 0
		for _, meta := range bundle.Accounts {
			env, ok := g.pool.Get(meta.AccountID)
			if !ok && meta.Label != "" {
				for _, e := range g.pool.ListByProvider(meta.ProviderID) {
					if e.Label == meta.Label {
						env = e
						ok = true
						break
					}
				}
			}
			if ok {
				env.SetTags(meta.Tags)
				okN++
			}
		}
		applied["accounts"] = okN > 0
	}
	return applied
}

// handleBackupPush POST /api/backup/push —— 把当前配置备份推送到 WebDAV。
func (g *Gateway) handleBackupPush(w http.ResponseWriter, r *http.Request) {
	sc := g.activeSync()
	if sc == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "尚未配置云同步，请先在设置页填写 WebDAV / S3 / GitHub"})
		return
	}
	if sc.Kind == "webdav" && sc.WebDAV != nil {
		if _, err := normalizeWebDAVURL(sc.WebDAV.URL); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	size, remote, err := g.pushBackupToWebDAV()
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pushed": true, "bytes": size, "remote": remote})
}

// handleBackupPull POST /api/backup/pull —— 从 WebDAV 拉取备份并应用。
func (g *Gateway) handleBackupPull(w http.ResponseWriter, r *http.Request) {
	sc := g.activeSync()
	if sc == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "尚未配置云同步，请先在设置页填写 WebDAV / S3 / GitHub"})
		return
	}
	if sc.Kind == "webdav" && sc.WebDAV != nil {
		if _, err := normalizeWebDAVURL(sc.WebDAV.URL); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	data, remote, err := g.pullBackupFromTarget()
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	var bundle backupBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "远端备份文件解析失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": g.applyBundle(&bundle), "remote": remote})
}

// reapplyModelGroups 按覆盖配置重建虚拟组（导入备份后调用）。
func (g *Gateway) reapplyModelGroups(groups map[string]json.RawMessage) {
	for name, raw := range groups {
		chain, strategy, err := parseModelGroup(raw)
		if err != nil {
			continue
		}
		valid := make([]string, 0, len(chain))
		for _, pid := range chain {
			if _, ok := provider.Get(pid); ok {
				valid = append(valid, pid)
			}
		}
		if len(valid) > 0 {
			g.models.SetVirtualGroupWithStrategy(name, valid, strategy)
		}
	}
}

// poolAll 在极简构造（测试/命令行工具）下返回空列表而不是 panic。
func (g *Gateway) poolAll() []*provider.AccountEnv {
	if g.pool == nil {
		return nil
	}
	return g.pool.All()
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
