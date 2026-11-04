package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.RequestRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 请求行的列序与单位合同（对齐桌面 `logs.js:205-243`）。
 *
 * 规则来自桌面列头：首字节 / 总耗时、输入 / 输出、输出速率、费用、状态；
 * 这些必须是纯函数行为，不能靠截图碰运气。
 */
class RequestsRowFormatTest {
    private val ok = RequestRow(
        id = "1-1",
        timeMs = 1_700_000_000_000,
        model = "claude-sub/claude-sonnet-4-6",
        providerName = "Claude 官方",
        status = 200,
        errorType = "",
        inputTokens = 12,
        outputTokens = 34,
        ms = 263,
        cost = 0.0,
    )

    private val failed = ok.copy(status = 401, errorType = "auth", providerName = "")

    @Test
    fun statusWordMatchesDesktopSpelling() {
        // 与桌面 logs.js:240 同形状：「成功 200」/「失败 401」。
        assertEquals("成功 200", requestStatusText(ok))
        assertEquals("失败 401", requestStatusText(failed))
    }

    @Test
    fun unknownStatusIsNotColouredAsSuccessOrFailure() {
        // status == 0 表示账本没记录状态码：只显示占位符，不能编成「成功」或「失败」。
        assertEquals("—", requestStatusText(ok.copy(status = 0)))
    }

    @Test
    fun ttftAndTotalShareOneCellWithDashForMissingValues() {
        // 桌面 logs.js:221,235：「{ttft} / {total} ms」；缺首字节写「—」而不是 0。
        // 移动端刻意再严一档：总耗时 <= 0（没记）也写「—」，不写「0 ms」——0 是量到了。
        assertEquals("— / 263 ms", requestTtftText(ok))
        assertEquals("120 / 263 ms", requestTtftText(ok.copy(ttftMs = 120)))
        assertEquals("— / —", requestTtftText(ok.copy(ms = 0, ttftMs = 0)))
    }

    @Test
    fun tokenPairUsesPromptTokensIncludingCache() {
        // 桌面 logs.js:6,222：提示 Token = 输入 + 缓存读 + 缓存写。
        assertEquals("12 / 34", requestTokenPairText(ok))
        assertEquals("1.1K / 34", requestTokenPairText(ok.copy(inputTokens = 1_000, cacheRead = 100)))
        assertEquals("—", requestTokenPairText(ok.copy(inputTokens = 0, outputTokens = 0)))
    }

    @Test
    fun outputSpeedUsesDecodeWindowNotTotalWhenTtftExists() {
        // 桌面 logs.js:17-23：有 TTFT 时只算首字节之后的解码时间。
        val row = ok.copy(outputTokens = 1_000, ms = 2_000, ttftMs = 1_000)
        assertEquals(1000.0, requestOutputSpeed(row), 0.001)
        assertEquals("1000.0 tok/s", requestSpeedText(row))
        // 没有 TTFT 时退回总耗时。
        assertEquals(500.0, requestOutputSpeed(row.copy(ttftMs = 0)), 0.001)
        assertEquals("—", requestSpeedText(ok.copy(outputTokens = 0)))
    }

    @Test
    fun costFollowsDesktopSmallAmountPrecision() {
        // 桌面 format.js:15-23：0 → $0；<0.01 → 6 位；<1 → 4 位；否则 2 位。
        assertEquals("$0", requestsCost(0.0))
        assertEquals("$0.001200", requestsCost(0.0012))
        assertEquals("$0.0120", requestsCost(0.012))
        assertEquals("$1.23", requestsCost(1.234))
        assertEquals("未计价", requestCostText(ok))
        assertEquals("$0.001200", requestCostText(ok.copy(priced = true, cost = 0.0012)))
    }

    @Test
    fun protocolLabelMirrorsDesktopMapping() {
        // 桌面 logs.js:8-14。
        assertEquals("chat", protocolLabel("openai_chat"))
        assertEquals("anthropic", protocolLabel("anthropic_messages"))
        assertEquals("responses", protocolLabel("openai_responses"))
        assertEquals("responses", protocolLabel("responses"))
        assertEquals("custom", protocolLabel("openai_custom"))
        assertEquals("http", protocolLabel(""))
    }

    @Test
    fun providerLineJoinsProviderAccountAgentAndFastPath() {
        // 桌面 logs.js:220。
        val line = requestProviderLine(
            ok.copy(accountName = "主账号", agent = "codex", fastPath = true),
        )
        assertEquals("Claude 官方 · 主账号 · codex · 快速直通", line)
        assertEquals("—", requestProviderLine(ok.copy(providerName = "", providerId = "", accountName = "")))
    }

    @Test
    fun providerFallsBackWithExplicitCopy() {
        assertEquals("Claude 官方", requestProviderText(ok))
        assertEquals("未知渠道", requestProviderText(failed))
    }

    @Test
    fun kpiLineMatchesDesktopFiveCells() {
        val page = com.muyuchat.aether.runtime.RequestsPage(
            rows = emptyList(),
            offset = 0,
            limit = 20,
            total = 21,
            avgMs = 625,
            p95Ms = 800,
            calls = 21,
            errors = 1,
            cost = 0.012,
            inputTokens = 1_000,
            outputTokens = 500,
            cacheRead = 200,
            unpriced = 2,
        )
        val kpis = requestsKpis(page, "7d")
        assertEquals(listOf("请求数", "成功 / 失败", "平均延迟", "Token", "费用"), kpis.map { it.label })
        assertEquals("21", kpis[0].value)
        assertEquals("已持久化 · 近 7 天", kpis[0].detail)
        assertEquals("20 / 1", kpis[1].value)
        assertTrue(kpis[1].detail.contains("4.8%"))
        assertEquals("625 ms", kpis[2].value)
        assertEquals("P95 800 ms", kpis[2].detail)
        assertEquals("1.7K", kpis[3].value)
        assertEquals("缓存读 200", kpis[3].detail)
        assertEquals("未计价 2 次", kpis[4].detail)
    }

    @Test
    fun tokenUnitsMatchDesktopKiloMegaGiga() {
        assertEquals("999", requestsTokens(999))
        assertEquals("1.0K", requestsTokens(1_000))
        assertEquals("1.5M", requestsTokens(1_500_000))
        assertEquals("2.0B", requestsTokens(2_000_000_000))
    }
}
