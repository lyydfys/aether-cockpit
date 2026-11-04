package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 链路节点详情（桌面 `overview.js:376-413`）。
 *
 * 三条红线：
 * 1. 端点按 `models[].id` 推断（`familiesOf`，`overview.js:364-374`）；
 * 2. 渠道行带账号数；
 * 3. **没有额度窗口时给「—」+ 原因，绝不显示 0%**（桌面也不画 0%）。
 */
class ProviderDetailTest {
    private fun link(
        id: String = "codex",
        name: String = "Codex",
        accounts: Int = 3,
        modelIds: List<String> = listOf("gpt-5-codex"),
    ) = OverviewProviderLink(
        id = id,
        name = name,
        accountCount = accounts,
        readyCount = accounts,
        off = false,
        calls = 0L,
        errors = 0L,
        cost = 0.0,
        modelIds = modelIds,
    )

    private fun quota(
        providerId: String,
        name: String,
        remaining: Double,
    ) = OverviewQuotaRow(
        name = name,
        remainingPct = remaining,
        resetNote = "未提供重置时间",
        providerId = providerId,
    )

    @Test
    fun endpointFollowsDesktopFamiliesOf() {
        assertEquals("Anthropic", providerEndpointLabel(listOf("claude-sonnet-4")))
        assertEquals("Gemini", providerEndpointLabel(listOf("gemini-2.5-pro")))
        assertEquals("OpenAI", providerEndpointLabel(listOf("gpt-5-codex")))
        // 一个模型都没有时桌面给 openai（overview.js:372）
        assertEquals("OpenAI", providerEndpointLabel(emptyList()))
        // 混合时按 openai → anthropic → gemini 稳定输出
        assertEquals(
            "OpenAI / Anthropic / Gemini",
            providerEndpointLabel(listOf("gpt-5", "claude-x", "gemini-2.5")),
        )
        // 大小写无关
        assertEquals("Anthropic", providerEndpointLabel(listOf("  CLAUDE-3 ")))
        assertEquals(setOf("gemini"), providerEndpointFamilies(listOf("Gemini-Pro")))
    }

    @Test
    fun channelLineCarriesAccountCount() {
        val detail = providerDetail(link(name = "Codex", accounts = 3), emptyList())
        assertEquals("Codex · 3 个账号", detail.channel)
    }

    @Test
    fun lowestRemainingPicksTightestWindowOfThatProvider() {
        val rows = listOf(
            quota("codex", "Codex · 5h", 62.0),
            quota("claude-sub", "Claude · week", 8.0),
            quota("codex", "Codex · week", 12.5),
        )
        val detail = providerDetail(link(), rows)
        // 只挑本渠道的窗口（8% 属于 claude-sub，不参与）
        assertEquals("12.5%", detail.lowest)
        assertTrue(detail.lowestNote.contains("Codex"))
        assertEquals("OpenAI", detail.endpoint)
    }

    @Test
    fun noWindowShowsDashWithReasonInsteadOfZero() {
        val detail = providerDetail(link(), emptyList())
        assertEquals("—", detail.lowest)
        assertEquals("该渠道没有额度窗口", detail.lowestNote)
        // 其它渠道的窗口不算自己家的
        val other = providerDetail(link(), listOf(quota("claude-sub", "Claude", 3.0)))
        assertEquals("—", other.lowest)
        assertTrue(!other.lowest.contains("0"))
    }

    @Test
    fun percentFormattingMatchesQuotaCardRules() {
        assertEquals("100", formatQuotaPct(100.0))
        assertEquals("0", formatQuotaPct(0.0))
        assertEquals("44.5", formatQuotaPct(44.5))
        assertEquals("44.5", formatQuotaPct(44.4999))
        assertEquals("—", formatQuotaPct(Double.NaN))
    }
}
