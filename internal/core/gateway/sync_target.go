package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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

// pushBackupToTarget 生成备份、与远端三路合并、条件写上传，并更新本地状态。
//
// 流程（对齐 aether 的同步语义）：
//  1. 取跨进程锁，避免 GUI 与 CLI 同时同步互相覆盖；
//  2. 读本机底稿（上次成功同步的内容）与远端当前内容；
//  3. base/local/remote 三路合并——两边都改过的字段保留本机并进冲突档案；
//  4. 条件写（If-Match）；版本不符就重新取远端、重新合并、再写一次；
//  5. 把合并结果落到本机（只影响本机没动过的字段，因此是安全的）；
//  6. 保存新底稿与 part 摘要。
func (g *Gateway) pushBackupToTarget() (int, string, error) {
	sc := g.activeSync()
	t, err := sc.target()
	if err != nil {
		return 0, "", err
	}
	release, err := g.acquireSyncLock()
	if err != nil {
		return 0, "", err
	}
	defer release()

	localRaw, err := json.Marshal(g.buildBackupBundle())
	if err != nil {
		return 0, "", err
	}
	st := g.loadSyncState()

	// 远端可能还不存在（首次同步）：那不是错误。
	remotePlain, etag, getErr := g.pullPlainForMerge(t)
	if getErr != nil {
		remotePlain, etag = nil, ""
	}

	merged := localRaw
	var conflicts []mergeConflict
	if len(remotePlain) > 0 {
		merged, conflicts, err = mergeBundles(st.Base, localRaw, remotePlain)
		if err != nil {
			g.noteSyncResult(0, "", fmt.Errorf("合并失败: %w", err))
			return 0, "", err
		}
	}

	// 合并结果与远端一致：没有要传的东西，只更新底稿（省一次上传）。
	if len(remotePlain) > 0 && samePartDigest(bundlePartDigest(merged), bundlePartDigest(remotePlain)) {
		g.commitSyncState(st, merged, etag, conflicts, false)
		g.applyMergedBundle(merged, localRaw)
		size := len(remotePlain)
		g.noteSyncResult(size, "", nil)
		return size, "", nil
	}

	sealed, err := sealBackup(merged, t.passphrase)
	if err != nil {
		return 0, "", err
	}
	remote, newETag, err := t.putIfMatch(sealed, etag)
	if errors.Is(err, errSyncConflict) {
		// 有人在合并之后又推了一次：重新取远端、重新合并、再写一次。
		// 只重试一次——连续冲突说明另一台机器正在高频写入，再试也是白试。
		remotePlain, etag, getErr = g.pullPlainForMerge(t)
		if getErr != nil {
			g.noteSyncResult(0, "", fmt.Errorf("冲突后重新读取远端失败: %w", getErr))
			return 0, "", getErr
		}
		merged, conflicts, err = mergeBundles(st.Base, localRaw, remotePlain)
		if err != nil {
			return 0, "", err
		}
		if sealed, err = sealBackup(merged, t.passphrase); err != nil {
			return 0, "", err
		}
		remote, newETag, err = t.putIfMatch(sealed, etag)
	}
	blind := false
	if errors.Is(err, errBlindWrite) {
		// 目标不支持条件写：本次写入仍然做了合并，只是没法保证窗口内无并发。
		blind = true
		err = nil
	}
	if err != nil {
		g.noteSyncResult(0, "", err)
		return 0, "", err
	}
	g.commitSyncState(st, merged, newETag, conflicts, blind)
	g.applyMergedBundle(merged, localRaw)
	size := len(sealed)
	g.noteSyncResult(size, remote, nil)
	return size, remote, nil
}

// pullPlainForMerge 取远端并解封；远端不存在时返回空（不视为错误）。
func (g *Gateway) pullPlainForMerge(t *syncTarget) ([]byte, string, error) {
	raw, etag, err := t.getWithETag()
	if err != nil {
		return nil, "", err
	}
	plain, err := openBackup(raw, t.passphrase)
	if err != nil {
		return nil, "", err
	}
	return plain, etag, nil
}

// commitSyncState 保存新底稿、part 摘要、ETag 与冲突清单。
func (g *Gateway) commitSyncState(st *syncState, merged []byte, etag string, conflicts []mergeConflict, blind bool) {
	if st == nil {
		st = &syncState{}
	}
	if len(conflicts) > 0 {
		if path := g.archiveSyncConflicts(conflicts); path != "" {
			g.settingsMu.Lock()
			if g.settings.Sync != nil {
				g.settings.Sync.LastError = describeConflicts(conflicts)
			}
			g.settingsMu.Unlock()
		}
	}
	st.Base = json.RawMessage(merged)
	st.BaseAt = time.Now().Unix()
	st.Parts = bundlePartDigest(merged)
	if etag != "" {
		st.ETag = etag
	}
	st.Blind = blind
	st.LastConflicts = nil
	for _, c := range conflicts {
		st.LastConflicts = append(st.LastConflicts, c.Path)
	}
	g.saveSyncState(st)
}

// applyMergedBundle 把合并结果落到本机。
//
// 安全前提：合并只在"本机没动过"的字段上取远端值，所以对本地用户来说
// 不会覆盖他刚改的东西；真正两边都改的字段保留本机并已进冲突档案。
func (g *Gateway) applyMergedBundle(merged, localRaw []byte) {
	if len(merged) == 0 || bytes.Equal(merged, localRaw) {
		return
	}
	var bundle backupBundle
	if json.Unmarshal(merged, &bundle) != nil {
		return
	}
	// 先留本机现状，Undo 用它与"合并后"对换，行为与手动恢复一致。
	_ = g.writeUndoSnapshot()
	g.applyBundle(&bundle)
	g.models.Rebuild()
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
