package gateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WebDAV 云同步 —— 配置备份的远端存储。
//
// 对标 aether 的 WebDAV/S3 同步：S3 需要额外的签名依赖，WebDAV 是
// 纯标准库（net/http 的 PUT/GET/MKCOL）就能实现的通用协议，
// 坚果云 / 群晖 / Nextcloud 等都原生支持，故刻意只做 WebDAV。
//
// 客户端是"最小实现"：不做 PROPFIND 列目录、不做锁；
// 上传前逐段 MKCOL 建目录（405/301 视为已存在），够用即可。

const webdavTimeout = 30 * time.Second

// davFilename 远端备份文件名，默认 aether-backup.json。
func davFilename(cfg *WebDAVConfig) string {
	name := strings.TrimSpace(cfg.Filename)
	if name == "" {
		return "aether-backup.json"
	}
	return strings.TrimPrefix(name, "/")
}

// normalizeWebDAVURL 规范化并校验 WebDAV 基础 URL：
// 去首尾空白、末尾补 /；仅允许 http(s)；必须带主机名。
func normalizeWebDAVURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("WebDAV URL 未配置")
	}
	if !strings.HasSuffix(raw, "/") {
		raw += "/"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("WebDAV URL 无效: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("WebDAV URL 仅支持 http(s)，收到 %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("WebDAV URL 缺少主机名")
	}
	return u, nil
}

// davJoinPath 拼接 base 路径与远端文件名。
func davJoinPath(basePath, name string) string {
	return strings.TrimSuffix(basePath, "/") + "/" + strings.TrimPrefix(name, "/")
}

// davRequest 构造带 Basic Auth 的 WebDAV 请求。
func davRequest(method, rawURL string, cfg *WebDAVConfig, body []byte) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, rawURL, rd)
	if err != nil {
		return nil, err
	}
	if cfg.Username != "" || cfg.Password != "" {
		req.SetBasicAuth(cfg.Username, cfg.Password)
	}
	return req, nil
}

// davMKCOL 建一级远端目录。
// 405（目录已存在）与 301（重定向，通常同样意味着已存在）视为成功。
func davMKCOL(client *http.Client, cfg *WebDAVConfig, base *url.URL, dir string) error {
	u := *base
	u.Path = dir
	req, err := davRequest("MKCOL", u.String(), cfg, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("WebDAV 建目录失败: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	switch {
	case resp.StatusCode == http.StatusMethodNotAllowed, resp.StatusCode == http.StatusMovedPermanently:
		return nil // 已存在
	case resp.StatusCode >= 200 && resp.StatusCode <= 299:
		return nil
	default:
		return fmt.Errorf("WebDAV 建目录 %s 失败：远端返回 %s", dir, resp.Status)
	}
}

// davPut 上传 data 到远端 <cfg.URL>/<name>。
// 先对远端路径逐段 MKCOL 建目录，再 PUT（Basic Auth、application/json）。
// 非 2xx 返回带状态码的错误。
func davPut(cfg *WebDAVConfig, name string, data []byte) error {
	base, err := normalizeWebDAVURL(cfg.URL)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: webdavTimeout}

	// 逐段建目录：最后一段是文件本身，跳过。
	full := davJoinPath(base.Path, name)
	segs := strings.Split(strings.Trim(full, "/"), "/")
	dir := ""
	for _, seg := range segs[:max(len(segs)-1, 0)] {
		if seg == "" {
			continue
		}
		dir += "/" + seg
		if err := davMKCOL(client, cfg, base, dir); err != nil {
			return err
		}
	}

	u := *base
	u.Path = full
	req, err := davRequest(http.MethodPut, u.String(), cfg, data)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("WebDAV 上传失败: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("WebDAV 上传失败：远端返回 %s", resp.Status)
	}
	return nil
}

// davGet 下载远端 <cfg.URL>/<name>。
// 404 返回明确错误（远端还没有备份文件）。
func davGet(cfg *WebDAVConfig, name string) ([]byte, error) {
	base, err := normalizeWebDAVURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	u := *base
	u.Path = davJoinPath(base.Path, name)
	req, err := davRequest(http.MethodGet, u.String(), cfg, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: webdavTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("WebDAV 下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("远端还没有备份文件（%s）", name)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("WebDAV 下载失败：远端返回 %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// davRemoteURL 供响应展示的远端完整地址（去掉可能内嵌在 URL 里的用户信息）。
func davRemoteURL(cfg *WebDAVConfig, name string) string {
	base, err := normalizeWebDAVURL(cfg.URL)
	if err != nil {
		return strings.TrimSuffix(strings.TrimSpace(cfg.URL), "/") + "/" + strings.TrimPrefix(name, "/")
	}
	u := *base
	u.User = nil
	u.Path = davJoinPath(base.Path, name)
	return u.String()
}

// webdavConfig 返回当前生效的 WebDAV 配置副本；未配置或 URL 为空返回 nil。
func (g *Gateway) webdavConfig() *WebDAVConfig {
	g.settingsMu.Lock()
	cfg := g.settings.WebDAV
	g.settingsMu.Unlock()
	if cfg == nil || strings.TrimSpace(cfg.URL) == "" {
		return nil
	}
	cp := *cfg
	return &cp
}

// pushBackupToWebDAV 生成配置备份并上传到 WebDAV，返回字节数与远端地址。
func (g *Gateway) pushBackupToWebDAV() (int, string, error) {
	// 统一入口：WebDAV / S3 / GitHub 都走 syncTarget（见 sync_target.go）。
	return g.pushBackupToTarget()
}

// StartWebDAVAutoPush 按 interval 周期把配置备份推送到 WebDAV。
//
// interval<=0 或当前未配置 WebDAV 时不启动调度，返回空 stop 函数；
// 已启动后修改 URL/密码/文件名，下一个周期自动生效（每次推送都读最新设置）。
// 推送失败只记日志，不中断调度。返回的 stop 函数可安全多次调用。
func (g *Gateway) StartWebDAVAutoPush(ctx context.Context, interval time.Duration) func() {
	if interval <= 0 || g.activeSync() == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, remote, err := g.pushBackupToWebDAV(); err != nil {
					log.Printf("[WARN] 云同步自动推送失败: %v", err)
				} else {
					log.Printf("[INFO] 云同步自动推送完成 → %s", remote)
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// WebDAVAutoPushInterval 返回设置里的自动推送间隔（<=0 表示未启用）。
// 供 cmd 侧启动接线使用，免去导出 runtimeSettings。
func (g *Gateway) WebDAVAutoPushInterval() time.Duration {
	sc := g.activeSync()
	if sc == nil {
		return 0
	}
	return time.Duration(sc.AutoPushMinutes) * time.Minute
}
