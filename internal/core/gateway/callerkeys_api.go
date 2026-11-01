package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/provider"
)

// 调用方网关密钥的管理 API（对齐 aether 的 /api/caller-keys/*）。

func (g *Gateway) callerKeyView() []map[string]any {
	out := []map[string]any{}
	if g.callerKeys == nil {
		return out
	}
	g.callerKeys.mu.Lock()
	keys := append([]callerKey(nil), g.callerKeys.keys...)
	g.callerKeys.mu.Unlock()
	for _, k := range keys {
		masked := ""
		if secret := decryptCallerSecret(k.SecretEnc); secret != "" {
			masked = maskCallerSecret(secret)
		}
		tokens, cost := g.callerKeyUsage(k.ID, k.Limit)
		out = append(out, map[string]any{
			"id": k.ID, "name": k.Name, "masked": masked,
			"off": k.Off, "limit": k.Limit, "models": k.Models,
			"createdAt": k.CreatedAt,
			"used":      map[string]any{"tokens": tokens, "cost": cost},
		})
	}
	return out
}

func (g *Gateway) callerKeysPayload() map[string]any {
	names := []string{}
	if g.pool != nil {
		for _, e := range g.pool.All() {
			if e.Label != "" {
				names = append(names, e.Label)
			}
		}
	}
	return map[string]any{"keys": g.callerKeyView(), "accountNames": names}
}

// handleCallerKeys GET /api/caller-keys
func (g *Gateway) handleCallerKeys(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.callerKeysPayload())
}

// handleCallerKeysModels GET /api/caller-keys/models —— 模型白名单选择器的数据。
func (g *Gateway) handleCallerKeysModels(w http.ResponseWriter, r *http.Request) {
	models := []map[string]any{}
	seen := map[string]bool{}
	for _, m := range provider.AllManifests() {
		for _, mm := range g.catalogModels(m.ID) {
			if seen[mm.ID] {
				continue
			}
			seen[mm.ID] = true
			models = append(models, map[string]any{"id": mm.ID, "name": mm.DisplayName, "provider": m.ID})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models, "accounts": []any{}})
}

// handleCallerKeysAction POST /api/caller-keys/{action}
func (g *Gateway) handleCallerKeysAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	var body struct {
		Key    string          `json:"key"`
		Name   string          `json:"name"`
		Secret string          `json:"secret"`
		Limit  *callerKeyLimit `json:"limit"`
		Models []string        `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	if g.callerKeys == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "密钥存储不可用"})
		return
	}
	fail := func(status int, msg string) {
		writeJSON(w, status, map[string]any{"error": msg})
	}
	var secret string

	switch action {
	case "add-key":
		name := strings.TrimSpace(body.Name)
		if own := strings.TrimSpace(body.Secret); own != "" {
			// 用户自带密钥：至少 16 字符，避免弱口令。
			if len(own) < 16 {
				fail(http.StatusBadRequest, "自带的密钥至少 16 个字符")
				return
			}
			enc, err := encryptCallerSecret(own)
			if err != nil {
				fail(http.StatusInternalServerError, "保存失败: "+err.Error())
				return
			}
			k := callerKey{ID: "ck_" + shortHash(own), Name: name, SecretEnc: enc, CreatedAt: time.Now().Unix()}
			g.callerKeys.mu.Lock()
			g.callerKeys.keys = append(g.callerKeys.keys, k)
			g.callerKeys.reindex()
			_ = g.callerKeys.saveLocked()
			g.callerKeys.mu.Unlock()
			secret = own
		} else {
			_, secret = g.callerKeys.add(name)
		}
	case "on-key", "off-key":
		off := action == "off-key"
		if !g.callerKeys.update(body.Key, func(k *callerKey) bool { k.Off = off; return true }) {
			fail(http.StatusNotFound, "密钥不存在")
			return
		}
	case "remove":
		if !g.callerKeys.remove(body.Key) {
			fail(http.StatusNotFound, "密钥不存在")
			return
		}
		g.callerKeys.resetRPM(body.Key)
	case "rename-key":
		name := strings.TrimSpace(body.Name)
		if !g.callerKeys.update(body.Key, func(k *callerKey) bool { k.Name = name; return true }) {
			fail(http.StatusNotFound, "密钥不存在")
			return
		}
	case "rotate-key":
		s, ok := g.callerKeys.rotate(body.Key)
		if !ok {
			fail(http.StatusNotFound, "密钥不存在")
			return
		}
		secret = s
	case "copy-key":
		s, ok := g.callerKeys.reveal(body.Key)
		if !ok {
			fail(http.StatusNotFound, "密钥不存在")
			return
		}
		secret = s
	case "limit-key":
		if !g.callerKeys.update(body.Key, func(k *callerKey) bool {
			if body.Limit == nil || (body.Limit.Tokens == 0 && body.Limit.Cost == 0 && body.Limit.RPM == 0) {
				k.Limit = nil
			} else {
				cp := *body.Limit
				k.Limit = &cp
			}
			return true
		}) {
			fail(http.StatusNotFound, "密钥不存在")
			return
		}
	case "reset-limit-key":
		if !g.callerKeys.update(body.Key, func(k *callerKey) bool { k.Limit = nil; return true }) {
			fail(http.StatusNotFound, "密钥不存在")
			return
		}
		g.callerKeys.resetRPM(body.Key)
	case "models-key":
		if !g.callerKeys.update(body.Key, func(k *callerKey) bool {
			k.Models = append([]string(nil), body.Models...)
			return true
		}) {
			fail(http.StatusNotFound, "密钥不存在")
			return
		}
	case "accounts-key", "quotas-key":
		// 本项目按账号路由由渠道设置决定；接受该动作但不改变行为。
	default:
		fail(http.StatusNotFound, "未知动作: "+action)
		return
	}

	payload := g.callerKeysPayload()
	if secret != "" {
		payload["secret"] = secret
	}
	writeJSON(w, http.StatusOK, payload)
}
