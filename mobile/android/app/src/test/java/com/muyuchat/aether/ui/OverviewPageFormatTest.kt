package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.GatewayState
import com.muyuchat.aether.runtime.GatewayStatus
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 概览页的排版纯逻辑：数字拆分成「前缀/数字/单位」、状态词、最近一次请求的正文。
 *
 * 这些函数只服务排版，不改数值：断言的重点是"数字没被改写、单位没被猜出来"。
 */
class OverviewPageFormatTest {

    @Test
    fun splitsMagnitudeSuffixFromNumber() {
        assertEquals(StatFocusParts("", "1.5", "K"), statFocusParts("1.5K"))
        assertEquals(StatFocusParts("", "1.2", "M"), statFocusParts("1.2M"))
        assertEquals(StatFocusParts("", "12", ""), statFocusParts("12"))
        assertEquals(StatFocusParts("", "0", ""), statFocusParts("0"))
    }

    @Test
    fun splitsCurrencyPrefixFromNumber() {
        assertEquals(StatFocusParts("\$", "0.0123", ""), statFocusParts("\$0.0123"))
        assertEquals(StatFocusParts("\$", "0", ""), statFocusParts("\$0"))
        assertEquals(StatFocusParts("¥", "12.50", ""), statFocusParts("¥12.50"))
    }

    @Test
    fun keepsDashAndEmptyValueHonest() {
        // 读不到 = "—"，不许变成 0，也不许加单位。
        assertEquals(StatFocusParts("", "—", ""), statFocusParts("—"))
        assertEquals(StatFocusParts("", "—", ""), statFocusParts("   "))
        // 认不出的写法原样留在数字里，不猜单位。
        assertEquals(StatFocusParts("", "n/a", ""), statFocusParts("n/a"))
    }

    @Test
    fun statNoteOnlyKeepsMissingDataSignal() {
        // 卡片标题已经交代了"今日"，captions 里的范围词不该再占一行。
        assertEquals("", statNoteOrBlank("今日"))
        assertEquals("", statNoteOrBlank("已启用"))
        assertEquals("", statNoteOrBlank(null))
        // 缺失信号必须留下。
        assertEquals("读不到", statNoteOrBlank("读不到"))
        assertEquals("读不到", statNoteOrBlank(" 读不到 "))
    }

    @Test
    fun gatewayHeadlineNamesTheRealState() {
        assertEquals("网关正在运行", gatewayStateHeadline(GatewayStatus(state = GatewayState.RUNNING)))
        // 启动失败 != 未启动：错误码要能被看到。
        assertEquals("启动失败", gatewayStateHeadline(GatewayStatus(state = GatewayState.FAILED)))
        assertEquals("网关未启动", gatewayStateHeadline(GatewayStatus(state = GatewayState.STOPPED)))
    }

    @Test
    fun gatewayDetailCarriesErrorCodeInsteadOfUnknown() {
        val failed = gatewayStateDetail(
            GatewayStatus(state = GatewayState.FAILED, errorCode = "listen_failed"),
        )
        assertTrue(failed, failed.contains("listen_failed"))
        val blank = gatewayStateDetail(GatewayStatus(state = GatewayState.FAILED))
        assertTrue(blank, blank.contains("unknown"))
        val running = gatewayStateDetail(GatewayStatus(state = GatewayState.RUNNING))
        assertTrue(running, running.contains("回环"))
        val stopped = gatewayStateDetail(GatewayStatus(state = GatewayState.STOPPED))
        assertTrue(stopped, stopped.contains("127.0.0.1"))
    }

    @Test
    fun actionLabelDescribesWhatTheTapDoes() {
        assertEquals("停止网关", gatewayActionLabel(GatewayStatus(state = GatewayState.RUNNING)))
        assertEquals("启动网关", gatewayActionLabel(GatewayStatus(state = GatewayState.STOPPED)))
        assertEquals("启动网关", gatewayActionLabel(GatewayStatus(state = GatewayState.FAILED)))
    }

    @Test
    fun latestRequestBodyDropsOnlyTheCardTitleEcho() {
        assertEquals(
            "3 分钟前 · gpt-5 · 成功",
            latestRequestBody("最近一次：3 分钟前 · gpt-5 · 成功"),
        )
        // 读不到时原文照旧，不许被去掉前缀后变成空串。
        assertEquals("读不到请求记录", latestRequestBody("读不到请求记录"))
        assertEquals("还没有客户端连过本机网关", latestRequestBody("还没有客户端连过本机网关"))
    }
}
