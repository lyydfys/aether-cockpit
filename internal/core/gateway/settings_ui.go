package gateway

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// UI 偏好透传与访问控制（CORS 来源 / IP 白名单）。
//
// aether 设置页把主题、语言、字号、各种开关塞进同一个 POST /api/settings，
// 并把响应当 prefs 本体读取。这里把未被内核结构化字段消费的键原样存在
// settings.ui 下并回显，前端开关才能持久化。

// reservedSettingKeys 是内核结构化处理的字段，不进入 UI 透传。
var reservedSettingKeys = map[string]bool{
	"privacy_mask": true, "model_prices": true, "launch_commands": true,
	"webdav": true, "otlp": true, "tools": true, "sync": true,
	"lan": true, "lan_addr": true, "cors_origins": true, "allow_ips": true, "proxy": true, "global_proxy": true,
	"port": true, "addr": true, "listen_addr": true,
	"profiles": true, "active_profile": true, "model_groups_override": true,
	"ui": true, "settings": true, "update_repo": true, "update_token": true,
}

const uiPrefsMaxBytes = 512 << 10

// applyUIPrefs 把请求体里的非结构化键合并进 settings.ui。调用方持有 settingsMu。
func (g *Gateway) applyUIPrefs(raw []byte) {
	if len(raw) == 0 {
		return
	}
	var all map[string]json.RawMessage
	if json.Unmarshal(raw, &all) != nil {
		return
	}
	if g.settings.UI == nil {
		g.settings.UI = make(map[string]any, len(all))
	}
	total := 0
	for k, v := range all {
		if reservedSettingKeys[k] || strings.TrimSpace(k) == "" {
			continue
		}
		if len(v) > 64<<10 {
			continue
		}
		total += len(v)
		if total > uiPrefsMaxBytes {
			break
		}
		var val any
		if json.Unmarshal(v, &val) == nil {
			g.settings.UI[k] = val
		}
	}
}

// validCORSOrigin 接受 http(s)://host[:port] 与 chrome-extension://id。
func validCORSOrigin(o string) bool {
	u, err := url.Parse(strings.TrimSpace(o))
	if err != nil || u.Scheme == "" {
		return false
	}
	switch u.Scheme {
	case "http", "https":
		return u.Host != ""
	case "chrome-extension", "moz-extension", "safari-web-extension":
		return u.Host != "" || u.Opaque != ""
	}
	return false
}

// validIPEntry 接受单个 IP 或 CIDR。
func validIPEntry(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if _, _, err := net.ParseCIDR(s); err == nil {
		return true
	}
	return net.ParseIP(s) != nil
}

// handleCORSSettings POST /api/settings/cors {origins:[...]}
func (g *Gateway) handleCORSSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Origins []string `json:"origins"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	cleaned := make([]string, 0, len(body.Origins))
	for _, o := range body.Origins {
		o = strings.TrimSpace(o)
		if !validCORSOrigin(o) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "不是合法的网页来源: " + o})
			return
		}
		cleaned = append(cleaned, o)
	}
	g.settingsMu.Lock()
	g.settings.CORSOrigins = cleaned
	err := g.saveSettings()
	g.settingsMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, g.settingsView())
}

// handleAllowIPsSettings POST /api/settings/allowips {ips:[...]}
func (g *Gateway) handleAllowIPsSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IPs []string `json:"ips"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	cleaned := make([]string, 0, len(body.IPs))
	for _, ip := range body.IPs {
		ip = strings.TrimSpace(ip)
		if !validIPEntry(ip) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "不是合法的 IP 或网段: " + ip})
			return
		}
		cleaned = append(cleaned, ip)
	}
	g.settingsMu.Lock()
	g.settings.AllowIPs = cleaned
	err := g.saveSettings()
	g.settingsMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, g.settingsView())
}

// clientIP 取调用方地址：优先转发链的第一跳，其次 CF 头，最后 RemoteAddr。
func clientIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(first)
	}
	if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
		return cf
	}
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		return xr
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

// allowIPAllowed 检查远程来源是否在白名单内（空名单 = 全部允许）。
func (g *Gateway) allowIPAllowed(ip string) bool {
	g.settingsMu.Lock()
	list := append([]string(nil), g.settings.AllowIPs...)
	g.settingsMu.Unlock()
	if len(list) == 0 {
		return true
	}
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return false
	}
	for _, entry := range list {
		if strings.Contains(entry, "/") {
			if _, netw, err := net.ParseCIDR(entry); err == nil && netw.Contains(parsed) {
				return true
			}
			continue
		}
		if other := net.ParseIP(entry); other != nil && other.Equal(parsed) {
			return true
		}
	}
	return false
}

// corsOriginFor 返回应答应使用的 Access-Control-Allow-Origin（空 = 不放行）。
func (g *Gateway) corsOriginFor(origin string) string {
	g.settingsMu.Lock()
	list := append([]string(nil), g.settings.CORSOrigins...)
	g.settingsMu.Unlock()
	if len(list) == 0 {
		return "*"
	}
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return ""
	}
	for _, o := range list {
		if strings.EqualFold(o, origin) {
			return origin
		}
	}
	return ""
}

// uiSettingKeys 是设置页里逐个动作保存、且内核能正确回显的偏好键。
// 值 = 前端读取的 prefs 字段名。
var uiSettingKeys = map[string]string{
	"text-size":     "textSize",
	"quota-left":    "quotaLeft",
	"quota-reads":   "quotaReads",
	"plain-names":   "plainNames",
	"gateway-mode":  "gatewayMode",
	"update-mirror": "updateMirror",
	"update-skip":   "updateSkip",
	// 每日签到开关（设置页 / 用量卡片）。只影响 cron 自动签到，
	// 手动「立即签到」始终可用。
	"workbuddy-checkin": "workbuddyCheckin",
	"trae-checkin":      "traeCheckin",
	"qoder-checkin":     "qoderCheckin",
	"minimax-checkin":   "minimaxCheckin",
}

// uiSettingRefusals 是不适用/不支持的设置项：如实说明，不假装保存成功。
var uiSettingRefusals = map[string]string{
	"disabled-models":     "模型级屏蔽请在渠道编辑器的模型列表里操作",
	"full-context":        "这是上游桌面壳的长对话策略，本项目未启用",
	"account-discovery":   "本机登录态发现由「导入本地配置」统一处理，无需单独开关",
	"checkin-account":     "签到账号在渠道页的账号列表里逐项管理",
	"archive":             "会话归档由会话页管理",
	"login":               "开机自启由桌面壳负责，本项目未实现",
	"guide":               "本项目的引导由界面直接提供，无需记录",
	"github-token":        "本项目的资源库不访问 GitHub，无需令牌",
	"desktop-longest":     "桌面窗口尺寸由桌面壳负责",
	"plugin-checkin":      "本项目没有插件签到",
	"plugin-option":       "本项目没有插件运行时",
	"search-api":          "搜索服务在设置页的「工具服务」里配置（Tavily/Serper/SearXNG）",
	"search-key":          "搜索服务在设置页的「工具服务」里配置（Tavily/Serper/SearXNG）",
	"codex-agents-v1":     "本项目不代管 Codex 后台服务",
	"codex-async-compact": "本项目不代管 Codex 后台服务",
	"codex-auto-reset":    "本项目不代管 Codex 后台服务",
	"codex-auto-review":   "本项目不代管 Codex 后台服务",
	"codex-credits":       "本项目不代管 Codex 后台服务",
	"codex-descriptions":  "本项目不代管 Codex 后台服务",
	"codex-predictions":   "本项目不代管 Codex 后台服务",
	"codex-titles":        "本项目不代管 Codex 后台服务",
	"codex-warm-at":       "本项目不代管 Codex 后台服务",
}

// handleUISetting POST /api/settings/{key}
//
// 设置页对单个开关/数值直接保存；这里把请求体折成该偏好的值存进
// settings.ui，并返回扁平 prefs。不支持的项返回明确原因（而不是 404）。
func (g *Gateway) handleUISetting(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if reason, refuse := uiSettingRefusals[key]; refuse {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": reason})
		return
	}
	field, ok := uiSettingKeys[key]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "未知设置项: " + key})
		return
	}
	var body map[string]json.RawMessage
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	var val any
	if len(body) == 1 {
		for _, raw := range body {
			if json.Unmarshal(raw, &val) != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "值无法解析"})
				return
			}
		}
	} else {
		v := make(map[string]any, len(body))
		for k, raw := range body {
			var x any
			if json.Unmarshal(raw, &x) == nil {
				v[k] = x
			}
		}
		val = v
	}
	g.settingsMu.Lock()
	if g.settings.UI == nil {
		g.settings.UI = map[string]any{}
	}
	g.settings.UI[field] = val
	err := g.saveSettings()
	g.settingsMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, g.settingsView())
}
