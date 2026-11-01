package gateway

import (
	"context"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/provider"
)

// 账号批量管理 —— 对齐 cockpit-tools 的批量操作与脱敏导出。
//
// 脱敏导出只含元数据（渠道 / 标签 / 状态 / 额度快照），绝不包含凭证：
// 账号凭证是 AES 本机绑定加密的，离开本机也无法解密，明文导出等于
// 把密钥写进一个迟早会被分享出去的文件。

// handleSetAccountTags POST /api/accounts/{id}/tags
//
// 请求体：{"tags": ["主力", "临期"]}
func (g *Gateway) handleSetAccountTags(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	env, ok := g.pool.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "账号不存在: " + id})
		return
	}

	var body struct {
		Tags []string `json:"tags"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	env.SetTags(body.Tags)
	g.saveAccountsNow()
	eventbus.Emit(eventbus.TopicAccount+".updated", map[string]any{
		"account_id": id, "provider_id": env.ProviderID, "change": "tags",
	})
	writeJSON(w, http.StatusOK, map[string]any{"account_id": id, "tags": env.TagsList()})
}

// handleSetAccountNote POST /api/accounts/{id}/note
//
// 请求体：{"note": "这条账号今天开始限流"}
func (g *Gateway) handleSetAccountNote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	env, ok := g.pool.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "账号不存在: " + id})
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	env.SetNote(body.Note)
	g.saveAccountsNow()
	eventbus.Emit(eventbus.TopicAccount+".updated", map[string]any{
		"account_id": id, "provider_id": env.ProviderID, "change": "note",
	})
	writeJSON(w, http.StatusOK, map[string]any{"account_id": id, "note": env.Note()})
}

// handleAccountSettings POST /api/accounts/{id}/settings
//
// 账号级路由参数（cockpit-tools 的账号编辑）：启用状态、优先级、权重、
// 并发覆盖、模型白名单。字段缺省表示不修改；concurrency=-1 表示跟随全局。
func (g *Gateway) handleAccountSettings(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	env, ok := g.pool.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "账号不存在: " + id})
		return
	}
	var body struct {
		Disabled      *bool     `json:"disabled"`
		Priority      *int      `json:"priority"`
		Weight        *int      `json:"weight"`
		Concurrency   *int      `json:"concurrency"`
		AllowedModels *[]string `json:"allowed_models"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if body.Disabled != nil {
		env.SetDisabled(*body.Disabled)
	}
	if body.Priority != nil {
		env.SetPriority(*body.Priority)
	}
	if body.Weight != nil {
		env.SetWeight(*body.Weight)
	}
	if body.Concurrency != nil {
		if *body.Concurrency < 0 {
			env.SetConcurrencyOverride(nil)
		} else {
			limit := *body.Concurrency
			env.SetConcurrencyOverride(&limit)
		}
	}
	if body.AllowedModels != nil {
		seen := make(map[string]struct{}, len(*body.AllowedModels))
		models := make([]string, 0, len(*body.AllowedModels))
		for _, m := range *body.AllowedModels {
			m = strings.TrimSpace(m)
			if m == "" {
				continue
			}
			if _, dup := seen[m]; dup {
				continue
			}
			seen[m] = struct{}{}
			models = append(models, m)
			if len(models) >= 200 {
				break
			}
		}
		env.SetAllowedModels(models)
	}
	g.saveAccountsNow()
	eventbus.Emit(eventbus.TopicAccount+".updated", map[string]any{
		"account_id": id, "provider_id": env.ProviderID, "change": "settings",
	})
	writeJSON(w, http.StatusOK, map[string]any{"account": env.Snapshot()})
}

// handleAccountBatch POST /api/accounts/batch
//
// 请求体：{"action": "delete" | "refresh" | "ready", "ids": ["a", "b"]}
//
// refresh 按顺序执行（同一渠道账号间留 1~2 秒间隔，避免同时打上游刷新端点）。
func (g *Gateway) handleAccountBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if len(body.IDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 ids"})
		return
	}

	type batchResult struct {
		ID    string `json:"id"`
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	results := make([]batchResult, 0, len(body.IDs))

	switch body.Action {
	case "delete":
		for _, id := range body.IDs {
			ok := g.pool.Remove(id)
			if ok {
				eventbus.Emit(eventbus.TopicAccount+".removed", map[string]any{"account_id": id})
			}
			results = append(results, batchResult{ID: id, OK: ok})
		}
		g.saveAccountsNow()

	case "ready":
		for _, id := range body.IDs {
			env, ok := g.pool.Get(id)
			if !ok {
				results = append(results, batchResult{ID: id, Error: "账号不存在"})
				continue
			}
			env.MarkReady()
			eventbus.Emit(eventbus.TopicAccount+".state_changed", env.Snapshot())
			results = append(results, batchResult{ID: id, OK: true})
		}

	case "refresh":
		for i, id := range body.IDs {
			if i > 0 {
				time.Sleep(time.Duration(1000+i*300) * time.Millisecond)
			}
			env, ok := g.pool.Get(id)
			if !ok {
				results = append(results, batchResult{ID: id, Error: "账号不存在"})
				continue
			}
			p, ok := provider.Get(env.ProviderID)
			if !ok {
				results = append(results, batchResult{ID: id, Error: "渠道未注册"})
				continue
			}
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			_, err := g.RefreshAccount(ctx, env, p)
			cancel()
			if err != nil {
				results = append(results, batchResult{ID: id, Error: err.Error()})
				continue
			}
			g.saveAccountsNow()
			eventbus.Emit(eventbus.TopicAccount+".refreshed", map[string]any{
				"account_id": id, "provider_id": env.ProviderID,
			})
			results = append(results, batchResult{ID: id, OK: true})
		}

	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "未知 action: " + body.Action + "（支持 delete / refresh / ready）"})
		return
	}

	okN := 0
	for _, rr := range results {
		if rr.OK {
			okN++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "ok": okN, "total": len(results)})
}

// handleExportAccounts GET /api/accounts/export —— 脱敏导出（无任何凭证）。
func (g *Gateway) handleExportAccounts(w http.ResponseWriter, r *http.Request) {
	envs := g.pool.All()
	type exportAccount struct {
		AccountID  string                `json:"account_id"`
		ProviderID string                `json:"provider_id"`
		Label      string                `json:"label,omitempty"`
		Tags       []string              `json:"tags,omitempty"`
		Note       string                `json:"note,omitempty"`
		CreatedAt  int64                 `json:"created_at,omitempty"`
		State      string                `json:"state"`
		Quota      *provider.QuotaStatus `json:"quota,omitempty"`
		TLSProfile string                `json:"tls_profile,omitempty"`
	}
	out := make([]exportAccount, 0, len(envs))
	for _, e := range envs {
		snap := e.Snapshot()
		out = append(out, exportAccount{
			AccountID:  snap.AccountID,
			ProviderID: snap.ProviderID,
			Label:      snap.Label,
			Tags:       snap.Tags,
			Note:       snap.Note,
			CreatedAt:  snap.CreatedAt,
			State:      string(snap.State),
			Quota:      snap.Quota,
			TLSProfile: snap.TLSProfile,
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=accounts-export.json")
	writeJSON(w, http.StatusOK, map[string]any{
		"version":     1,
		"exported_at": time.Now().Unix(),
		"note":        "脱敏导出：仅含账号元数据，不含任何凭证字段",
		"accounts":    out,
	})
}
