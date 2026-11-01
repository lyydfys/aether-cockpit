package gateway

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/simplecrypt"
)

// 远程 Aether 实例的额度（aether 的 remote quotas）：
// 本机用量页把其它实例的卡片聚在一起看，卡片本身仍是对方实例的真实读数。

const remoteQuotaKeep = 2 * time.Minute

type remoteQuotaEntry struct {
	at    time.Time
	cards []quotaCard
	err   string
}

// remoteQuotaKey 是缓存的稳定键（同一 URL 改名不重复拉取）。
func remoteQuotaKey(src RemoteQuotaSource) string {
	return strings.TrimRight(strings.TrimSpace(src.URL), "/")
}

// remoteQuotaCacheKey 带上名称：改名后立刻重新拉取，不会沿用旧名字的卡片。
func remoteQuotaCacheKey(src RemoteQuotaSource) string {
	return remoteQuotaKey(src) + "\x00" + strings.TrimSpace(src.Name)
}

func remoteProviderID(src RemoteQuotaSource) string {
	name := strings.ToLower(strings.TrimSpace(src.Name))
	if name == "" {
		name = "remote"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "node"
	}
	sum := sha1.Sum([]byte(remoteQuotaKey(src)))
	return "remote-" + slug + "-" + hex.EncodeToString(sum[:3])
}

// handleRemoteQuotas POST /api/settings/remote-quotas
//
// 整表替换远程实例列表；Key 留空表示沿用同 name+url 的旧 Key，
// 新 Key 用本机绑定加密保存，GET /api/settings 不回显。
func (g *Gateway) handleRemoteQuotas(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Sources []struct {
			Name    string `json:"name"`
			URL     string `json:"url"`
			Key     string `json:"key"`
			Enabled bool   `json:"enabled"`
		} `json:"sources"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}

	g.settingsMu.Lock()
	old := make(map[string]RemoteQuotaSource, len(g.settings.RemoteQuotas))
	for _, src := range g.settings.RemoteQuotas {
		old[src.Name+"\x00"+remoteQuotaKey(src)] = src
	}
	out := make([]RemoteQuotaSource, 0, len(in.Sources))
	for _, src := range in.Sources {
		name := strings.TrimSpace(src.Name)
		rawURL := strings.TrimRight(strings.TrimSpace(src.URL), "/")
		if name == "" {
			name = "远程实例"
		}
		if src.Enabled {
			u, err := url.Parse(rawURL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				g.settingsMu.Unlock()
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "远程地址不是有效的 http(s) URL：" + rawURL})
				return
			}
		}
		entry := RemoteQuotaSource{Name: name, URL: rawURL, Enabled: src.Enabled}
		if key := strings.TrimSpace(src.Key); key != "" {
			enc, err := simplecrypt.Encrypt([]byte(key))
			if err != nil {
				g.settingsMu.Unlock()
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "加密远程密钥失败"})
				return
			}
			entry.KeyEnc = base64.StdEncoding.EncodeToString(enc)
		} else if prev, ok := old[name+"\x00"+rawURL]; ok {
			entry.KeyEnc = prev.KeyEnc
		}
		out = append(out, entry)
	}
	g.settings.RemoteQuotas = out
	err := g.saveSettings()
	g.settingsMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": maskedRemoteQuotaView(out)})
}

func maskedRemoteQuotaView(srcs []RemoteQuotaSource) []map[string]any {
	out := make([]map[string]any, 0, len(srcs))
	for _, src := range srcs {
		out = append(out, map[string]any{
			"name": src.Name, "url": src.URL, "enabled": src.Enabled,
			"hasKey": src.KeyEnc != "",
		})
	}
	return out
}

// handleRemoteQuotaTest POST /api/settings/remote-quotas/test
//
// 在保存前验证地址与密钥：请求对方的 /api/health，返回渠道/账号数。
func (g *Gateway) handleRemoteQuotaTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		URL  string `json:"url"`
		Key  string `json:"key"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in)
	base := strings.TrimRight(strings.TrimSpace(in.URL), "/")
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "远程地址不是有效的 http(s) URL"})
		return
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		// 没输入新 Key 时用已保存的（按 name+url 匹配）。
		g.settingsMu.Lock()
		for _, src := range g.settings.RemoteQuotas {
			if remoteQuotaKey(src) == base && (strings.TrimSpace(in.Name) == "" || src.Name == in.Name) {
				key = g.remoteQuotaKeyPlain(src)
				break
			}
		}
		g.settingsMu.Unlock()
	}

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/health", nil)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "连不上远程实例：" + err.Error()})
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 160 {
			msg = msg[:160]
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "远程实例返回 HTTP " + strconv.Itoa(resp.StatusCode) + "：" + msg})
		return
	}
	var health struct {
		Accounts  int    `json:"accounts"`
		Providers int    `json:"providers"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(raw, &health); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "远程健康检查回复不是 JSON"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "accounts": health.Accounts, "providers": health.Providers, "status": health.Status,
	})
}

// handleRemoteQuotaUseAsProvider POST /api/settings/remote-quotas/use-provider
//
// 把一台已配置的远程实例加成 OpenAI 兼容上游：base {url}/v1 + 该实例的
// 访问 Key（调用方密钥），之后可用 openai-compat/<模型> 走对方的网关。
func (g *Gateway) handleRemoteQuotaUseAsProvider(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		URL      string `json:"url"`
		Protocol string `json:"protocol"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in)
	name := strings.TrimSpace(in.Name)
	rawURL := strings.TrimRight(strings.TrimSpace(in.URL), "/")
	protocol := strings.ToLower(strings.TrimSpace(in.Protocol))

	g.settingsMu.Lock()
	var src *RemoteQuotaSource
	for i := range g.settings.RemoteQuotas {
		s := &g.settings.RemoteQuotas[i]
		if remoteQuotaKey(*s) == rawURL && (name == "" || s.Name == name) {
			src = s
			break
		}
	}
	g.settingsMu.Unlock()
	if src == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有找到这个远程实例，请先保存"})
		return
	}
	key := g.remoteQuotaKeyPlain(*src)
	if key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "该远程实例没有保存访问 Key；加上 Key 后再试"})
		return
	}
	providerID, creds := remoteProviderFor(protocol, remoteQuotaKey(*src), key)
	base := creds["base_url"]
	// 已存在同一 base 的账号：不重复创建。
	for _, e := range g.pool.ListByProvider(providerID) {
		if strings.TrimRight(strings.TrimSpace(e.GetCredential("base_url")), "/") == base {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "created": false, "account": e.Snapshot()})
			return
		}
	}
	label := strings.TrimSpace(src.Name)
	if label == "" {
		label = "远程 Aether"
	}
	env, apiErr := g.createAccount(providerID, label, creds, "")
	if apiErr != nil {
		writeJSON(w, apiErr.StatusCode(), map[string]any{"error": apiErr.Message})
		return
	}
	g.saveAccountsNow()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "created": true, "account": env.Snapshot()})
}

// remoteProviderFor 把远程实例协议映射成内核渠道与账号凭证。
//   - 默认 OpenAI 兼容：base 带 /v1，Key 走 api_key
//   - responses：codex，base 带 /v1，Key 走 access_token
//   - anthropic：claude-sub，base 是根地址（/v1/messages），Key 走 access_token
//   - gemini：gemini-compat，base 是根地址（自动补 /v1beta），Key 走 api_key
func remoteProviderFor(protocol, root, key string) (string, map[string]string) {
	root = strings.TrimRight(strings.TrimSpace(root), "/")
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "responses":
		return "codex", map[string]string{"base_url": root + "/v1", "access_token": key}
	case "anthropic":
		return "claude-sub", map[string]string{"base_url": root, "access_token": key}
	case "gemini":
		return "gemini-compat", map[string]string{"base_url": root, "api_key": key}
	default:
		return "openai-compat", map[string]string{"base_url": root + "/v1", "api_key": key}
	}
}

// remoteQuotaCards 返回缓存的远程卡片；过期时后台刷新（reading=true 让前端稍后重拉）。
func (g *Gateway) remoteQuotaCards(fresh bool) ([]quotaCard, bool) {
	g.settingsMu.Lock()
	sources := append([]RemoteQuotaSource(nil), g.settings.RemoteQuotas...)
	g.settingsMu.Unlock()
	if len(sources) == 0 {
		return nil, false
	}

	reading := false
	out := make([]quotaCard, 0, len(sources)*2)
	now := time.Now()
	g.remoteMu.Lock()
	if g.remoteCache == nil {
		g.remoteCache = map[string]remoteQuotaEntry{}
	}
	if g.remoteBusy == nil {
		g.remoteBusy = map[string]bool{}
	}
	g.remoteMu.Unlock()
	for _, src := range sources {
		if !src.Enabled || remoteQuotaKey(src) == "" {
			continue
		}
		key := remoteQuotaCacheKey(src)

		g.remoteMu.Lock()
		entry, has := g.remoteCache[key]
		busy := g.remoteBusy[key]
		stale := !has || now.Sub(entry.at) > remoteQuotaKeep
		appendEntry := func(e remoteQuotaEntry) {
			if e.err != "" {
				out = append(out, remoteErrorCard(src, e.err))
				return
			}
			out = append(out, e.cards...)
		}
		if !fresh && !stale {
			g.remoteMu.Unlock()
			appendEntry(entry)
			continue
		}
		if has {
			// 先给旧读数，后台刷新。
			appendEntry(entry)
		}
		if busy {
			g.remoteMu.Unlock()
			reading = true
			continue
		}
		g.remoteBusy[key] = true
		g.remoteMu.Unlock()
		reading = true

		go func(src RemoteQuotaSource) {
			cards, errMsg := g.fetchRemoteQuotaCards(src)
			g.remoteMu.Lock()
			g.remoteCache[remoteQuotaCacheKey(src)] = remoteQuotaEntry{at: time.Now(), cards: cards, err: errMsg}
			delete(g.remoteBusy, remoteQuotaCacheKey(src))
			g.remoteMu.Unlock()
		}(src)
	}
	return out, reading
}

// fetchRemoteQuotaCards 拉一台远程实例的卡片并改写展示身份。
func (g *Gateway) fetchRemoteQuotaCards(src RemoteQuotaSource) ([]quotaCard, string) {
	base := remoteQuotaKey(src)
	if base == "" {
		return nil, "远程地址为空"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/usage/quotas", nil)
	if err != nil {
		return nil, err.Error()
	}
	if key := g.remoteQuotaKeyPlain(src); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "AetherCockpit/remote-quota")

	// 远程实例一般在内网；不走上游代理，避免把局域网地址交给代理。
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "连不上远程实例：" + err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 160 {
			msg = msg[:160]
		}
		return nil, "远程实例返回 HTTP " + strconv.Itoa(resp.StatusCode) + "：" + msg
	}
	var cards []quotaCard
	if err := json.Unmarshal(raw, &cards); err != nil {
		return nil, "远程回复不是卡片数组：" + err.Error()
	}

	provider := remoteProviderID(src)
	out := make([]quotaCard, 0, len(cards))
	for _, c := range cards {
		// 不再继续转发对方聚合的远程卡，避免多实例互指时无限套娃。
		if strings.HasPrefix(c.Provider, "remote-") {
			continue
		}
		c.Provider = provider
		c.Name = src.Name + " · " + c.Name
		if c.Icon == "" {
			c.Icon = "generic"
		}
		// 远程卡片只读：签到/刷新等动作由对方实例自己处理。
		c.Checkins = false
		c.Checkin = nil
		c.CheckinBy = ""
		c.CheckinOff = false
		out = append(out, c)
	}
	return out, ""
}

// remoteErrorCard 让不可达的远程实例以错误卡出现，而不是静默消失。
func remoteErrorCard(src RemoteQuotaSource, msg string) quotaCard {
	return quotaCard{
		Provider: remoteProviderID(src),
		Name:     src.Name,
		Icon:     "generic",
		Windows:  []quotaCardWindow{},
		Error:    msg,
	}
}

func (g *Gateway) remoteQuotaKeyPlain(src RemoteQuotaSource) string {
	if src.KeyEnc == "" {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(src.KeyEnc)
	if err != nil {
		return ""
	}
	plain, err := simplecrypt.Decrypt(raw)
	if err != nil {
		return ""
	}
	return string(plain)
}
