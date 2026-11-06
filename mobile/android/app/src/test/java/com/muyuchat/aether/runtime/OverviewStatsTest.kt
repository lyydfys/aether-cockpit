package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 概览页 KPI 的数据映射（对标桌面 `overview.js:157-164`）。
 *
 * 四条口径必须与桌面一致，否则同一台手机上两页会打架：
 * - 可用账号 = `/api/providers` 的 readyCount 之和；注解是「真实登录态 / 测试渠道」拆分
 * - 今日请求 = `summary.calls`；注解「失败 N · 成功率 X%」
 * - 今日 Token = `input + output + cache_read + cache_write`（桌面 `overview.js:148-149`）
 * - 会话     = `GET /api/sessions/stats` 的 count；注解「本机记录 · 只读」
 */
class OverviewStatsTest {
    private fun summary(
        calls: Long = 0,
        errors: Long = 0,
        input: Long = 0,
        output: Long = 0,
        cacheRead: Long = 0,
        cacheWrite: Long = 0,
        cost: Double = 0.0,
        unpriced: Long = 0,
    ) = UsageSummary(
        period = "today",
        calls = calls,
        errors = errors,
        inputTokens = input,
        outputTokens = output,
        cacheReadTokens = cacheRead,
        cacheWriteTokens = cacheWrite,
        cost = cost,
        models = emptyList(),
        series = emptyList(),
        unpriced = unpriced,
    )

    private fun provider(
        id: String,
        accounts: Int,
        ready: Int,
        name: String = id,
    ) = ProviderSummary(
        id = id,
        name = name,
        description = "",
        accountCount = accounts,
        readyCount = ready,
        ready = ready > 0,
        off = false,
        brandColor = "",
    )

    @Test
    fun mapsFourKpisWithDesktopTokenScope() {
        val stats = overviewStats(
            usage = summary(calls = 7, errors = 2, input = 1200, output = 300, cacheRead = 999, cost = 0.0123),
            providers = listOf(provider("claude", accounts = 3, ready = 2)),
            sessions = 12,
            nowMs = 1_700_000_000_000L,
        )
        // 主数字是磁盘账号总数（3），注解里写"其中 2 个可用"。
        assertEquals("3", stats.accounts)
        assertTrue(stats.captions[0], stats.captions[0].contains("其中 2 个可用"))
        assertEquals("7", stats.calls)
        // 1200 + 999(缓存读) + 0 + 300 = 2499 → 2.5K（桌面把缓存算进 Token 总量）
        assertEquals("2.5K", stats.tokens)
        assertEquals("12", stats.sessions)
        assertEquals(1_700_000_000_000L, stats.updatedAtMs)
        assertEquals(4, stats.captions.size)
    }

    /**
     * 渠道未就绪时账号不能从概览上消失。
     *
     * 场景（真机复现）：accounts.json 里有 1 个账号，但它所属的自定义渠道没注册，
     * 账号进不了内存池。旧口径只数内存池 → 概览显示 0，用户以为"渠道被清零了"。
     */
    @Test
    fun unloadedAccountsStillCountedOnTheOverview() {
        val stats = overviewStats(
            usage = summary(calls = 0),
            providers = emptyList(),
            sessions = 0,
            accountTotals = ProviderAccountTotals(
                accountTotal = 1,
                readyTotal = 0,
                unloaded = listOf(
                    UnloadedAccountSummary(
                        accountId = "custom-e2e-1",
                        providerId = "custom-e2e",
                        label = "我的渠道",
                    ),
                ),
            ),
        )
        // 磁盘上有 1 个账号 → 主数字是 1，不是 0。
        assertEquals("1", stats.accounts)
        assertTrue(stats.captions[0], stats.captions[0].contains("其中 0 个可用"))
        assertTrue(stats.captions[0], stats.captions[0].contains("1 个渠道未就绪"))
    }

    /** 老服务端没有 account_total：回落到渠道行合计，行为与改动前一致。 */
    @Test
    fun fallsBackToProviderRowsWhenTotalsMissing() {
        val stats = overviewStats(
            usage = summary(calls = 0),
            providers = listOf(provider("claude", accounts = 3, ready = 2)),
            sessions = 0,
        )
        assertEquals("3", stats.accounts)
        assertTrue(stats.captions[0], !stats.captions[0].contains("渠道未就绪"))
    }

    @Test
    fun captionsCarryTheDesktopWording() {
        val stats = overviewStats(
            usage = summary(calls = 10, errors = 2, input = 100, cacheRead = 50, cost = 0.5, unpriced = 3),
            providers = listOf(provider("claude", accounts = 2, ready = 2)),
            sessions = 1,
        )
        assertTrue(stats.captions[0], stats.captions[0].contains("真实登录态"))
        assertTrue(stats.captions[0], stats.captions[0].contains("测试渠道"))
        assertTrue(stats.captions[1], stats.captions[1].contains("失败 2"))
        assertTrue(stats.captions[1], stats.captions[1].contains("80.0%"))
        assertTrue(stats.captions[2], stats.captions[2].contains("缓存"))
        assertTrue(stats.captions[2], stats.captions[2].contains("命中"))
        assertTrue(stats.captions[2], stats.captions[2].contains("成本"))
        assertTrue(stats.captions[2], stats.captions[2].contains("未计价 3 次"))
        assertEquals("本机记录 · 只读", stats.captions[3])
    }

    @Test
    fun testProvidersAreCountedSeparately() {
        val stats = overviewStats(
            usage = summary(calls = 1),
            providers = listOf(
                provider("claude", accounts = 2, ready = 2),
                provider("mock-fast", accounts = 1, ready = 1),
            ),
            sessions = 0,
        )
        assertTrue(stats.captions[0], stats.captions[0].contains("2 个真实登录态"))
        assertTrue(stats.captions[0], stats.captions[0].contains("1 个测试渠道"))
        assertEquals("3", stats.accounts)
        assertEquals("0", stats.sessions)
    }

    @Test
    fun zeroValuesRenderAsZeroNotPlaceholder() {
        val stats = overviewStats(usage = summary(), providers = emptyList(), sessions = 0)
        assertEquals("0", stats.calls)
        assertEquals("0", stats.tokens)
        assertEquals("0", stats.accounts)
        assertEquals("0", stats.sessions)
        stats.captions.forEach { caption ->
            assertTrue("不能退回阶段占位：$caption", !caption.contains("Stage"))
        }
    }

    @Test
    fun eachFailureOnlyAffectsItsOwnKpi() {
        val usageFail = overviewStats(
            usage = null,
            providers = listOf(provider("claude", accounts = 1, ready = 1)),
            sessions = 4,
            usageError = "读取失败",
        )
        assertEquals("—", usageFail.calls)
        assertEquals("—", usageFail.tokens)
        assertEquals("1", usageFail.accounts)
        assertEquals("4", usageFail.sessions)
        assertEquals("读不到", usageFail.captions[1])

        val accountsFail = overviewStats(
            usage = summary(calls = 2, input = 10, output = 5),
            providers = null,
            sessions = 1,
            accountsError = "读取失败",
        )
        assertEquals("—", accountsFail.accounts)
        assertEquals("读不到", accountsFail.captions[0])
        assertEquals("2", accountsFail.calls)

        val sessionsFail = overviewStats(
            usage = summary(calls = 2),
            providers = emptyList(),
            sessions = null,
            sessionsError = "读取失败",
        )
        assertEquals("—", sessionsFail.sessions)
        assertEquals("读不到", sessionsFail.captions[3])
    }

    @Test
    fun allThreeFailuresShowDashesEverywhere() {
        val stats = overviewStats(
            usage = null,
            providers = null,
            sessions = null,
            usageError = "x",
            accountsError = "y",
            sessionsError = "z",
        )
        assertEquals(
            listOf("—", "—", "—", "—"),
            listOf(stats.accounts, stats.calls, stats.tokens, stats.sessions),
        )
        assertEquals(listOf("读不到", "读不到", "读不到", "读不到"), stats.captions)
    }

    @Test
    fun testProviderRuleMatchesDesktop() {
        // 桌面 overview.js:30：`/^mock/.test(id) || id === 'openai-compat'`——只看前缀
        assertTrue(isTestProviderId("mock-fast"))
        assertTrue(isTestProviderId("openai-compat"))
        assertTrue(!isTestProviderId("claude"))
        // 只认前缀：名字里含 mock 但不在开头的渠道不算测试渠道
        assertTrue(!isTestProviderId("acme-mock"))
    }

    @Test
    fun ratesBecomeNullWhenTheDenominatorIsZero() {
        assertEquals(null, usageSuccessRatePercent(0, 0))
        assertEquals(80.0, usageSuccessRatePercent(10, 2)!!, 0.0001)
        // 脏数据（失败数 > 请求数）不能给出负数
        assertEquals(0.0, usageSuccessRatePercent(2, 5)!!, 0.0001)
        assertEquals(null, usageCacheHitRatePercent(0, 0, 0))
        assertEquals(33.3333, usageCacheHitRatePercent(100, 50, 0)!!, 0.001)
        assertEquals("—", percentText(null))
        assertEquals("80.0%", percentText(80.0))
    }
}
