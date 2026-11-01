package gateway

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// 统一云同步目标：WebDAV / S3 / GitHub 三选一。
//
// 备份内容与既有 /api/backup/* 一致（设置 / spec / 账号标签，不含凭证），
// 可选口令密封；口令本身用本机绑定加密保存。

const syncBackupFilename = "aether-backup.json"

type syncTarget struct {
	kind       string
	webdav     *WebDAVConfig
	s3         *SyncS3Config
	github     *SyncGitHubConfig
	passphrase string
	auto       int
}

// activeSync 返回当前生效的同步配置（含旧版 WebDAV 字段），无则 nil。
func (g *Gateway) activeSync() *SyncConfig {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.Sync != nil && strings.TrimSpace(g.settings.Sync.Kind) != "" {
		return cloneSyncConfig(g.settings.Sync)
	}
	if wd := g.settings.WebDAV; wd != nil && strings.TrimSpace(wd.URL) != "" {
		cp := *wd
		return &SyncConfig{Kind: "webdav", WebDAV: &cp, AutoPushMinutes: wd.AutoPushMinutes}
	}
	return nil
}

func cloneSyncConfig(sc *SyncConfig) *SyncConfig {
	if sc == nil {
		return nil
	}
	cp := *sc
	if sc.WebDAV != nil {
		wd := *sc.WebDAV
		cp.WebDAV = &wd
	}
	if sc.S3 != nil {
		s3 := *sc.S3
		cp.S3 = &s3
	}
	if sc.GitHub != nil {
		gh := *sc.GitHub
		cp.GitHub = &gh
	}
	return &cp
}

func (sc *SyncConfig) target() (*syncTarget, error) {
	if sc == nil {
		return nil, errors.New("尚未配置云同步")
	}
	t := &syncTarget{kind: sc.Kind, passphrase: decryptPassphrase(sc.PassphraseEnc), auto: sc.AutoPushMinutes}
	switch sc.Kind {
	case "webdav":
		if sc.WebDAV == nil || strings.TrimSpace(sc.WebDAV.URL) == "" {
			return nil, errors.New("WebDAV 地址为空")
		}
		wd := *sc.WebDAV
		t.webdav = &wd
	case "s3":
		if sc.S3 == nil || strings.TrimSpace(sc.S3.Bucket) == "" {
			return nil, errors.New("S3 bucket 为空")
		}
		s3 := *sc.S3
		t.s3 = &s3
	case "github":
		if sc.GitHub == nil || !strings.Contains(sc.GitHub.Repo, "/") {
			return nil, errors.New("GitHub 仓库要写成 owner/repo")
		}
		gh := *sc.GitHub
		t.github = &gh
	default:
		return nil, errors.New("未知的同步类型: " + sc.Kind)
	}
	return t, nil
}

func (t *syncTarget) filename() string {
	switch t.kind {
	case "webdav":
		return davFilename(t.webdav)
	default:
		return syncBackupFilename
	}
}

func (t *syncTarget) Put(data []byte) (string, error) {
	switch t.kind {
	case "webdav":
		name := t.filename()
		if err := davPut(t.webdav, name, data); err != nil {
			return "", err
		}
		return davRemoteURL(t.webdav, name), nil
	case "s3":
		return s3PutObject(t.s3, s3Key(t.s3, t.filename()), data)
	case "github":
		return githubPutObject(t.github, t.filename(), data)
	}
	return "", errors.New("未知的同步类型")
}

func (t *syncTarget) Get() ([]byte, string, error) {
	switch t.kind {
	case "webdav":
		name := t.filename()
		data, err := davGet(t.webdav, name)
		return data, davRemoteURL(t.webdav, name), err
	case "s3":
		return s3GetObject(t.s3, s3Key(t.s3, t.filename()))
	case "github":
		return githubGetObject(t.github, t.filename())
	}
	return nil, "", errors.New("未知的同步类型")
}

// pushBackupToTarget 生成备份（必要时密封）并上传，同时记录最近一次状态。
func (g *Gateway) pushBackupToTarget() (int, string, error) {
	sc := g.activeSync()
	t, err := sc.target()
	if err != nil {
		return 0, "", err
	}
	raw, err := json.Marshal(g.buildBackupBundle())
	if err != nil {
		return 0, "", err
	}
	sealed, err := sealBackup(raw, t.passphrase)
	if err != nil {
		return 0, "", err
	}
	remote, err := t.Put(sealed)
	if err != nil {
		g.noteSyncResult(0, "", err)
		return 0, "", err
	}
	g.noteSyncResult(len(sealed), remote, nil)
	return len(sealed), remote, nil
}

// pullBackupFromTarget 下载并解封备份（不应用）。
func (g *Gateway) pullBackupFromTarget() ([]byte, string, error) {
	sc := g.activeSync()
	t, err := sc.target()
	if err != nil {
		return nil, "", err
	}
	raw, remote, err := t.Get()
	if err != nil {
		g.noteSyncResult(0, "", err)
		return nil, "", err
	}
	plain, err := openBackup(raw, t.passphrase)
	if err != nil {
		return nil, "", err
	}
	return plain, remote, nil
}

// noteSyncResult 记录最近一次同步时间与错误（供设置页显示）。
func (g *Gateway) noteSyncResult(size int, remote string, err error) {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	// 旧版只有 WebDAV 字段时没有地方记状态：不凭一次推送就造出一个
	// 空目标的 Sync 配置，那会让下一次自动推送失败。
	if g.settings.Sync == nil {
		return
	}
	if err != nil {
		g.settings.Sync.LastError = err.Error()
	} else {
		g.settings.Sync.LastAt = time.Now().Unix()
		g.settings.Sync.LastRemote = remote
		g.settings.Sync.LastError = ""
	}
	_ = g.saveSettings()
}
