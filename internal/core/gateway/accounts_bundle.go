package gateway

// 加密账号包：把账号凭证与元数据打包成口令加密文件，用于跨机迁移。
//
// 与 /api/backup/export 的区别：后者刻意不含凭证；这里是真正的账号迁移，
// 因此必须强制口令加密，明文只在内存里出现，HTTP 响应只回 base64 密文。

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/provider"
)

const (
	bundleFormat   = "aether-accounts"
	bundleVersion  = 1
	bundleMaxBytes = 8 << 20
	bundleMinPass  = 8
)

type accountBundle struct {
	Format    string          `json:"format"`
	Version   int             `json:"version"`
	CreatedAt int64           `json:"created_at"`
	Accounts  []bundleAccount `json:"accounts"`
}

type bundleAccount struct {
	ProviderID    string            `json:"provider_id"`
	Label         string            `json:"label,omitempty"`
	Credentials   map[string]string `json:"credentials"`
	Tags          []string          `json:"tags,omitempty"`
	Note          string            `json:"note,omitempty"`
	Priority      int               `json:"priority,omitempty"`
	Weight        int               `json:"weight,omitempty"`
	Concurrency   *int              `json:"concurrency,omitempty"`
	AllowedModels []string          `json:"allowed_models,omitempty"`
	ModelMapping  map[string]string `json:"model_mapping,omitempty"`
	Disabled      bool              `json:"disabled,omitempty"`
	TLSProfile    string            `json:"tls_profile,omitempty"`
	ProxyOverride string            `json:"proxy_override,omitempty"`
	CreatedAt     int64             `json:"created_at,omitempty"`
}

func (g *Gateway) handleAccountsBundleExport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Passphrase string `json:"passphrase"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	pass := strings.TrimSpace(body.Passphrase)
	if len(pass) < bundleMinPass {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "口令至少 8 个字符；账号包包含凭证，不允许明文导出"})
		return
	}

	bundle := accountBundle{Format: bundleFormat, Version: bundleVersion, CreatedAt: time.Now().Unix()}
	for _, env := range g.pool.All() {
		if env == nil {
			continue
		}
		snap := env.Snapshot()
		concurrency, ok := env.ConcurrencyOverride()
		var conc *int
		if ok {
			conc = &concurrency
		}
		bundle.Accounts = append(bundle.Accounts, bundleAccount{
			ProviderID:    env.ProviderID,
			Label:         env.Label,
			Credentials:   env.CredentialsSnapshot(),
			Tags:          env.TagsList(),
			Note:          env.Note(),
			Priority:      env.Priority(),
			Weight:        env.Weight(),
			Concurrency:   conc,
			AllowedModels: env.AllowedModels(),
			ModelMapping:  env.ModelMapping(),
			Disabled:      env.Disabled(),
			TLSProfile:    env.TLSProfile,
			ProxyOverride: env.ProxyOverride,
			CreatedAt:     snap.CreatedAt,
		})
	}
	plain, err := json.Marshal(bundle)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "打包失败: " + err.Error()})
		return
	}
	sealed, err := sealBackup(plain, pass)
	if err != nil || !sealedBackup(sealed) {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "加密失败"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"format":    bundleFormat,
		"version":   bundleVersion,
		"accounts":  len(bundle.Accounts),
		"encrypted": true,
		"filename":  "aether-accounts-" + time.Now().Format("20060102-150405") + ".aether-accounts",
		"data":      base64.StdEncoding.EncodeToString(sealed),
	})
}

func (g *Gateway) handleAccountsBundleImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Passphrase string `json:"passphrase"`
		Data       string `json:"data"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, bundleMaxBytes+1))
	if len(raw) > bundleMaxBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "账号包超过 8 MB"})
		return
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	pass := strings.TrimSpace(body.Passphrase)
	if pass == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "需要口令才能解密账号包"})
		return
	}
	sealed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body.Data))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "账号包不是合法的 base64"})
		return
	}
	if !sealedBackup(sealed) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "不是加密账号包（拒绝导入明文凭证）"})
		return
	}
	plain, err := openBackup(sealed, pass)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	var bundle accountBundle
	if err := json.Unmarshal(plain, &bundle); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "账号包内容损坏: " + err.Error()})
		return
	}
	if bundle.Format != bundleFormat || bundle.Version != bundleVersion {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "不支持的账号包格式或版本"})
		return
	}

	existing := map[string]bool{}
	for _, env := range g.pool.All() {
		existing[envLocalIdentity(env)] = true
	}
	imported := make([]string, 0, len(bundle.Accounts))
	skipped := make([]string, 0)
	failed := make([]map[string]string, 0)
	for _, a := range bundle.Accounts {
		p, ok := provider.Get(a.ProviderID)
		if !ok {
			failed = append(failed, map[string]string{"label": a.Label, "error": "渠道未注册: " + a.ProviderID})
			continue
		}
		if len(a.Credentials) == 0 {
			failed = append(failed, map[string]string{"label": a.Label, "error": "缺少凭证"})
			continue
		}
		id := localIdentity(a.ProviderID, a.Label, func(k string) string { return a.Credentials[k] })
		if existing[id] {
			skipped = append(skipped, a.Label)
			continue
		}
		label := g.uniqueLocalLabel(a.ProviderID, a.Label, p.Manifest().DisplayName)
		env, apiErr := g.createAccount(a.ProviderID, label, a.Credentials, a.TLSProfile)
		if apiErr != nil {
			failed = append(failed, map[string]string{"label": label, "error": apiErr.Message})
			continue
		}
		if a.Note != "" {
			env.SetNote(a.Note)
		}
		if len(a.Tags) > 0 {
			env.SetTags(a.Tags)
		}
		if a.Priority > 0 {
			env.SetPriority(a.Priority)
		}
		if a.Weight > 1 {
			env.SetWeight(a.Weight)
		}
		if a.Concurrency != nil {
			env.SetConcurrencyOverride(a.Concurrency)
		}
		if len(a.AllowedModels) > 0 {
			env.SetAllowedModels(a.AllowedModels)
		}
		if len(a.ModelMapping) > 0 {
			env.SetModelMapping(a.ModelMapping)
		}
		env.SetDisabled(a.Disabled)
		if a.CreatedAt > 0 {
			env.SetCreatedAt(time.Unix(a.CreatedAt, 0))
		}
		if a.ProxyOverride != "" {
			env.SetProxyOverride(a.ProxyOverride)
			_ = g.rebuildAccountClient(env)
		}
		existing[id] = true
		imported = append(imported, label)
		if g.providerQuotaReadable(a.ProviderID) {
			g.ensureQuotaRead(env)
		}
	}
	if len(imported) > 0 {
		g.saveAccountsNow()
		eventbus.Emit(eventbus.TopicAccount+".imported", map[string]any{
			"count": len(imported), "auto": false, "bundle": true,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"imported": imported,
		"skipped":  skipped,
		"failed":   failed,
		"total":    len(bundle.Accounts),
	})
}
