package gateway

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// aether 设置页「同步与备份」读 /api/davsync。这里支持 WebDAV / S3 / GitHub
// 三种目标与可选的口令密封；备份内容与 /api/backup/* 一致（不含凭证）。

func (g *Gateway) davsyncView(errText string) map[string]any {
	sc := g.activeSync()
	view := map[string]any{
		"on": false, "kind": "webdav", "url": "", "user": "",
		"passwordSet": false, "passphraseSet": false, "auto": 0,
		"keys": false, "agents": true, "library": true, "usage": false,
	}
	if sc == nil {
		if errText != "" {
			view["error"] = errText
		}
		return view
	}
	view["on"] = true
	view["kind"] = sc.Kind
	view["auto"] = sc.AutoPushMinutes
	view["passphraseSet"] = sc.PassphraseEnc != ""
	view["last"] = sc.LastAt * 1000
	if sc.LastRemote != "" {
		view["remote"] = sc.LastRemote
	}
	switch sc.Kind {
	case "webdav":
		if sc.WebDAV != nil {
			view["url"] = sc.WebDAV.URL
			view["user"] = sc.WebDAV.Username
			view["passwordSet"] = sc.WebDAV.Password != ""
			view["filename"] = davFilename(sc.WebDAV)
		}
	case "s3":
		if sc.S3 != nil {
			view["url"] = "s3://" + sc.S3.Bucket
			if sc.S3.Prefix != "" {
				view["url"] = "s3://" + sc.S3.Bucket + "/" + strings.Trim(sc.S3.Prefix, "/")
			}
			view["user"] = sc.S3.AccessKey
			view["passwordSet"] = sc.S3.SecretKey != ""
			view["endpoint"] = sc.S3.Endpoint
			view["region"] = sc.S3.Region
			view["pathStyle"] = sc.S3.PathStyle
		}
	case "github":
		if sc.GitHub != nil {
			ghURL := "github://" + sc.GitHub.Repo
			if sc.GitHub.Path != "" {
				ghURL += "/" + strings.Trim(sc.GitHub.Path, "/")
			}
			view["url"] = ghURL
			view["branch"] = sc.GitHub.Branch
			view["passwordSet"] = sc.GitHub.Token != ""
		}
	}
	if errText != "" {
		view["error"] = errText
	} else if sc.LastError != "" {
		view["error"] = sc.LastError
	}
	return view
}

func (g *Gateway) handleDavSync(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.davsyncView(""))
}

// parseS3URL 解析 s3://bucket/prefix。
func parseS3URL(raw string) (bucket, prefix string, ok bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(raw), "s3://") {
		return "", "", false
	}
	rest := raw[5:]
	bucket, prefix, _ = strings.Cut(rest, "/")
	return bucket, strings.Trim(prefix, "/"), bucket != ""
}

// parseGitHubURL 解析 github://owner/repo/path。
func parseGitHubURL(raw string) (repo, path string, ok bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(raw), "github://") {
		return "", "", false
	}
	rest := raw[len("github://"):]
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	repo = parts[0] + "/" + parts[1]
	if len(parts) == 3 {
		path = strings.Trim(parts[2], "/")
	}
	return repo, path, true
}

func (g *Gateway) handleDavSyncSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind       string `json:"kind"`
		URL        string `json:"url"`
		User       string `json:"user"`
		Password   string `json:"password"`
		Endpoint   string `json:"endpoint"`
		Region     string `json:"region"`
		PathStyle  bool   `json:"pathStyle"`
		Branch     string `json:"branch"`
		Passphrase string `json:"passphrase"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	kind := strings.ToLower(strings.TrimSpace(body.Kind))
	if kind == "" {
		kind = "webdav"
	}

	g.settingsMu.Lock()
	old := cloneSyncConfig(g.settings.Sync)
	sc := &SyncConfig{Kind: kind}
	if old != nil {
		sc.AutoPushMinutes = old.AutoPushMinutes
		sc.PassphraseEnc = old.PassphraseEnc
	}
	fail := func(status int, msg string) {
		g.settingsMu.Unlock()
		writeJSON(w, status, map[string]any{"error": msg})
	}
	switch kind {
	case "webdav":
		cfg := &WebDAVConfig{URL: strings.TrimSpace(body.URL), Username: strings.TrimSpace(body.User), Password: body.Password}
		if old != nil && old.WebDAV != nil {
			cfg.Filename = old.WebDAV.Filename
			if cfg.Password == "" {
				cfg.Password = old.WebDAV.Password
			}
		}
		if _, err := normalizeWebDAVURL(cfg.URL); err != nil {
			fail(http.StatusBadRequest, err.Error())
			return
		}
		sc.WebDAV = cfg
	case "s3":
		bucket, prefix, ok := parseS3URL(body.URL)
		if !ok {
			fail(http.StatusBadRequest, "S3 地址要写成 s3://bucket/前缀")
			return
		}
		cfg := &SyncS3Config{
			Bucket: bucket, Prefix: prefix, Endpoint: strings.TrimSpace(body.Endpoint),
			Region: strings.TrimSpace(body.Region), AccessKey: strings.TrimSpace(body.User),
			SecretKey: body.Password, PathStyle: body.PathStyle,
		}
		if old != nil && old.S3 != nil && cfg.SecretKey == "" {
			cfg.SecretKey = old.S3.SecretKey
		}
		if cfg.AccessKey == "" || cfg.SecretKey == "" {
			fail(http.StatusBadRequest, "S3 需要 Access Key 与 Secret")
			return
		}
		sc.S3 = cfg
	case "github":
		repo, path, ok := parseGitHubURL(body.URL)
		if !ok {
			fail(http.StatusBadRequest, "GitHub 地址要写成 github://owner/repo/目录")
			return
		}
		cfg := &SyncGitHubConfig{Repo: repo, Path: path, Branch: strings.TrimSpace(body.Branch), Token: body.Password}
		if old != nil && old.GitHub != nil && cfg.Token == "" {
			cfg.Token = old.GitHub.Token
		}
		if cfg.Token == "" {
			fail(http.StatusBadRequest, "GitHub 需要具有 Contents 读写权限的 token")
			return
		}
		sc.GitHub = cfg
	default:
		fail(http.StatusBadRequest, "未知的同步类型: "+kind)
		return
	}
	if strings.TrimSpace(body.Passphrase) != "" {
		if len([]rune(body.Passphrase)) < 8 {
			fail(http.StatusBadRequest, "备份口令至少 8 个字符")
			return
		}
		enc, err := encryptPassphrase(body.Passphrase)
		if err != nil {
			fail(http.StatusInternalServerError, "口令保存失败: "+err.Error())
			return
		}
		sc.PassphraseEnc = enc
	} else if sc.PassphraseEnc == "" {
		fail(http.StatusBadRequest, "云同步必须设置至少 8 位备份口令；明文备份不再允许上传")
		return
	}
	g.settings.Sync = sc
	// 兼容旧的 WebDAV 设置页：kind=webdav 时同步写回。
	if kind == "webdav" {
		wd := *sc.WebDAV
		wd.AutoPushMinutes = sc.AutoPushMinutes
		g.settings.WebDAV = &wd
	}
	err := g.saveSettings()
	g.settingsMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, g.davsyncView(""))
}

func (g *Gateway) handleDavSyncNow(w http.ResponseWriter, r *http.Request) {
	size, remote, err := g.pushBackupToTarget()
	if err != nil {
		writeJSON(w, http.StatusOK, g.davsyncView(err.Error()))
		return
	}
	view := g.davsyncView("")
	view["last"] = time.Now().UnixMilli()
	view["bytes"] = size
	view["remote"] = remote
	writeJSON(w, http.StatusOK, view)
}

func (g *Gateway) handleDavSyncAuto(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Minutes int `json:"minutes"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	g.settingsMu.Lock()
	if g.settings.Sync != nil {
		min := body.Minutes
		if min < 0 {
			min = 0
		}
		g.settings.Sync.AutoPushMinutes = min
		if g.settings.WebDAV != nil {
			g.settings.WebDAV.AutoPushMinutes = min
		}
		_ = g.saveSettings()
	}
	g.settingsMu.Unlock()
	writeJSON(w, http.StatusOK, g.davsyncView(""))
}

func (g *Gateway) handleDavSyncRestore(w http.ResponseWriter, r *http.Request) {
	data, remote, err := g.pullBackupFromTarget()
	if err != nil {
		writeJSON(w, http.StatusOK, g.davsyncView(err.Error()))
		return
	}
	var bundle backupBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		writeJSON(w, http.StatusOK, g.davsyncView("远端备份解析失败: "+err.Error()))
		return
	}
	// 覆盖之前先留下本机现状，Undo 用它换回来。
	if err := g.writeUndoSnapshot(); err != nil {
		log.Printf("[WARN] 恢复前快照写入失败（将无法撤销）: %v", err)
	}
	view := g.davsyncView("")
	view["imported"] = g.applyBundle(&bundle)
	view["remote"] = remote
	view["undo"] = g.hasUndoSnapshot()
	view["notice"] = map[string]any{"restored": true, "here": []string{"settings"}}
	writeJSON(w, http.StatusOK, view)
}

func (g *Gateway) handleDavSyncOff(w http.ResponseWriter, r *http.Request) {
	g.settingsMu.Lock()
	g.settings.Sync = nil
	g.settings.WebDAV = nil
	_ = g.saveSettings()
	g.settingsMu.Unlock()
	writeJSON(w, http.StatusOK, g.davsyncView(""))
}

// handleCLIStatus GET /api/cli —— 把 CLI 装进 PATH 是桌面壳能力，这里如实返回未安装。
func (g *Gateway) handleCLIStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"installed": false, "windows": false, "shim": false, "exe": "",
	})
}

// discardResponse 收集一个处理器的状态与正文，不写回客户端。
type discardResponse struct {
	header http.Header
	status int
	body   []byte
}

func (d *discardResponse) Header() http.Header { return d.header }
func (d *discardResponse) WriteHeader(code int) {
	if d.status == 0 {
		d.status = code
	}
}
func (d *discardResponse) Write(b []byte) (int, error) {
	if d.status == 0 {
		d.status = http.StatusOK
	}
	d.body = append(d.body, b...)
	return len(b), nil
}

func (d *discardResponse) message() string {
	var m struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(d.body, &m) == nil && m.Error != "" {
		return m.Error
	}
	return strings.TrimSpace(string(d.body))
}
