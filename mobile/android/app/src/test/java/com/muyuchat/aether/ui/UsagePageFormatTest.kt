package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.UsageSummary
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 用量页的排版纯逻辑（对标桌面 `usage.js`）：
 * 周期口径、成功率/缓存命中率、注解行、分类行、趋势刻度、币种口径、空态与错误文案。
 *
 * 口径与概览页一致：Token = 输入 + 输出 + 缓存读 + 缓存写（桌面 `usage.js:90-91`）。
 */
class UsagePageFormatTest {

    private fun summary(
        calls: Long = 0,
        errors: Long = 0,
        input: Long = 0,
        output: Long = 0,
        cacheRead: Long = 0,
        cacheWrite: Long = 0,
        cost: Double = 0.0,
        unpriced: Long = 0,
        providers: List<com.muyuchat.aether.runtime.UsageProviderRow> = emptyList(),
        models: List<com.muyuchat.aether.runtime.UsageModelRow> = emptyList(),
        agents: List<com.muyuchat.aether.runtime.UsageBreakdownRow> = emptyList(),
    ) = UsageSummary(
        period = "today",
        calls = calls,
        errors = errors,
        inputTokens = input,
        outputTokens = output,
        cacheReadTokens = cacheRead,
        cacheWriteTokens = cacheWrite,
        cost = cost,
        models = models,
        series = emptyList(),
        providers = providers,
        unpriced = unpriced,
        agents = agents,
    )

    @Test
    fun periodScopeLabelCoversAllThreeDesktopSegments() {
        assertEquals("今日", usagePeriodScopeLabel(UsagePeriod.TODAY))
        assertEquals("近 7 天", usagePeriodScopeLabel(UsagePeriod.WEEK))
        assertEquals("近 30 天", usagePeriodScopeLabel(UsagePeriod.MONTH))
        assertEquals(listOf("今日", "近 7 天", "近 30 天"), UsagePeriod.entries.map { it.label })
    }

    @Test
    fun successRateIsDerivedFromCallsAndErrors() {
        assertEquals("80.0%", usageSuccessRateText(summary(calls = 10, errors = 2)))
        assertEquals("100.0%", usageSuccessRateText(summary(calls = 3, errors = 0)))
        // 没有请求写 "—"：0 请求时的 0% 是假事实。
        assertEquals("—", usageSuccessRateText(summary(calls = 0)))
        // 失败数超过请求数（脏数据）也不能给出负数或 >100%。
        assertEquals("0.0%", usageSuccessRateText(summary(calls = 2, errors = 5)))
    }

    @Test
    fun cacheHitRateUsesInputPlusCacheDenominator() {
        // 50 / (100 + 50 + 0) = 33.3%
        assertEquals("33.3%", usageCacheHitRateText(summary(input = 100, cacheRead = 50)))
        // 分母为 0 时写 "—"，不写 0%。
        assertEquals("—", usageCacheHitRateText(summary()))
    }

    @Test
    fun tokenTotalIncludesCacheLikeDesktop() {
        // 桌面 usage.js:90-91：input + cache_read + cache_write + output
        assertEquals(2_000L, summary(input = 1_000, output = 500, cacheRead = 400, cacheWrite = 100).totalTokens)
    }

    @Test
    fun notesCarryFailureCountAndCacheRate() {
        val requests = usageRequestsNoteText(summary(calls = 10, errors = 2))
        assertTrue(requests, requests.contains("失败 2"))
        assertTrue(requests, requests.contains("80.0%"))

        val tokens = usageTokensNoteText(summary(input = 100, cacheRead = 50))
        assertTrue(tokens, tokens.contains("缓存"))
        assertTrue(tokens, tokens.contains("33.3%"))
        assertFalse(tokens, tokens.contains("总"))
    }

    @Test
    fun costNoteSaysUnpricedOrPriceTable() {
        assertEquals("按当前价格表", usageCostNoteText(summary(cost = 1.0)))
        assertEquals("未计价 7 次", usageCostNoteText(summary(cost = 1.0, unpriced = 7)))
    }

    @Test
    fun trendTotalAndAxisLabelsFollowDesktop() {
        assertEquals("总计: 12 次 · \$1.20", usageTrendTotalText(12, 1.2, "USD", 7.2))
        assertEquals("总计: 12 次 · ¥8.64", usageTrendTotalText(12, 1.2, "CNY", 7.2))
        // 横轴抽稀：最多 6 个标签
        assertEquals(1, usageTrendLabelStep(3))
        assertEquals(1, usageTrendLabelStep(6))
        assertEquals(2, usageTrendLabelStep(7))
        assertEquals(5, usageTrendLabelStep(30))
        // 服务端标签 "Jan 2" → 取日期部分；认不出来原样显示
        assertEquals("2", trendAxisLabel("Jan 2"))
        assertEquals("2026-11-05", trendAxisLabel("2026-11-05"))
        assertEquals("", trendAxisLabel("  "))
    }

    @Test
    fun rateLabelDoesNotInventExchangeRate() {
        assertEquals("汇率未读取", usageRateText(0.0, pricingKnown = false))
        assertEquals("汇率 1 USD = 7.20 CNY", usageRateText(7.2, pricingKnown = true))
        assertEquals("汇率未提供", usageRateText(0.0, pricingKnown = true))
    }

    @Test
    fun calendarRangeAndBreakdownChipMatchDesktop() {
        assertEquals("2026-10-07 → 2026-11-05", usageCalendarRangeLabel("2026-10-07", "2026-11-05"))
        assertEquals("近一年", usageCalendarRangeLabel("", ""))
        assertEquals("3 类", usageBreakdownChipText(3))
    }

    @Test
    fun sharePercentRoundsAndClamps() {
        assertEquals("100%", sharePctText(100.0))
        assertEquals("42%", sharePctText(41.6))
        assertEquals("0%", sharePctText(-5.0))
        assertEquals("100%", sharePctText(180.0))
        assertEquals("0%", sharePctText(Double.NaN))
    }

    @Test
    fun breakdownRowsFollowTheSelectedDimension() {
        val summary = summary(
            providers = listOf(
                com.muyuchat.aether.runtime.UsageProviderRow(
                    id = "p1", name = "Claude", calls = 3, errors = 1, cost = 0.5,
                    inputTokens = 100, outputTokens = 50, cacheReadTokens = 50, cacheWriteTokens = 0,
                ),
                com.muyuchat.aether.runtime.UsageProviderRow(
                    id = "p2", name = "Gemini", calls = 9, errors = 0, cost = 2.0,
                ),
            ),
            models = listOf(
                com.muyuchat.aether.runtime.UsageModelRow(
                    id = "m1", name = "gpt-5", calls = 2, inputTokens = 10, outputTokens = 5, cost = 0.1,
                ),
            ),
            agents = listOf(
                com.muyuchat.aether.runtime.UsageBreakdownRow(
                    id = "claude_code", name = "Claude Code", calls = 4, errors = 0,
                    inputTokens = 10, outputTokens = 5, cacheReadTokens = 0, cacheWriteTokens = 0, cost = 1.0,
                ),
            ),
        )
        val providers = usageBreakdownRows(summary, UsageBreakdown.PROVIDER)
        assertEquals(2, providers.size)
        // 成本降序（桌面 usage.js:215）
        assertEquals("Gemini", providers[0].name)
        assertEquals("Claude", providers[1].name)
        assertEquals(200L, providers[1].totalTokens)

        assertEquals("gpt-5", usageBreakdownRows(summary, UsageBreakdown.MODEL).single().name)
        assertEquals("Claude Code", usageBreakdownRows(summary, UsageBreakdown.AGENT).single().name)
        // 缓存率 = 50 / (100 + 50 + 0) = 33.3%
        assertEquals("33.3%", usageBreakdownCacheRateText(providers[1]))
        assertEquals("—", usageBreakdownCacheRateText(providers[0]))
    }

    @Test
    fun breakdownIsEmptyWhenTheDimensionHasNoRows() {
        assertTrue(usageBreakdownRows(summary(), UsageBreakdown.PROVIDER).isEmpty())
        assertEquals("渠道", UsageBreakdown.PROVIDER.label)
        assertEquals("模型", UsageBreakdown.MODEL.label)
        assertEquals("调用方", UsageBreakdown.AGENT.label)
    }

    @Test
    fun emptyStateSaysWhyAndWhatNext() {
        val headline = usageEmptyHeadline()
        val detail = usageEmptyDetail()
        assertTrue(headline.isNotBlank())
        assertTrue(detail, detail.contains("为什么") || detail.contains("还没"))
        assertTrue(detail, detail.contains("下一步"))
        // 禁语："暂无数据"。
        assertFalse(headline, headline.contains("暂无数据"))
        assertFalse(detail, detail.contains("暂无数据"))
    }

    @Test
    fun sectionEmptyHintsSayWhatNext() {
        for (hint in listOf(usageModelsEmptyHint(), usageSeriesEmptyHint(), usageBreakdownEmptyDetail())) {
            assertTrue(hint, hint.contains("下一步"))
            assertFalse(hint, hint.contains("暂无数据"))
        }
        assertEquals("还没有用量数据", usageBreakdownEmptyHeadline())
    }

    @Test
    fun detailErrorLineKeepsCodeAndReason() {
        val line = usageDetailErrorLine("http_500", "管理接口返回 500")
        assertTrue(line, line.contains("http_500"))
        assertTrue(line, line.contains("管理接口返回 500"))
        // 服务端没给原因时不留空标签。
        val blank = usageDetailErrorLine("network_error", "  ")
        assertTrue(blank, blank.contains("未返回原因"))
    }

    @Test
    fun sectionErrorLineSeparatesNotRunningFromRealFailures() {
        val notRunning = sectionErrorLine("gateway_not_running", "网关未启动", "订阅信息")
        assertEquals("网关未启动：启动后才能读取订阅信息。", notRunning)

        val failed = sectionErrorLine("http_502", "上游超时", "订阅信息")
        assertTrue(failed, failed.contains("上游超时"))
        assertTrue(failed, failed.contains("http_502"))
        assertTrue(failed, failed.contains("订阅信息"))
    }
}
