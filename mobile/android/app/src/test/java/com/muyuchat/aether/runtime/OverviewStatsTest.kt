package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 概览页四张统计卡的数据映射。
 *
 * 口径必须与「用量」页一致（否则同一台手机上两个页面会给出不同数字）：
 * - 请求数 = `summary.calls`
 * - Token  = `inputTokens + outputTokens`（**不含缓存**，与「用量」页一致）
 * - 成本   = `summary.cost`，格式 `$0` / `$0.0000`
 * - 已启用账号 = `/api/providers` 里 readyCount 之和
 */
class OverviewStatsTest {
    private fun summary(
        calls: Long = 0,
        input: Long = 0,
        output: Long = 0,
        cost: Double = 0.0,
        cacheRead: Long = 999,
    ) = UsageSummary(
        period = "today",
        calls = calls,
        errors = 0,
        inputTokens = input,
        outputTokens = output,
        cacheReadTokens = cacheRead,
        cacheWriteTokens = 0,
        cost = cost,
        models = emptyList(),
        series = emptyList(),
    )

    @Test
    fun mapsUsageAndAccounts() {
        val stats = overviewStats(usage = summary(calls = 7, input = 1200, output = 300, cost = 0.0123), enabledAccounts = 2)
        assertEquals("7", stats.calls)
        // 1200 + 300 = 1500 → 1.5K；缓存 999 **不计入**（与用量页口径一致）
        assertEquals("1.5K", stats.tokens)
        assertEquals("$0.0123", stats.cost)
        assertEquals("2", stats.accounts)
        assertTrue(stats.captions.all { it.isNotBlank() })
    }

    @Test
    fun zeroValuesRenderAsZeroNotPlaceholder() {
        val stats = overviewStats(usage = summary(), enabledAccounts = 0)
        assertEquals("0", stats.calls)
        assertEquals("0", stats.tokens)
        assertEquals("$0", stats.cost)
        assertEquals("0", stats.accounts)
        // 关键：不许出现阶段占位
        stats.captions.forEach { c ->
            assertTrue("不能退回阶段占位：$c", !c.contains("Stage"))
        }
    }

    @Test
    fun usageFailureOnlyAffectsItsOwnCards() {
        val stats = overviewStats(usage = null, enabledAccounts = 3, usageError = "读取失败")
        assertEquals("—", stats.calls)
        assertEquals("—", stats.tokens)
        assertEquals("—", stats.cost)
        // 账号那一侧照常显示真实值
        assertEquals("3", stats.accounts)
        assertTrue(stats.captions[0].contains("读不到"))
        assertTrue(!stats.captions[3].contains("读不到"))
    }

    @Test
    fun accountsFailureOnlyAffectsItsOwnCard() {
        val stats = overviewStats(
            usage = summary(calls = 2, input = 10, output = 5, cost = 0.5),
            enabledAccounts = null,
            accountsError = "读取失败",
        )
        assertEquals("2", stats.calls)
        assertEquals("—", stats.accounts)
        assertTrue(stats.captions[3].contains("读不到"))
        assertTrue(!stats.captions[1].contains("读不到"))
    }

    @Test
    fun bothFailuresShowDashesEverywhere() {
        val stats = overviewStats(usage = null, enabledAccounts = null)
        assertEquals(listOf("—", "—", "—", "—"), listOf(stats.calls, stats.tokens, stats.cost, stats.accounts))
    }

    @Test
    fun captionsCarryTheScope() {
        val stats = overviewStats(usage = summary(calls = 1, input = 1, output = 1), enabledAccounts = 1)
        // 前三张是"今日"口径，第四张是"已启用"
        assertTrue(stats.captions[0].contains("今日"))
        assertTrue(stats.captions[1].contains("今日"))
        assertTrue(stats.captions[2].contains("今日"))
        assertTrue(stats.captions[3].contains("已启用"))
    }
}
