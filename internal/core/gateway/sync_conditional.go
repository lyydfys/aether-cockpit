package gateway

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// 条件写（optimistic concurrency）：带 If-Match 的 PUT。
//
// 为什么必须有：三路合并解决的是"我在合并时看到的那份远端"，但合并完成到
// 真正写上去之间还有一个窗口——这期间另一台机器可能又推了一次。没有条件写，
// 合并就白做了，第二次覆盖照样丢配置。
//
// 远端支持程度不同：
//   - WebDAV：标准支持 ETag + If-Match（412 = 版本不符）；
//   - GitHub：靠 Contents API 的 sha 字段做条件更新（409 = 版本不符）；
//   - S3：对象存储没有条件 PUT（PutObject 不接受 If-Match），只能退化成
//     "合并后再写，并标记 Blind 让下次重新合并"。

// errBlindWrite 表示目标不支持条件写（调用方应标记 Blind）。
var errBlindWrite = errors.New("远端不支持条件写")

// getWithETag 下载并返回版本标识。
func (t *syncTarget) getWithETag() (data []byte, etag string, err error) {
	switch t.kind {
	case "webdav":
		name := t.filename()
		data, etag, err = davGetWithETag(t.webdav, name)
		return data, etag, err
	case "github":
		// GitHub 的 sha 就是它的版本标识：读时把它当 etag 返回，写时按它做
		// 条件写。若读之后别的机器又提交过，sha 已经变了，GitHub 会返回 409，
		// 我们按冲突重读——不再"自己重读 sha 再覆盖"。
		data, sha, gerr := githubGetObject(t.github, t.filename())
		if gerr != nil {
			if strings.Contains(gerr.Error(), "404") || strings.Contains(gerr.Error(), "不存在") {
				return nil, "", errSyncRemoteAbsent
			}
			return nil, "", gerr
		}
		return data, "sha:" + sha, nil
	default:
		data, remote, gerr := t.Get()
		return data, remote, gerr
	}
}

// putIfMatch 条件写入；返回远端的新版本标识。
//
// ifMatch 为空表示"我认为远端不存在或用不上版本"——WebDAV 上会退化成
// 无条件写（并返回 errBlindWrite，让调用方记 Blind 供下次重算）。
func (t *syncTarget) putIfMatch(data []byte, ifMatch string) (remote string, etag string, err error) {
	switch t.kind {
	case "webdav":
		name := t.filename()
		etag, err = davPutIfMatch(t.webdav, name, data, ifMatch)
		return davRemoteURL(t.webdav, name), etag, err
	case "github":
		// 用读时拿到的 sha 做条件写：sha 不符时 GitHub 返回 409，转成冲突。
		sha := strings.TrimPrefix(ifMatch, "sha:")
		remote, err := githubPutObjectIfMatch(t.github, t.filename(), data, sha)
		if errors.Is(err, errSyncConflict) {
			return "", "", errSyncConflict
		}
		return remote, "", err
	default:
		// S3 等：没有条件 PUT。写完返回 errBlindWrite，由调用方标记 Blind。
		remote, err := t.Put(data)
		if err != nil {
			return "", "", err
		}
		return remote, "", errBlindWrite
	}
}

// davGetWithETag 下载并带回 ETag（服务器没给就返回空串）。
func davGetWithETag(cfg *WebDAVConfig, name string) ([]byte, string, error) {
	base, err := normalizeWebDAVURL(cfg.URL)
	if err != nil {
		return nil, "", err
	}
	u := *base
	u.Path = davJoinPath(base.Path, name)
	req, err := davRequest(http.MethodGet, u.String(), cfg, nil)
	if err != nil {
		return nil, "", err
	}
	client := &http.Client{Timeout: webdavTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("WebDAV 下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// 明确的"远端还没有这个文件"。与"读失败"必须区分开：
		// 前者可以安全地首次写入，后者一旦当成不存在就会覆盖另一台机器。
		return nil, "", errSyncRemoteAbsent
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, "", fmt.Errorf("WebDAV 下载失败：远端返回 %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return data, strings.TrimSpace(resp.Header.Get("ETag")), err
}

// davPutIfMatch 带 If-Match 的 PUT。412 返回 errSyncConflict。
func davPutIfMatch(cfg *WebDAVConfig, name string, data []byte, ifMatch string) (string, error) {
	base, err := normalizeWebDAVURL(cfg.URL)
	if err != nil {
		return "", err
	}
	// 先确保目录存在（与 davPut 一致：逐级 MKCOL）。
	client := &http.Client{Timeout: webdavTimeout}
	if err := davEnsureDirs(client, cfg, base, davJoinPath(base.Path, name)); err != nil {
		return "", err
	}
	u := *base
	u.Path = davJoinPath(base.Path, name)
	req, err := davRequest(http.MethodPut, u.String(), cfg, data)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	} else {
		// 没有已知版本时用 If-None-Match: * 表达"我认为远端不存在"：
		// 若另一台机器刚创建了它，服务端返回 412，我们走冲突重读，
		// 而不是无条件把它盖掉。
		req.Header.Set("If-None-Match", "*")
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("WebDAV 上传失败: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode == http.StatusPreconditionFailed {
		return "", errSyncConflict
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("WebDAV 上传失败：远端返回 %s", resp.Status)
	}
	etag := strings.TrimSpace(resp.Header.Get("ETag"))
	if etag == "" && ifMatch == "" {
		// 服务器既不校验也不回版本：本次写入是"盲写"。
		return "", errBlindWrite
	}
	return etag, nil
}
