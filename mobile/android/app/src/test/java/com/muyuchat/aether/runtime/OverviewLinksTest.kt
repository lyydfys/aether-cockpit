package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 概览新增「三步接入」「聚合链路」的纯数据合同。
 *
 * 覆盖：桌面文案原样、三步真实完成判定、providers/usage 两侧独立失败、
 * 端点路径与桌面一致、`/api/usage` 的 `providers[]` 解析。
 */
class OverviewLinksTest {
    @Test
    fun onboardingStepsKeepDesktopCopyAndTrackRealCompletion() {
        val steps = overviewOnboardingSteps(
            running = true,
            linksLoaded = true,
            accountsKnown = true,
            accountCount = 0,
            usageKnown = true,
            calls = 0L,
        )
        assertEquals(3, steps.size)
        assertEquals("设置网关密钥", steps[0].title)
        assertEquals("本机回环可免；开放局域网必填", steps[0].description)
        assertEquals("添加第一个渠道账号", steps[1].title)
        assertEquals("订阅渠道按提示登录，或添加 OpenAI 兼容账号", steps[1].description)
        assertEquals("复制接入片段", steps[2].title)
        assertEquals("把网关地址与密钥填进客户端", steps[2].description)

        assertTrue(steps[0].done) // 网关运行 → 本机会话接管
        assertFalse(steps[1].done) // 0 个账号
        assertFalse(steps[2].done) // 今日 0 次请求
        assertTrue(steps[0].status.contains("本机回环免密钥"))
        assertTrue(steps[1].status.contains("还没有渠道账号"))
        assertTrue(steps[2].status.contains("发一次请求"))
    }

    @Test
    fun onboardingStepsTurnGreenAsAccountsAndClientRequestsAppear() {
        val withAccounts = overviewOnboardingSteps(
            running = true,
            linksLoaded = true,
            accountsKnown = true,
            accountCount = 2,
            usageKnown = true,
            calls = 0L,
        )
        assertTrue(withAccounts[1].done)
        assertTrue(withAccounts[1].status.contains("2 个账号"))

        val withRequest = overviewOnboardingSteps(
            running = true,
            linksLoaded = true,
            accountsKnown = true,
            accountCount = 2,
            usageKnown = true,
            calls = 3L,
        )
        assertTrue(withRequest[2].done)
        assertTrue(withRequest[2].status.contains("客户端已接入"))
    }

    @Test
    fun onboardingStepsStayPendingWhenGatewayIsOff() {
        val steps = overviewOnboardingSteps(
            running = false,
            linksLoaded = false,
            accountsKnown = false,
            accountCount = null,
            usageKnown = false,
            calls = 0L,
        )
        assertTrue(steps.none { it.done })
        assertTrue(steps.all { it.status.isNotBlank() })
        assertTrue(steps[2].status.contains("启动网关"))
    }

    @Test
    fun onboardingStepsDoNotClaimCompletionWhenReadsFailOrAreLoading() {
        val failed = overviewOnboardingSteps(
            running = true,
            linksLoaded = true,
            accountsKnown = false,
            accountCount = null,
            usageKnown = false,
            calls = 0L,
        )
        assertFalse(failed[1].done)
        assertFalse(failed[2].done)
        assertTrue(failed[1].status.contains("读不到"))
        assertTrue(failed[2].status.contains("读不到"))

        val loading = overviewOnboardingSteps(
            running = true,
            linksLoaded = false,
            accountsKnown = false,
            accountCount = null,
            usageKnown = false,
            calls = 0L,
        )
        assertTrue(loading[1].status.contains("正在读取"))
        assertTrue(loading[2].status.contains("正在读取"))
    }

    @Test
    fun overviewLinksMergesProviderAndUsageRowsByProviderId() {
        val providers = listOf(
            provider("p1", "Alpha", accountCount = 2, readyCount = 1),
            provider("p2", "Empty vendor", accountCount = 0, readyCount = 0),
            provider("p3", "Off vendor", accountCount = 1, readyCount = 0, off = true),
        )
        val usage = usage(
            calls = 12,
            providerRows = listOf(
                UsageProviderRow("p1", "Alpha", calls = 10, errors = 1, cost = 0.5),
                UsageProviderRow("p3", "Off vendor", calls = 2, errors = 2, cost = 0.0),
            ),
        )

        val state = overviewLinks(providers, usage)
        assertTrue(state.providersLoaded)
        assertTrue(state.usageLoaded)
        assertEquals(3, state.totalAccounts)
        assertEquals(12L, state.calls)
        assertEquals(listOf("p1", "p3"), state.providers.map { it.id })
        assertEquals(
            OverviewProviderLink(
                id = "p1",
                name = "Alpha",
                accountCount = 2,
                readyCount = 1,
                off = false,
                calls = 10,
                errors = 1,
                cost = 0.5,
            ),
            state.providers[0],
        )
        assertTrue(state.providers[1].off)
        assertEquals(2L, state.providers[1].errors)
    }

    @Test
    fun overviewLinksHandlesProvidersAndUsageFailuresIndependently() {
        val usage = usage(calls = 7, providerRows = emptyList())
        val noProviders = overviewLinks(providers = null, usage = usage)
        assertFalse(noProviders.providersLoaded)
        assertNull(noProviders.totalAccounts)
        assertTrue(noProviders.usageLoaded)
        assertEquals(7L, noProviders.calls)

        val noUsage = overviewLinks(providers = listOf(provider("p1", "Alpha", 1, 0)), usage = null)
        assertTrue(noUsage.providersLoaded)
        assertEquals(1, noUsage.totalAccounts)
        assertFalse(noUsage.usageLoaded)
        assertEquals(0L, noUsage.providers.single().calls)

        val both = overviewLinks(providers = null, usage = null)
        assertFalse(both.providersLoaded)
        assertFalse(both.usageLoaded)
        assertNull(both.totalAccounts)
    }

    @Test
    fun overviewLinksKeepsKnownEmptyProviderSetDistinctFromFailure() {
        val state = overviewLinks(providers = emptyList(), usage = usage(calls = 0, providerRows = emptyList()))
        assertTrue(state.providersLoaded)
        assertEquals(0, state.totalAccounts)
        assertTrue(state.providers.isEmpty())
        assertTrue(state.usageLoaded)
    }

    @Test
    fun endpointsMatchDesktopPaths() {
        val endpoints = overviewEndpoints("http://127.0.0.1:7860/")
        assertEquals(listOf("OpenAI Base", "Anthropic Base", "Gemini Base"), endpoints.map { it.label })
        assertEquals("http://127.0.0.1:7860/v1", endpoints[0].url)
        assertEquals("http://127.0.0.1:7860", endpoints[1].url)
        assertEquals("http://127.0.0.1:7860/v1beta", endpoints[2].url)

        assertTrue(overviewEndpoints("").all { it.url.isEmpty() })
    }

    @Test
    fun parseUsageParsesProviderRowsAndToleratesMissingArray() {
        val parsed = parseUsage(
            """
            {
              "period": "today",
              "calls": 5,
              "errors": 1,
              "input": 0,
              "output": 0,
              "cost": 0.25,
              "providers": [
                {"id":"p1","name":"Alpha","calls":5,"errors":1,"cost":0.25},
                {"id":"","name":"ignored","calls":1}
              ]
            }
            """.trimIndent(),
        )
        assertEquals(1, parsed.providers.size)
        assertEquals("p1", parsed.providers[0].id)
        assertEquals(5L, parsed.providers[0].calls)
        assertEquals(0.25, parsed.providers[0].cost, 0.0001)

        val withoutProviders = parseUsage("""{"period":"7d","calls":0,"errors":0}""")
        assertTrue(withoutProviders.providers.isEmpty())
    }

    private fun provider(
        id: String,
        name: String,
        accountCount: Int,
        readyCount: Int,
        off: Boolean = false,
    ) = ProviderSummary(
        id = id,
        name = name,
        description = "",
        accountCount = accountCount,
        readyCount = readyCount,
        ready = readyCount > 0,
        off = off,
        brandColor = "",
    )

    private fun usage(calls: Long, providerRows: List<UsageProviderRow>) = UsageSummary(
        period = "today",
        calls = calls,
        errors = 0,
        inputTokens = 0,
        outputTokens = 0,
        cacheReadTokens = 0,
        cacheWriteTokens = 0,
        cost = 0.0,
        models = emptyList(),
        series = emptyList(),
        providers = providerRows,
    )
}
