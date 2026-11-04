package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.UsageSummary
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 用量页的排版纯逻辑：周期口径、成功率/缓存命中率、注解行、空态与错误文案。
 *
 * 断言口径与概览页一致：Token = 输入 + 输出（不含缓存）；没有请求时写 "—"，不写 0%。
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
    )

    @Test
    fun periodScopeLabelMatchesTheSelector() {
        assertEquals("今日", usagePeriodScopeLabel(UsagePeriod.TODAY))
        assertEquals("近 7 天", usagePeriodScopeLabel(UsagePeriod.WEEK))
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
    fun notesCarryFailureCountAndCacheRate() {
        val requests = usageRequestsNoteText(summary(calls = 10, errors = 2))
        assertTrue(requests, requests.contains("失败 2"))
        assertTrue(requests, requests.contains("80.0%"))

        val tokens = usageTokensNoteText(summary(input = 100, cacheRead = 50))
        assertTrue(tokens, tokens.contains("缓存"))
        assertTrue(tokens, tokens.contains("33.3%"))
        // Token 注解不许把缓存算进总量口径（这里只声明缓存与命中率）。
        assertFalse(tokens, tokens.contains("总"))
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
        val models = usageModelsEmptyHint()
        val series = usageSeriesEmptyHint()
        assertTrue(models, models.contains("下一步"))
        assertTrue(series, series.contains("下一步"))
        assertFalse(models, models.contains("暂无数据"))
        assertFalse(series, series.contains("暂无数据"))
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
