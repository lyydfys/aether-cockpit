package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"sort"
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
	key := strings.TrimSpace(g.GatewayKey())
	if key == "" {
		key = "aether-local"
	}
	kept := g.driftKept()
	for _, a := range g.buildAetherState().Agents {
		if !a.Wired {
			continue
		}
		params := g.driftParamsFor(a, origin, key)
		changes, err := mgr.PreviewApply(a.ID, params)
		if err != nil || len(changes) == 0 {
			continue
		}
		// 用户点过"保持当前设置"：只要磁盘内容还是当时那一份，就不再提示。
		// 一旦配置再次变化（hash 变了），漂移会重新出现。
		if kept[a.ID] != "" && kept[a.ID] == driftDigest(changes) {
			continue
		}
		out[a.ID] = driftFromChanges(changes, params.Model)
	}
	writeJSON(w, http.StatusOK, out)
}

// driftDigest 把一次预览结果压成稳定摘要，用于"用户已确认保留"的记忆。
//
// 用摘要而不是字段值：漂移可能是任意一处文件差异，逐字段比对既复杂又容易漏。
// 摘要变了就说明文件又动过，正好是"重新提示"的时机。
//
// 必须与顺序无关：diff 里混着 modelPicker 的模型列表，而模型目录来自 map，
// 每次渲染的行序都可能不同（同一份配置算出不同摘要 → keep 立刻失效）。
// 所以先把 "op+文本" 排序再哈希——配置内容变了，集合一定变；
// 只是行序变了，集合不变，不该被当成新漂移。
func driftDigest(changes []clients.PreviewChange) string {
	rows := make([]string, 0, len(changes)*4)
	for _, c := range changes {
		// 只取文件名：临时目录路径在测试里会变，对"配置是否被改"没有意义。
		rows = append(rows, "path:"+filepath.Base(c.Path))
		for _, ln := range c.Lines {
			rows = append(rows, ln.Op+"\x00"+ln.Text)
		}
	}
	sort.Strings(rows)
	h := sha256.New()
	for _, r := range rows {
		h.Write([]byte(r))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// driftKept 返回"用户已确认保留"的 Agent -> 摘要。
func (g *Gateway) driftKept() map[string]string {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	raw, _ := g.settings.UI["driftKeep"].(map[string]any)
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			out[k] = s
		}
	}
	return out
}

// markDriftKept 记住"这个 Agent 的当前差异是用户有意保留的"。
func (g *Gateway) markDriftKept(id, digest string) error {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.UI == nil {
		g.settings.UI = map[string]any{}
	}
	all, _ := g.settings.UI["driftKeep"].(map[string]any)
	if all == nil {
		all = map[string]any{}
		g.settings.UI["driftKeep"] = all
	}
	if digest == "" {
		delete(all, id)
	} else {
		all[id] = digest
	}
	return g.saveSettings()
}

// driftParamsFor 构造“重新写一次”的参数，和 GET /api/drift 的预览保持一致。
func (g *Gateway) driftParamsFor(a aetherAgent, origin, key string) clients.ApplyParams {
	model := ""
	if len(a.Fields) > 0 {
		model = a.Fields[0].Value
	}
	if model == "" {
		if opts := g.modelOptions(); len(opts) > 0 {
			model = opts[0].Value
		}
	}
	return clients.ApplyParams{
		BaseURL: origin, APIKey: key, Model: model, ModelOptions: g.agentModelOptions(a.ID),
		Tiers: g.agentTiers(a.ID), Effort: g.agentStageOf(a.ID)["effort"],
	}
}

// agentTiers 取该 Agent 暂存的档位模型（opus/sonnet/haiku），供漂移预览对齐。
func (g *Gateway) agentTiers(id string) map[string]string {
	staged := g.agentStageOf(id)
	tiers := map[string]string{}
	for _, k := range []string{"opus", "sonnet", "haiku", "small"} {
		if v := strings.TrimSpace(staged[k]); v != "" {
			tiers[k] = v
		}
	}
	if len(tiers) == 0 {
		return nil
	}
	return tiers
}

// handleDriftApply POST /api/drift/apply —— 把检测到的漂移一键写回。
// 请求体可带 {"ids":[...]} 只修指定的 Agent；不带则修全部有漂移的。
func (g *Gateway) handleDriftApply(w http.ResponseWriter, r *http.Request) {
	mgr := g.clientsManager()
	if mgr == nil {
		writeJSON(w, http.StatusOK, map[string]any{"applied": []string{}, "errors": map[string]string{}})
		return
	}
	var body struct {
		IDs []string `json:"ids"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	wanted := make(map[string]bool, len(body.IDs))
	for _, id := range body.IDs {
		wanted[strings.TrimSpace(id)] = true
	}

	origin := requestOrigin(r, g.PublicAddr())
	key := strings.TrimSpace(g.GatewayKey())
	if key == "" {
		key = "aether-local"
	}
	applied := make([]string, 0, 4)
	errs := map[string]string{}
	for _, a := range g.buildAetherState().Agents {
		if !a.Wired {
			continue
		}
		if len(wanted) > 0 && !wanted[a.ID] {
			continue
		}
		params := g.driftParamsFor(a, origin, key)
		changes, err := mgr.PreviewApply(a.ID, params)
		if err != nil {
			errs[a.ID] = err.Error()
			continue
		}
		if len(changes) == 0 {
			continue
		}
		if _, err := mgr.Apply(a.ID, params); err != nil {
			errs[a.ID] = err.Error()
			continue
		}
		applied = append(applied, a.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"applied": applied, "errors": errs})
}

// driftFromChanges 把文件变化摊成前端要的 drift 形状。
//
// 分类（与 aether 的五类对齐，便于页面按类型给不同的修复动作）：
//   - removed ：我们写过的那份文件被人删了 → 只能重新接入
//   - created ：文件是别人新建的（我们没写过）→ 提示可能重复配置
//   - unwired ：网关地址被人改回官方 → 典型"换回原客户端"
//   - model   ：地址还在，只有模型/档位被改 → 只重写模型即可
//   - edited  ：其它键被改（用户手改、其它工具写入）
func driftFromChanges(changes []clients.PreviewChange, want string) map[string]any {
	first := changes[0]
	kind := classifyDrift(changes)
	var b strings.Builder
	switch kind {
	case "removed":
		b.WriteString("配置文件已被删除：")
	case "created":
		b.WriteString("检测到新建的配置文件：")
	case "unwired":
		b.WriteString("网关地址已被改回官方：")
	case "model":
		b.WriteString("模型设置已被改动：")
	default:
		b.WriteString("配置文件与 Aether Cockpit 上次写入的不一致：")
	}
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
		"kind":   kind,
		"field":  "model",
		"want":   want,
		"detail": b.String(),
	}
}

// classifyDrift 按变化内容判断漂移类型（多个文件时以最严重的一处为准）。
func classifyDrift(changes []clients.PreviewChange) string {
	sev := map[string]int{"removed": 4, "unwired": 3, "created": 2, "edited": 1, "model": 0}
	worst := "edited"
	for _, c := range changes {
		k := "edited"
		switch {
		case c.Removed:
			k = "removed"
		case c.Created:
			k = "created"
		default:
			// 地址类变化优先级最高：它意味着整个接入失效。
			addr, model := false, false
			for _, ln := range c.Lines {
				t := strings.ToLower(ln.Text + " " + ln.Was)
				if strings.Contains(t, "base_url") || strings.Contains(t, "baseurl") ||
					strings.Contains(t, "anthropic_base_url") || strings.Contains(t, "api_url") ||
					strings.Contains(t, "openaibaseurl") || strings.Contains(t, "deploymenturl") {
					addr = true
				}
				if strings.Contains(t, "model") && !strings.Contains(t, "base") {
					model = true
				}
			}
			switch {
			case addr:
				k = "unwired"
			case model:
				k = "model"
			}
		}
		if sev[k] > sev[worst] || (k == "model" && worst == "edited") {
			worst = k
		}
	}
	return worst
}
