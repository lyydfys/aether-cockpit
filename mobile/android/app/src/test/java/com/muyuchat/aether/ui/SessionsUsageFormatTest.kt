package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.SessionUsage
import com.muyuchat.aether.runtime.SessionUsageDay
import com.muyuchat.aether.runtime.SessionUsageModel
import com.muyuchat.aether.runtime.SessionUsageTotals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 会话用量视图的纯格式化与排序。
 *
 * 桌面依据：`sessions.js:210-256`（三张表）与 `:248`（按日期**倒序**）、
 * `:29-34`（`fmtBig`：≥1 亿用「亿」、≥1 万用「万」）。
 */
class SessionsUsageFormatTest {
    private fun totals(
        tokens: Long = 3800L,
        requests: Long = 42L,
        cost: Double = 0.1234,
    ) = SessionUsageTotals(
        input = 1000L,
        cacheRead = 2000L,
        cacheWrite = 500L,
        output = 300L,
        tokens = tokens,
        requests = requests,
        cost = cost,
    )

    @Test
    fun viewAndPeriodLabelsFollowDesktopCopy() {
        assertEquals("会话列表", sessionViewLabel(SessionView.LIST))
        assertEquals("会话用量", sessionViewLabel(SessionView.USAGE))
        // 桌面 sessions.js:263 的三档
        assertEquals("今日", sessionUsagePeriodLabel(1))
        assertEquals("近 7 天", sessionUsagePeriodLabel(7))
        assertEquals("近 30 天", sessionUsagePeriodLabel(30))
        // 非常规值按 7 天兜底，不显示成 "近 0 天"
        assertEquals("近 7 天", sessionUsagePeriodLabel(0))
    }

    @Test
    fun bigNumbersUseWanAndYiLikeDesktop() {
        assertEquals("0", sessionUsageBigLabel(0))
        assertEquals("9999", sessionUsageBigLabel(9999))
        assertEquals("1.00 万", sessionUsageBigLabel(10_000))
        assertEquals("1.23 万", sessionUsageBigLabel(12_345))
        assertEquals("1.00 亿", sessionUsageBigLabel(100_000_000))
        // 负数不出现（页面不显示负 Token）
        assertEquals("0", sessionUsageBigLabel(-5))
    }

    @Test
    fun costIsHonestAboutUnpricedRequests() {
        assertEquals("$0.1234", sessionUsageCostLabel(0.1234, 0L))
        assertEquals("$0.0000", sessionUsageCostLabel(0.0, 0L))
        // 有未计价请求时必须标注，不能把未知成本当 0
        assertTrue(sessionUsageCostLabel(0.5, unpriced = 3L).contains("未计价 3 次"))
        // NaN/负值不显示成 "$NaN"
        assertEquals("$0.0000", sessionUsageCostLabel(Double.NaN, 0L))
        assertEquals("$0.0000", sessionUsageCostLabel(-1.0, 0L))
    }

    @Test
    fun totalsLineCombinesTokensRequestsAndCost() {
        assertEquals(
            "Token 3800 · 请求 42 · $0.1234",
            sessionUsageTotalsLine(totals()),
        )
        assertTrue(sessionUsageTotalsLine(totals(tokens = 12_345), unpriced = 2L).contains("1.23 万"))
        assertTrue(sessionUsageTotalsLine(totals(tokens = 12_345), unpriced = 2L).contains("未计价 2 次"))
    }

    /** 桌面把 series 按日期倒序（`sessions.js:248`），服务端是升序回包。 */
    @Test
    fun dayRowsAreSortedNewestFirst() {
        val usage = SessionUsage(
            from = "2026-10-29",
            to = "2026-11-04",
            days = 7,
            agent = "",
            files = 3,
            sessions = 1,
            totals = totals(),
            unpriced = 0L,
            series = listOf(
                SessionUsageDay("2026-11-02", totals()),
                SessionUsageDay("2026-11-04", totals()),
                SessionUsageDay("2026-11-03", totals()),
            ),
            models = listOf(SessionUsageModel("m1", totals())),
            agents = emptyList(),
            lastSync = 0L,
        )
        assertEquals(
            listOf("2026-11-04", "2026-11-03", "2026-11-02"),
            sessionUsageDayRows(usage).map { it.date },
        )
        // 按实例保持服务端顺序（tokens 降序）
        assertTrue(sessionUsageAgentRows(usage).isEmpty())
    }
}
