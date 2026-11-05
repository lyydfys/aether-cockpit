package gateway

// 更新检查：优先读 AETHER_UPDATE_URL 的 JSON 清单；未配置时读取
// GitHub Releases（AETHER_GITHUB_REPO / settings.update_repo，默认本仓库）。
// 只做“检查 + 下载”，不自动替换正在运行的 exe。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/apppaths"
	"aether-cockpit/internal/provider"
)

const (
	defaultGitHubRepo = "lyydfys/aether-cockpit"
)

var githubRepoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func validGitHubRepo(repo string) bool {
	repo = strings.TrimSpace(repo)
	if !githubRepoPattern.MatchString(repo) {
		return false
	}
	parts := strings.Split(repo, "/")
	return len(parts) == 2 && parts[0] != "." && parts[0] != ".." && parts[1] != "." && parts[1] != ".."
}

type updateManifest struct {
	Version     string `json:"version"`
	URL         string `json:"url"`
	Notes       string `json:"notes"`
	PublishedAt string `json:"published_at"`
	SHA256      string `json:"sha256,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

type githubAsset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
}

type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Body        string        `json:"body"`
	HTMLURL     string        `json:"html_url"`
	PublishedAt string        `json:"published_at"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	Assets      []githubAsset `json:"assets"`
}

type updateCheckCache struct {
	mu      sync.Mutex
	at      time.Time
	ttl     time.Duration
	source  string
	payload map[string]any
}

var updateCache updateCheckCache

// compareVersions 逐段比较数字版本；预发布标签不参与（发布包只用 x.y.z）。
func compareVersions(a, b string) int {
	parse := func(s string) []int {
		s = strings.TrimPrefix(strings.TrimSpace(s), "v")
		parts := strings.FieldsFunc(s, func(r rune) bool { return r == '.' || r == '-' || r == '+' })
		out := make([]int, 0, len(parts))
		for _, p := range parts {
			n, _ := strconv.Atoi(p)
			out = append(out, n)
		}
		return out
	}
	av, bv := parse(a), parse(b)
	for i := 0; i < len(av) || i < len(bv); i++ {
		var x, y int
		if i < len(av) {
			x = av[i]
		}
		if i < len(bv) {
			y = bv[i]
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

// updateSource 返回 GitHub 仓库/令牌/API 地址：设置优先，其次环境变量。
func (g *Gateway) updateSource() (repo, token, apiBase string) {
	if g != nil {
		g.settingsMu.Lock()
		repo = strings.TrimSpace(g.settings.UpdateRepo)
		token = strings.TrimSpace(g.settings.UpdateToken)
		g.settingsMu.Unlock()
	}
	if repo == "" {
		repo = strings.TrimSpace(os.Getenv("AETHER_GITHUB_REPO"))
	}
	if repo == "" {
		repo = defaultGitHubRepo
	}
	if token == "" {
		token = strings.TrimSpace(os.Getenv("AETHER_GITHUB_TOKEN"))
	}
	apiBase = strings.TrimRight(strings.TrimSpace(os.Getenv("AETHER_GITHUB_API")), "/")
	if apiBase == "" {
		apiBase = defaultGitHubAPI
	}
	return repo, token, apiBase
}

// updateHTTPClient 复用全局代理，避免国内网络下 GitHub 检查必超时。
func (g *Gateway) updateHTTPClient() *http.Client {
	if g != nil && g.tr != nil {
		if c, err := g.tr.NewClient(provider.NetPolicySystem, "", ""); err == nil && c != nil {
			return c
		}
	}
	return http.DefaultClient
}

func tokenFingerprint(token string) string {
	if token == "" {
		return "anon"
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:4])
}

// handleUpdateCheck GET /api/update
func (g *Gateway) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	source := strings.TrimSpace(os.Getenv("AETHER_UPDATE_URL"))
	repo, token, apiBase := g.updateSource()
	cacheKey := "manifest:" + source
	if source == "" {
		cacheKey = "github:" + repo + ":" + apiBase + ":" + tokenFingerprint(token)
	}
	now := time.Now()
	updateCache.mu.Lock()
	if updateCache.payload != nil && updateCache.source == cacheKey && now.Sub(updateCache.at) < updateCache.ttl {
		payload := updateCache.payload
		updateCache.mu.Unlock()
		writeJSON(w, http.StatusOK, payload)
		return
	}
	updateCache.mu.Unlock()

	payload := map[string]any{"current": BuildVersion, "checked_at": now.Unix(), "repo": repo}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	client := g.updateHTTPClient()
	if source != "" {
		g.mergeManifestUpdate(ctx, client, source, payload)
	} else {
		g.mergeGitHubUpdate(ctx, client, repo, token, apiBase, payload)
	}

	ttl := 10 * time.Minute
	if _, ok := payload["latest"]; !ok {
		// 失败/无 Release 的结论缓存短一些，避免用户配好 Token 后还要等 10 分钟。
		ttl = time.Minute
	}
	updateCache.mu.Lock()
	updateCache.at, updateCache.ttl, updateCache.source, updateCache.payload = now, ttl, cacheKey, payload
	updateCache.mu.Unlock()
	writeJSON(w, http.StatusOK, payload)
}

func (g *Gateway) mergeManifestUpdate(ctx context.Context, client *http.Client, source string, payload map[string]any) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		payload["available"] = false
		payload["reason"] = "更新源地址无效: " + err.Error()
		return
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "aether-cockpit-updater")
	resp, err := client.Do(req)
	if err != nil {
		payload["available"] = false
		payload["reason"] = "更新源不可达: " + err.Error()
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		payload["available"] = false
		payload["reason"] = "更新源返回 " + resp.Status
		return
	}
	var m updateManifest
	if err := json.Unmarshal(raw, &m); err != nil || strings.TrimSpace(m.Version) == "" {
		payload["available"] = false
		payload["reason"] = "更新源清单无法解析"
		return
	}
	payload["source"] = "manifest"
	payload["latest"] = strings.TrimPrefix(strings.TrimSpace(m.Version), "v")
	payload["url"] = m.URL
	payload["notes"] = m.Notes
	payload["published_at"] = m.PublishedAt
	payload["sha256"] = m.SHA256
	payload["size"] = m.Size
	payload["available"] = compareVersions(m.Version, BuildVersion) > 0
}

func (g *Gateway) mergeGitHubUpdate(ctx context.Context, client *http.Client, repo, token, apiBase string, payload map[string]any) {
	rel, reason := fetchGitHubRelease(ctx, client, repo, token, apiBase)
	if rel == nil {
		payload["source"] = "github"
		payload["available"] = false
		payload["reason"] = reason
		return
	}
	tag := strings.TrimSpace(rel.TagName)
	latest := strings.TrimPrefix(tag, "v")
	if latest == "" {
		payload["source"] = "github"
		payload["available"] = false
		payload["reason"] = "GitHub Release 缺少 tag_name"
		return
	}
	payload["source"] = "github"
	payload["tag"] = tag
	payload["latest"] = latest
	payload["release_name"] = firstNonEmpty(rel.Name, tag)
	payload["notes"] = rel.Body
	payload["html_url"] = rel.HTMLURL
	payload["published_at"] = rel.PublishedAt
	payload["available"] = compareVersions(latest, BuildVersion) > 0

	asset := pickGitHubAsset(rel.Assets, runtime.GOOS)
	if asset == nil {
		payload["downloadable"] = false
		// 每个平台都要给出「为什么不能直接下」的原文（旧版只有 darwin 有，
		// 桌面端其它平台会拿到 downloadable=false 却没有任何解释）。
		switch runtime.GOOS {
		case "darwin":
			payload["reason"] = "当前 Release 没有 macOS 安装包（.zip/.dmg）"
		case "windows":
			payload["reason"] = "当前 Release 没有 Windows 安装包（.zip/.exe）"
		case "linux":
			payload["reason"] = "当前 Release 没有 Linux 安装包（.zip）"
		default:
			payload["reason"] = "当前 Release 没有本平台安装包"
		}
		return
	}
	payload["downloadable"] = true
	payload["asset_name"] = asset.Name
	payload["size"] = asset.Size
	payload["url"] = fmt.Sprintf("%s/repos/%s/releases/assets/%d", apiBase, repo, asset.ID)
	payload["github"] = true
	if digest := strings.TrimSpace(asset.Digest); strings.HasPrefix(strings.ToLower(digest), "sha256:") {
		payload["sha256"] = strings.TrimPrefix(strings.ToLower(digest), "sha256:")
	}
}

// fetchGitHubRelease 依次尝试 latest 与 releases 列表；两者都 404 时给出
// 与“仓库不可见/没有 Release”一致的提示。
func fetchGitHubRelease(ctx context.Context, client *http.Client, repo, token, apiBase string) (*githubRelease, string) {
	repo = strings.TrimSpace(repo)
	if !validGitHubRepo(repo) {
		return nil, "GitHub 仓库格式应为 owner/repo"
	}
	get := func(apiPath string) (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+apiPath, nil)
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "aether-cockpit-updater")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, body, nil
	}

	status, body, err := get("/repos/" + repo + "/releases/latest")
	if err != nil {
		return nil, "GitHub 不可达: " + err.Error()
	}
	if status == http.StatusOK {
		var rel githubRelease
		if err := json.Unmarshal(body, &rel); err != nil {
			return nil, "GitHub Release 响应无法解析"
		}
		return &rel, ""
	}

	// latest 只返回非预发布 Release；没有时用列表兜底（也覆盖“仓库暂无 Release”）。
	status2, body2, err2 := get("/repos/" + repo + "/releases?per_page=5")
	if err2 != nil {
		return nil, "GitHub 不可达: " + err2.Error()
	}
	if status2 == http.StatusOK {
		var list []githubRelease
		if err := json.Unmarshal(body2, &list); err != nil {
			return nil, "GitHub Releases 响应无法解析"
		}
		for i := range list {
			if !list[i].Draft {
				return &list[i], ""
			}
		}
		return nil, "GitHub 仓库暂无 Release"
	}
	switch {
	case status2 == http.StatusNotFound || status == http.StatusNotFound:
		return nil, "GitHub 仓库不可见或暂无 Release（私有仓库请在关于页配置 Token）"
	case status2 == http.StatusUnauthorized || status2 == http.StatusForbidden:
		return nil, fmt.Sprintf("GitHub 鉴权失败（%d）；私有仓库请在关于页配置 Token，或检查 API 限流", status2)
	default:
		return nil, fmt.Sprintf("GitHub 返回 %d: %s", status2, textSnippet(body2))
	}
}

// pickGitHubAsset 按目标平台挑安装包。不能把 Windows zip 当成 macOS 更新：
// 下载入口只负责校验和保存，用户拿到错误平台包才会真正踩坑。
func pickGitHubAsset(assets []githubAsset, goos string) *githubAsset {
	contains := func(name string, parts ...string) bool {
		for _, p := range parts {
			if strings.Contains(name, p) {
				return true
			}
		}
		return false
	}
	zipFirst, zipAny, exe := -1, -1, -1
	for i := range assets {
		name := strings.ToLower(assets[i].Name)
		switch {
		case strings.HasSuffix(name, ".zip"):
			match := false
			switch goos {
			case "darwin":
				match = contains(name, "macos", "darwin", "osx", "universal")
			case "windows":
				match = contains(name, "windows", "win-", "amd64", "x64")
			default:
				match = true
			}
			if match && zipFirst < 0 {
				zipFirst = i
			}
			if zipAny < 0 {
				zipAny = i
			}
		case goos == "windows" && strings.HasSuffix(name, ".exe"):
			if exe < 0 {
				exe = i
			}
		}
	}
	switch {
	case zipFirst >= 0:
		return &assets[zipFirst]
	case goos == "" && zipAny >= 0:
		return &assets[zipAny]
	case exe >= 0:
		return &assets[exe]
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func textSnippet(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// githubAssetHostAllowed 只允许下载与配置 API 源同源的资产地址。
func (g *Gateway) githubAssetHostAllowed(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return false
	}
	_, _, apiBase := g.updateSource()
	base, err := url.Parse(apiBase)
	if err != nil || base.Host == "" {
		return false
	}
	return strings.EqualFold(u.Scheme, base.Scheme) && strings.EqualFold(u.Host, base.Host)
}

// handleUpdateDownload POST /api/update/download
//
// 请求体：{"url":"https://.../aether-cockpit-x.zip","sha256":"...","name":"可选文件名","github":true}
// 下载到下载目录（AETHER_DOWNLOAD_DIR 可覆盖），校验 SHA256 后保留；
// 校验失败删除临时文件并如实报错。只做“下载 + 校验”，不自动替换正在运行的 exe。
func (g *Gateway) handleUpdateDownload(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
		Name   string `json:"name"`
		GitHub bool   `json:"github"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	rawURL := strings.TrimSpace(body.URL)
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "下载地址必须是 http/https"})
		return
	}
	if u, err := url.Parse(rawURL); err != nil || u.Host == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "下载地址无效"})
		return
	}
	name := sanitizeDownloadName(body.Name, rawURL)
	dir := apppaths.DownloadDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "无法创建下载目录: " + err.Error()})
		return
	}
	target := filepath.Join(dir, name)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if body.GitHub {
		// 令牌只发给配置的 GitHub API 源，避免被本地调用方诱导外泄。
		if !g.githubAssetHostAllowed(rawURL) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "GitHub 资产地址与配置的更新源不一致"})
			return
		}
		// Release asset API：Accept octet-stream 才会返回二进制；
		// 私有仓库带 Token。重定向到对象存储后由预签名 URL 完成鉴权。
		req.Header.Set("Accept", "application/octet-stream")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "aether-cockpit-updater")
		if _, token, _ := g.updateSource(); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := g.updateHTTPClient().Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "下载失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "下载源返回 " + resp.Status})
		return
	}

	tmp, err := os.CreateTemp(dir, ".aether-update-*")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "无法创建临时文件: " + err.Error()})
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	const maxBytes = 512 << 20
	hasher := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hasher), io.LimitReader(resp.Body, maxBytes+1))
	cerr := tmp.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "写入下载文件失败: " + err.Error()})
		return
	}
	if n > maxBytes {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "更新包超过 512 MB 限制"})
		return
	}
	sum := hex.EncodeToString(hasher.Sum(nil))
	want := strings.ToLower(strings.TrimSpace(body.SHA256))
	if want != "" && want != sum {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "SHA256 校验失败（期望 " + want + "，实际 " + sum + "）", "sha256": sum,
		})
		return
	}
	_ = os.Remove(target)
	if err := os.Rename(tmpName, target); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存更新包失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "path": target, "sha256": sum, "verified": want != "", "bytes": n,
	})
}

// sanitizeDownloadName 只保留文件名部分，防止路径穿越。
func sanitizeDownloadName(name, rawURL string) string {
	candidate := strings.TrimSpace(name)
	if candidate == "" {
		if u, err := url.Parse(rawURL); err == nil {
			candidate = path.Base(u.Path)
		}
	}
	candidate = filepath.Base(strings.TrimSpace(candidate))
	if candidate == "" || candidate == "." || candidate == string(filepath.Separator) {
		candidate = "aether-update.zip"
	}
	lower := strings.ToLower(candidate)
	if !strings.HasSuffix(lower, ".zip") && !strings.HasSuffix(lower, ".exe") && !strings.HasSuffix(lower, ".msi") {
		candidate += ".zip"
	}
	return candidate
}
