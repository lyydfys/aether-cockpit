package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 概览页「最近一次模型请求」文案。
 *
 * 这行字回答的是"这台手机的网关到底被客户端用过没有"，
 * 所以三种状态必须能分清：**没记录 / 读不到 / 有记录（含失败）**。
 */
class LatestRequestLineTest {
    private val now = 1_700_000_000_000L

    private fun row(
        timeMs: Long,
        model: String = "workbuddy-cn/auto",
        status: Int = 200,
        errorType: String = "",
    ) = RequestRow(
        id = "r$timeMs",
        timeMs = timeMs,
        model = model,
        providerName = "",
        status = status,
        errorType = errorType,
        inputTokens = 18,
        outputTokens = 4,
        ms = 800,
        cost = 0.0,
    )

    @Test
    fun noRequestsSaysNoClientYet() {
        val line = latestRequestLine(emptyList(), now)
        assertTrue(line.contains("还没有客户端连过"))
        // 不许用含糊词冒充
        assertTrue(!line.contains("未知"))
        assertTrue(!line.contains("未验证"))
    }

    @Test
    fun picksTheNewestRecord() {
        val rows = listOf(
            row(now - 10 * 60_000L, model = "old/model"),
            row(now - 2 * 60_000L, model = "workbuddy-cn/auto"),
            row(now - 60 * 60_000L, model = "older/model"),
        )
        val line = latestRequestLine(rows, now)
        assertTrue("应取最新一条：$line", line.contains("workbuddy-cn/auto"))
        assertTrue(!line.contains("old/model"))
        assertTrue(line.contains("2 分钟前"))
        assertTrue(line.contains("成功"))
    }

    @Test
    fun failureIsShownNotHidden() {
        val line = latestRequestLine(listOf(row(now - 120_000, status = 502, errorType = "upstream_failed")), now)
        assertTrue("失败必须显示：$line", line.contains("失败"))
        assertTrue(line.contains("502"))
        assertTrue(line.contains("upstream_failed"))
    }

    @Test
    fun blankModelDoesNotProduceEmptySlot() {
        val line = latestRequestLine(listOf(row(now - 5_000, model = "")), now)
        assertTrue(line.contains("未知模型"))
    }

    @Test
    fun statusWithoutHttpCodeStillSaysFailed() {
        val line = latestRequestLine(listOf(row(now - 5_000, status = 0)), now)
        assertTrue(line.contains("失败"))
        assertTrue(!line.contains("HTTP 0"))
    }

    @Test
    fun unavailableIsDistinctFromEmpty() {
        val line = latestRequestUnavailableLine()
        assertTrue(line.contains("读不到"))
        assertTrue(!line.contains("还没有客户端"))
    }
}
