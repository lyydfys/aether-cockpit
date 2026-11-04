package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.RequestRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 请求行右列与状态词的排版合同。
 *
 * 规则来自 UI_REFRESH_SPEC §1 与交付目标 6：状态词只染自己、耗时/Token 右对齐成一行、
 * 长模型名允许两行再省略——这些都必须是纯函数行为，不能靠截图碰运气。
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
    fun latencyAndTokensShareOneRightAlignedLine() {
        assertEquals("263 ms · 12/34 tok", requestLatencyTokensText(ok))
    }

    @Test
    fun missingLatencyDegradesToDashNotZero() {
        // 缺失耗时写「—」，不能显示 0 ms（0 是"测量了但为 0"，两者含义不同）。
        assertEquals("— · 12/34 tok", requestLatencyTokensText(ok.copy(ms = 0)))
        assertEquals("— · 0/0 tok", requestLatencyTokensText(ok.copy(ms = 0, inputTokens = 0, outputTokens = 0)))
    }

    @Test
    fun providerFallsBackWithExplicitCopy() {
        assertEquals("Claude 官方", requestProviderText(ok))
        assertEquals("未知渠道", requestProviderText(failed))
    }

    @Test
    fun detailKeepsItsOwnFallbackWording() {
        // 详情页沿用「未记录渠道」，与行内的「未知渠道」是两处既定文案，别合并。
        val fields = requestDetailFields(failed).associate { it.label to it.value }
        assertEquals("未记录渠道", fields["渠道"])
        assertTrue(fields.containsKey("耗时"))
    }
}
