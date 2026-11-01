package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"aether-cockpit/internal/provider"
)

// 自定义供应商图标：上传一张图、或从站点抓 favicon，存到配置目录的 icons/，
// 供应商条目以 "file:<文件名>" 引用。/api/icons/<name> 按文件提供。
//
// 只接受真正的图片（按魔数判断，不信 Content-Type），上限 256KB，
// 文件名是内容的哈希 + 嗅探出的扩展名，不接受调用方给的路径。

const iconMaxBytes = 256 << 10

var iconNameRe = regexp.MustCompile(`^[a-f0-9]{16,64}\.(png|jpg|gif|webp|svg)$`)

func (g *Gateway) iconsDir() string { return filepath.Join(g.configDir(), "icons") }

// sniffIconExt 按文件头判断图片类型；不是图片返回 ""。
func sniffIconExt(b []byte) string {
	head := b
	if len(head) > 512 {
		head = head[:512]
	}
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return ".png"
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return ".jpg"
	case bytes.HasPrefix(b, []byte("GIF8")):
		return ".gif"
	case len(b) > 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return ".webp"
	case bytes.Contains(head, []byte("<svg")):
		return ".svg"
	}
	return ""
}

// saveIcon 落盘并返回 "file:<name>" 引用。
func (g *Gateway) saveIcon(data []byte) (string, error) {
	ext := sniffIconExt(data)
	if ext == "" {
		return "", errString("不是可识别的图片（png/jpg/gif/webp/svg）")
	}
	if len(data) > iconMaxBytes {
		return "", errString("图片太大（上限 256KB）")
	}
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:8]) + ext
	dir := g.iconsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp := filepath.Join(dir, name+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return "", err
	}
	return "file:" + name, nil
}

// handleIcons POST /api/icons {data: base64, name?} → {icon:"file:<name>"}
func (g *Gateway) handleIcons(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Data string `json:"data"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body.Data))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "图片数据不是 base64"})
		return
	}
	icon, err := g.saveIcon(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"icon": icon})
}

// handleIconGet GET /api/icons/{name}
func (g *Gateway) handleIconGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !iconNameRe.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(filepath.Join(g.iconsDir(), name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := "application/octet-stream"
	switch filepath.Ext(name) {
	case ".png":
		ct = "image/png"
	case ".jpg":
		ct = "image/jpeg"
	case ".gif":
		ct = "image/gif"
	case ".webp":
		ct = "image/webp"
	case ".svg":
		ct = "image/svg+xml"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "max-age=86400")
	_, _ = w.Write(data)
}

// handleIconFavicon POST /api/icons/favicon {url,name?}
//
// 先按原地址抓一次（可能直接指向图标），失败再试站点根目录的
// favicon.ico / favicon.png。走网关的网络策略（有全局代理就用）。
func (g *Gateway) handleIconFavicon(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	u, err := url.Parse(strings.TrimSpace(body.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "需要一个 http(s) 地址"})
		return
	}
	client, _ := g.tr.NewClient(provider.NetPolicyProxyFirst, "", "")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	origin := u.Scheme + "://" + u.Host
	candidates := []string{origin + "/favicon.ico", origin + "/favicon.png"}
	if strings.HasSuffix(strings.ToLower(u.Path), ".ico") || strings.HasSuffix(strings.ToLower(u.Path), ".png") || strings.HasSuffix(strings.ToLower(u.Path), ".svg") {
		candidates = append([]string{u.String()}, candidates...)
	}
	// 很多站点不把图标放在根目录：先读首页的 <link rel="icon">。
	if links := g.iconLinksFromPage(ctx, client, u); len(links) > 0 {
		candidates = append(links, candidates...)
	}
	seen := map[string]bool{}
	unique := candidates[:0]
	for _, c := range candidates {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		unique = append(unique, c)
	}
	candidates = unique
	var lastErr string
	for _, c := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, iconMaxBytes+1))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || len(raw) == 0 || len(raw) > iconMaxBytes {
			lastErr = "HTTP " + resp.Status
			continue
		}
		icon, err := g.saveIcon(raw)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		writeJSON(w, http.StatusOK, map[string]any{"icon": icon})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]any{"error": "没有找到可用的图标" + suffixErr(lastErr)})
}

// iconLinksFromPage 读站点首页（或给定页面）的 <link rel="icon">。
// 只读前 64KB；解析失败就返回空，不影响后续的根目录回退。
func (g *Gateway) iconLinksFromPage(ctx context.Context, client *http.Client, u *url.URL) []string {
	page := *u
	page.Path, page.RawQuery, page.Fragment = "/", "", ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, page.String(), nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "AetherCockpit/icon")
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	html := string(raw)
	var out []string
	for _, m := range iconLinkRe.FindAllStringSubmatch(html, -1) {
		href := strings.TrimSpace(m[1])
		if href == "" || strings.HasPrefix(href, "data:") {
			continue
		}
		ref, err := url.Parse(href)
		if err != nil {
			continue
		}
		out = append(out, page.ResolveReference(ref).String())
		if len(out) >= 4 {
			break
		}
	}
	return out
}

var iconLinkRe = regexp.MustCompile(`(?i)<link[^>]+rel=["']?[^"'>]*icon[^"'>]*["']?[^>]*?href=["']([^"']+)["']`)

func suffixErr(s string) string {
	if s == "" {
		return ""
	}
	return "：" + s
}

/* ---------------- providers 的 icon 偏好 ---------------- */

// providerIcon 返回某渠道当前的图标引用（空 = 用内置图标）。
func (g *Gateway) providerIcon(providerID string) string {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if m, ok := g.settings.UI["providerIcons"].(map[string]any); ok {
		if s, ok := m[providerID].(string); ok {
			return s
		}
	}
	return ""
}

// setProviderIcon 保存渠道图标引用（调用方持有 settingsMu）。
func (g *Gateway) setProviderIconLocked(providerID, icon string) {
	if g.settings.UI == nil {
		g.settings.UI = map[string]any{}
	}
	m, ok := g.settings.UI["providerIcons"].(map[string]any)
	if !ok {
		m = map[string]any{}
		g.settings.UI["providerIcons"] = m
	}
	if strings.TrimSpace(icon) == "" {
		delete(m, providerID)
	} else {
		m[providerID] = icon
	}
}

// handleIcon GET /icons/{file} —— 静态品牌图缺失时的兜底：
// 按渠道 brand_color 生成首字母贴片（SVG），文件名形如 "antigravity.svg"。
func (g *Gateway) handleIcon(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	base := strings.TrimSuffix(file, filepath.Ext(file))
	// 前端对 "-color" 图标走 <img>（其余走 CSS mask，只看 alpha）：
	// 去掉后缀再按渠道查品牌色，生成带底色的贴片。
	base = strings.TrimSuffix(strings.TrimSpace(base), "-color")
	if base == "" || strings.ContainsAny(base, `/\`) {
		http.NotFound(w, r)
		return
	}
	name, color, letter := "", "", ""
	if p, ok := provider.Get(base); ok {
		m := p.Manifest()
		name, color = m.DisplayName, m.BrandColor
	} else {
		// 客户端（claude_code 等）与自定义名字没有渠道记录：
		// 用名字的哈希从调色板取色，首字母做贴片，避免页面出现空图标。
		name = base
		color = "#6b7280"
		pal := []string{"#2563eb", "#7c3aed", "#db2777", "#dc2626", "#ea580c", "#16a34a", "#0891b2", "#4f46e5"}
		h := 0
		for _, r := range base {
			h = (h*131 + int(r)) & 0x7fffffff
		}
		color = pal[h%len(pal)]
	}
	if color == "" {
		color = "#6b7280"
	}
	letter = "?"
	if rs := []rune(strings.ToUpper(name)); len(rs) > 0 {
		letter = string(rs[0])
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64" viewBox="0 0 64 64">` +
		`<rect width="64" height="64" rx="14" fill="` + color + `"/>` +
		`<text x="32" y="42" font-family="system-ui,Segoe UI,Arial" font-size="30" font-weight="600" fill="#fff" text-anchor="middle">` + letter + `</text></svg>`
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "max-age=86400")
	_, _ = io.WriteString(w, svg)
}

// backupIcons 收集设置里引用的图标文件内容（供备份/同步带走）。
func (g *Gateway) backupIcons() map[string]string {
	g.settingsMu.Lock()
	refs := map[string]string{}
	if m, ok := g.settings.UI["providerIcons"].(map[string]any); ok {
		for _, v := range m {
			if s, ok := v.(string); ok && strings.HasPrefix(s, "file:") {
				refs[strings.TrimPrefix(s, "file:")] = s
			}
		}
	}
	g.settingsMu.Unlock()
	if len(refs) == 0 {
		return nil
	}
	out := make(map[string]string, len(refs))
	for name := range refs {
		if !iconNameRe.MatchString(name) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(g.iconsDir(), name))
		if err != nil || len(raw) > iconMaxBytes {
			continue
		}
		out[name] = base64.StdEncoding.EncodeToString(raw)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// handleImportIcon POST /api/import/icon {url} → {icon}
//
// 编辑器里"从图标地址导入"：直接抓给定 URL（必须是图片），不走 favicon 回落。
func (g *Gateway) handleImportIcon(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	u, err := url.Parse(strings.TrimSpace(body.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "需要一个 http(s) 图标地址"})
		return
	}
	client, _ := g.tr.NewClient(provider.NetPolicyProxyFirst, "", "")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	req.Header.Set("User-Agent", "AetherCockpit/icon")
	resp, err := client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "抓取失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, iconMaxBytes+1))
	if resp.StatusCode != http.StatusOK || len(raw) == 0 || len(raw) > iconMaxBytes {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "图标地址返回 " + resp.Status + " 或体积超限"})
		return
	}
	icon, err := g.saveIcon(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"icon": icon})
}
