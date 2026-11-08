package gateway

import (
	"log"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/pool"
)

// migrateLegacyRelayAccounts 把「接在 openai-compat 上的中转站账号」迁到独立自定义渠道。
//
// 背景：早先「接入渠道」页里那张「OpenAI 兼容端点」卡片走的是 manual_input，
// 账号直接建在内置 `openai-compat` 上。后果是模型 id 永远是
// `openai-compat/<模型名>`——前缀读不出是哪个上游；而且该渠道被当成测试渠过滤，
// 用户在额度页/概览里完全看不到自己刚接的账号。
//
// 判据：provider=openai-compat **且** 凭据里有非空 base_url。内置 openai-compat
// 本身没有真实上游（它只是个空壳），所以带 base_url 的账号一定是用户填的中转站。
// 没有 base_url 的（真正的历史遗留空壳）不动。
//
// 迁移方式：用账号 label 作为渠道名建一条 CustomChannel（用户名就是渠道名，
// 模型前缀随之变成 `custom-<label>`），把同一份凭据挂进新渠道，
// **原账号删除**——这一步是必要的，否则同一个中转站会有两个账号在池里，
// 路由会随机选到旧的那个，用户看到的还是 `openai-compat/` 前缀。
//
// 返回迁移成功的账号数；任何一条失败都不影响其它账号，也不回滚已成功的部分
// （迁移是幂等的：第二次启动时旧账号已经不在了）。
func (g *Gateway) migrateLegacyRelayAccounts() int {
	moved := 0
	for _, env := range g.pool.ListByProvider("openai-compat") {
		if env == nil {
			continue
		}
		base := strings.TrimSpace(env.GetCredential("base_url"))
		if base == "" {
			continue
		}
		key := strings.TrimSpace(env.GetCredential("api_key"))
		if key == "" {
			key = strings.TrimSpace(env.GetCredential("access_token"))
			}
		if key == "" {
			log.Printf("[MIGRATE] 跳过 %s：既有 base_url 又没有可用的 key", env.AccountID)
			continue
		}

		label := strings.TrimSpace(env.Label)
		if label == "" {
			label = "中转站-" + shortHash(base)
		}

		ch, ok, err := g.upsertCustomChannelFromDraft(&providerDraft{
			ID:    "",
			Name:  label,
			Key:   key,
			Chat:  base + "/chat/completions",
			Models: nil,
		})
		if err != nil || !ok {
			log.Printf("[MIGRATE] %s 迁移失败：ok=%v err=%v", env.AccountID, ok, err)
			continue
		}

		pid := customChannelProviderID(ch.ID)
		creds := map[string]string{
			"base_url": strings.TrimRight(strings.TrimSpace(base), "/"),
			"api_key":  key,
		}
		if _, apiErr := g.createAccount(pid, label, creds, env.TLSProfile); apiErr != nil {
			log.Printf("[MIGRATE] %s 建新账号失败：%s", env.AccountID, apiErr.Message)
			continue
		}
		// 旧账号必须删掉：留着会和新的抢路由，用户看到的还是旧前缀。
		if !g.pool.Remove(env.AccountID) {
			log.Printf("[MIGRATE] %s 已建新账号但旧账号删除失败，请手动清理", env.AccountID)
		}
		moved++
		log.Printf("[MIGRATE] %s → %s（渠道 %q）", env.AccountID, pid, ch.Name)
	}
	if moved > 0 {
		g.saveAccountsNow()
		g.models.Rebuild()
		log.Printf("[MIGRATE] 共迁移 %d 个中转站账号到独立自定义渠道", moved)
	}
	return moved
}

// 编译期断言：确认 pool 包被引用（ListByProvider/Remove 来自 pool.Manager）。
var _ = pool.SelectOptions{}
var _ = ir.ErrCodeNoProvider