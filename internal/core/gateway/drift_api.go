package gateway

import (
	"net/http"
	"strings"

	"aether-cockpit/internal/core/clients"
)

// GET /api/drift —— Agents 页的"配置被别人改过"检测。
//
// 对每个已接入的 Agent，把"此刻再 Apply 一次会产生的 diff"算出来：
// 有 diff 说明磁盘上的配置已经不是 Aether Cockpit 上次写的模样
// （另一个工具改回官方地址、手改了模型、文件被删等），页面给一个
// "Apply again" 药丸，一键写回。（aether 的 drift 语义。）
//
// 只读：PreviewApply 不落盘、不写备份。

func (g *Gateway) handleDrift(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}
	mgr := g.clientsManager()
	if mgr == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	origin := requestOrigin(r, g.PublicAddr())
	key := strings.TrimSpace(g.cfg.APIKey)
	if key == "" {
		key = "aether-local"
	}
	for _, a := range g.buildAetherState().Agents {
		if !a.Wired {
			continue
		}
		model := ""
		if len(a.Fields) > 0 {
			model = a.Fields[0].Value
		}
		if model == "" {
			if opts := g.modelOptions(); len(opts) > 0 {
				model = opts[0].Value
			}
		}
		changes, err := mgr.PreviewApply(a.ID, clients.ApplyParams{
			BaseURL: origin, APIKey: key, Model: model, ModelOptions: g.agentModelOptions(a.ID),
		})
		if err != nil || len(changes) == 0 {
			continue
		}
		out[a.ID] = driftFromChanges(changes, model)
	}
	writeJSON(w, http.StatusOK, out)
}

// driftFromChanges 把第一处文件变化摊成前端要的 drift 形状。
func driftFromChanges(changes []clients.PreviewChange, want string) map[string]any {
	first := changes[0]
	var b strings.Builder
	b.WriteString("配置文件与 Aether Cockpit 上次写入的不一致：")
	b.WriteString(first.Path)
	for _, ln := range first.Lines {
		if t := strings.TrimSpace(ln.Text); t != "" {
			b.WriteString("（")
			b.WriteString(t)
			b.WriteString("）")
			break
		}
	}
	return map[string]any{
		"kind":   "unwired",
		"field":  "model",
		"want":   want,
		"detail": b.String(),
	}
}
