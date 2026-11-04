package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.QuotaHistoryPoint
import com.muyuchat.aether.runtime.QuotaHoldEstimate
import com.muyuchat.aether.runtime.QuotaHoldWindow
import com.muyuchat.aether.runtime.UsageBreakdownRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 用量页两处新增的纯逻辑：
 * 1. 额度历史的曲线坐标（[quotaSparklineDots]）与文案（[quotaLineSummaryText] / [quotaHoldText]）；
 * 2. 用量分布的排序维度（[usageBreakdownComparator]）。
 */
class UsageQuotaCardTest {

    private fun point(atMs: Long, left: Double) =
        QuotaHistoryPoint(at = atMs.toString(), atMs = atMs, left = left, start = "", resetsAt = "")

    private fun row(name: String, calls: Long, tokens: Long, cost: Double) = UsageBreakdownRow(
        id = name,
        name = name,
        calls = calls,
        errors = 0,
        inputTokens = tokens,
        outputTokens = 0,
        cacheReadTokens = 0,
        cacheWriteTokens = 0,
        cost = cost,
    )

    /* ---------------- 曲线坐标 ---------------- */

    @Test
    fun sparklineUsesFixedZeroToHundredVerticalScale() {
        val dots = quotaSparklineDots(
            points = listOf(point(1_000L, 100.0), point(2_000L, 0.0)),
            width = 200f,
            height = 100f,
        )
        assertEquals(2, dots.size)
        // 剩余 100% → 贴顶（y=0）；剩余 0% → 贴底（y=height）。
        assertEquals(0f, dots[0].y, 1e-3f)
        assertEquals(100f, dots[1].y, 1e-3f)
        // 横轴按时间铺满：首点 0、末点 width。
        assertEquals(0f, dots[0].x, 1e-3f)
        assertEquals(200f, dots[1].x, 1e-3f)
    }

    @Test
    fun sparklineXFollowsRealTimeGapsNotIndexes() {
        // 三个点在 0 / 1h / 4h：横坐标必须按时间比例，否则"长期平稳+最后骤降"会画成等腰三角。
        val dots = quotaSparklineDots(
            points = listOf(point(0L, 80.0), point(3_600_000L, 80.0), point(14_400_000L, 20.0)),
            width = 400f,
            height = 100f,
        )
        assertEquals(3, dots.size)
        assertEquals(0f, dots[0].x, 1e-1f)
        assertEquals(100f, dots[1].x, 1e-1f)
        assertEquals(400f, dots[2].x, 1e-1f)
        assertEquals(20f, dots[0].y, 1e-3f)
        assertEquals(80f, dots[2].y, 1e-3f)
    }

    @Test
    fun sparklineDegradesSafely() {
        assertTrue(quotaSparklineDots(emptyList(), 100f, 50f).isEmpty())
        assertTrue(quotaSparklineDots(listOf(point(1L, 50.0)), 0f, 50f).isEmpty())
        assertTrue(quotaSparklineDots(listOf(point(1L, 50.0)), 100f, 0f).isEmpty())
        // 单点：居中，不除零。
        val single = quotaSparklineDots(listOf(point(1L, 50.0)), 100f, 50f)
        assertEquals(1, single.size)
        assertEquals(50f, single[0].x, 1e-3f)
        assertEquals(25f, single[0].y, 1e-3f)
        // 相同时间戳（span=0）也居中，不产生 NaN。
        val sameTime = quotaSparklineDots(listOf(point(5L, 10.0), point(5L, 90.0)), 100f, 50f)
        assertEquals(1, sameTime.size)
        assertEquals(50f, sameTime[0].x, 1e-3f)
    }

    /* ---------------- 文案 ---------------- */

    @Test
    fun lineSummaryReportsRemainingAndConsumption() {
        val line = com.muyuchat.aether.runtime.QuotaHistoryLine(
            name = "week",
            points = listOf(point(1_000L, 90.0), point(2_000L, 40.0)),
        )
        val text = quotaLineSummaryText(line)
        assertTrue(text.contains("剩余 40%"))
        assertTrue(text.contains("消耗 50%"))
        // 单点只说剩余，不谈消耗。
        val single = com.muyuchat.aether.runtime.QuotaHistoryLine("week", listOf(point(1_000L, 12.0)))
        assertEquals("剩余 12%", quotaLineSummaryText(single))
        // 空曲线直说"没有读数"。
        assertEquals("没有读数", quotaLineSummaryText(com.muyuchat.aether.runtime.QuotaHistoryLine("week", emptyList())))
    }

    @Test
    fun holdTextShowsEstimateOrTheHonestReason() {
        val estimate = QuotaHoldEstimate(
            tokens = 1_000_000L,
            cost = 12.5,
            priced = true,
            used = 42.0,
            routedTokens = 420_000L,
            routedCalls = 37L,
            cacheRead = 0L,
            since = "2026-11-02T00:00:00Z",
        )
        val text = quotaHoldText(QuotaHoldWindow(name = "week", why = "", estimate = estimate))
        assertTrue(text.contains("整窗约"))
        assertTrue(text.contains("$12.50"))
        assertTrue(text.contains("已用 42%"))
        assertTrue(text.contains("37 次调用"))

        // 未定价：说"成本未定价"，不给一个假金额。
        val unpriced = quotaHoldText(
            QuotaHoldWindow("week", "", estimate.copy(priced = false)),
        )
        assertTrue(unpriced.contains("成本未定价"))
        assertTrue(!unpriced.contains("$"))

        // 只有原因：照原因说，不出现"整窗约"。
        val low = quotaHoldText(QuotaHoldWindow("week", "low", null))
        assertTrue(low.contains("5%"))
        assertTrue(!low.contains("整窗约"))

        // 还没有估算结果（没拿到 holds）：如实说缺少起止时间，不谎报。
        val missing = quotaHoldText(null)
        assertTrue(missing.contains("缺少窗口起止时间"))
    }

    /* ---------------- 排序维度 ---------------- */

    @Test
    fun sortingByCostKeepsDesktopDefault() {
        val rows = listOf(
            row("cheap", calls = 100, tokens = 1_000, cost = 0.1),
            row("pricey", calls = 1, tokens = 10, cost = 9.9),
        )
        assertEquals(listOf("pricey", "cheap"), rows.sortedWith(usageBreakdownComparator(UsageSortKey.COST)).map { it.name })
        assertEquals("按费用", UsageSortKey.COST.label)
    }

    @Test
    fun sortingByCallsAndTokensUsesDifferentKeys() {
        val rows = listOf(
            row("a", calls = 10, tokens = 5_000, cost = 1.0),
            row("b", calls = 90, tokens = 100, cost = 2.0),
            row("c", calls = 5, tokens = 9_000, cost = 3.0),
        )
        assertEquals(
            listOf("b", "a", "c"),
            rows.sortedWith(usageBreakdownComparator(UsageSortKey.CALLS)).map { it.name },
        )
        assertEquals(
            listOf("c", "a", "b"),
            rows.sortedWith(usageBreakdownComparator(UsageSortKey.TOKENS)).map { it.name },
        )
        // 三个维度必须是三套不同结果（否则"排序"只是换了个标签）。
        assertNotEquals(
            rows.sortedWith(usageBreakdownComparator(UsageSortKey.CALLS)),
            rows.sortedWith(usageBreakdownComparator(UsageSortKey.TOKENS)),
        )
    }

    @Test
    fun sortingIsStableOnTies() {
        // 同费用时用调用数做次级键（与桌面 `|| (b.calls||0)-(a.calls||0)` 一致）。
        val rows = listOf(
            row("fewer", calls = 1, tokens = 10, cost = 2.0),
            row("more", calls = 9, tokens = 90, cost = 2.0),
        )
        assertEquals(
            listOf("more", "fewer"),
            rows.sortedWith(usageBreakdownComparator(UsageSortKey.COST)).map { it.name },
        )
        // 全等值时顺序保持不变（稳定排序，不随机跳动）。
        val same = listOf(row("x", 1, 1, 1.0), row("y", 1, 1, 1.0))
        assertEquals(
            listOf("x", "y"),
            same.sortedWith(usageBreakdownComparator(UsageSortKey.COST)).map { it.name },
        )
    }
}
