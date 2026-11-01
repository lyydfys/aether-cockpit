package gateway

import (
	"net/http"
	"strings"
)

// GET /api/login/usage?agent=
//
// magpie（前端 app.js 的 loginUsageOf）用它拿"某个客户端登录账号"的额度：
// 响应是 user → quota 的映射，quota 的字段与我们的额度卡完全同构
// （windows[{name,used,resetsAt,...}] / plan / balance / resets / asOf /
// readAt / error），逐字段契约以本接口的测试为准。
//
// 为什么现在才实现：我们的 /api/providers 不下发 magpie 的 `account` 层，
// 所以这条调用在当前前端里走不到（p.account 为空）——但它是公开契约的一部分，
// 留着 404 会让任何按 magpie 契约接入的调用方踩空。这里按"现有额度卡投影"
// 实现，不改动 providers 视图结构。
//
// agent 合法性：接受客户端 id（claude_code 等）、magpie 的订阅 id（claude 等）
// 与我们的渠道 id（claude-sub 等）；未知值不过滤（返回全部账号）——前端那条
// 路径今天不可达，放宽比拒绝更安全，也不会伪造数据。
func (g *Gateway) handleLoginUsage(w http.ResponseWriter, r *http.Request) {
	agent := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("agent")))
	cards, _ := g.usageQuotaCards()

	want := loginUsageProviders(agent)
	out := make(map[string]quotaCard, len(cards))
	for _, c := range cards {
		if len(want) > 0 && !want[c.Provider] {
			continue
		}
		user := strings.TrimSpace(c.User)
		if user == "" {
			continue
		}
		// 同一 label 可能出现在多个渠道：按 provider+user 排序后的第一张为准，
		// 避免输出顺序随池内顺序抖动。
		if _, ok := out[user]; ok {
			continue
		}
		out[user] = c
	}
	writeJSON(w, http.StatusOK, out)
}

// loginUsageProviders 把 agent 折成渠道 id 集合；空集合表示"不过滤"。
func loginUsageProviders(agent string) map[string]bool {
	if agent == "" {
		return nil
	}
	aliases := map[string][]string{
		"claude":      {"claude-sub"},
		"claude_code": {"claude-sub"},
		"claude-code": {"claude-sub"},
		"codex":       {"codex"},
		"codex_cli":   {"codex"},
		"codex-cli":   {"codex"},
		"gemini":      {"gemini-compat"},
		"gemini_cli":  {"gemini-compat"},
		"gemini-cli":  {"gemini-compat"},
		"opencode":    {"opencode"},
		"mimocode":    {"opencode"},
	}
	if ids, ok := aliases[agent]; ok {
		out := make(map[string]bool, len(ids))
		for _, id := range ids {
			out[id] = true
		}
		return out
	}
	// 直接当渠道 id 用（调用方也可以传 claude-sub / codex 这种）。
	return map[string]bool{agent: true}
}
