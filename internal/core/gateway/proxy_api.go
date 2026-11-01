package gateway

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
)

// 全局代理的运行中切换。
//
// 账号的 http.Client 是各自独占的（连接池与指纹隔离），所以"换代理"不是改
// 一个全局变量，而是用新工厂给每个账号重新铸造客户端，全部建好后再统一替换；
// 任何一个失败都保持旧客户端，避免一半账号走新出口。
// 代理地址里可能带密码：GET /api/settings 只回显打码后的 URL。

const proxyPasswordMask = "******"

func maskProxyURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	if _, has := u.User.Password(); !has {
		return raw
	}
	// 手工拼 ******：url.UserPassword 会把 * 转义成 %2A，回显不直观。
	rest := u.EscapedPath()
	if u.RawQuery != "" {
		rest += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		rest += "#" + u.Fragment
	}
	return u.Scheme + "://" + url.User(u.User.Username()).String() + ":" + proxyPasswordMask + "@" + u.Host + rest
}

// resolveProxyInput 处理"提交的是打码值"的情况：密码沿用旧值。
func resolveProxyInput(raw, current string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	pass, has := u.User.Password()
	if !has || pass != proxyPasswordMask {
		return raw
	}
	cu, err := url.Parse(current)
	if err != nil || cu.User == nil {
		return raw
	}
	oldPass, hasOld := cu.User.Password()
	if !hasOld {
		return raw
	}
	u.User = url.UserPassword(u.User.Username(), oldPass)
	return u.String()
}

// applyGlobalProxy 切换代理并返回打码后的地址。
func (g *Gateway) applyGlobalProxy(raw string) (string, error) {
	current := g.tr.GlobalProxyValue()
	raw = strings.TrimSpace(raw)
	if raw != "" && raw == maskProxyURL(current) {
		return maskProxyURL(current), nil // 前端回显的打码值，未改动
	}
	next := resolveProxyInput(raw, current)

	if err := g.tr.SetGlobalProxy(next); err != nil {
		return "", err
	}
	if g.pool != nil {
		if _, err := g.pool.RebuildClients(g.tr); err != nil {
			// 回滚工厂；客户端未被替换（RebuildClients 先建后换）。
			_ = g.tr.SetGlobalProxy(current)
			return "", err
		}
	}

	g.settingsMu.Lock()
	g.settings.GlobalProxy = next
	saveErr := g.saveSettings()
	g.settingsMu.Unlock()
	if saveErr != nil {
		return "", saveErr
	}
	log.Printf("[INFO] 全局代理已切换为 %q（所有账号客户端已重建）", maskProxyURL(next))
	return maskProxyURL(next), nil
}

// handleProxySetting POST /api/settings/proxy {proxy}
// 也接受 {"value": ...}；空串表示关闭全局代理。
func (g *Gateway) handleProxySetting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Proxy *string `json:"proxy"`
		Value *string `json:"value"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败"})
		return
	}
	var val string
	switch {
	case body.Proxy != nil:
		val = *body.Proxy
	case body.Value != nil:
		val = *body.Value
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 proxy"})
		return
	}
	masked, err := g.applyGlobalProxy(val)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	view := g.settingsView()
	view["proxy"] = masked
	writeJSON(w, http.StatusOK, view)
}
