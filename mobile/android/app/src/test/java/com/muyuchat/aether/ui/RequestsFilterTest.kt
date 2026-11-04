package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.RequestRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 日志页的本地筛选与分页（对齐桌面 `logs.js:25,133-141,192-259`）。
 *
 * 桌面一次取 500 行后在本地按「全部/成功/失败」筛选、每 20 行分页；
 * 这里把同一套规则做成纯函数，避免分页/计数在 UI 里各算一遍。
 */
class RequestsFilterTest {
    private fun row(status: Int, id: String = "r$status") = RequestRow(
        id = id,
        timeMs = 1_700_000_000_000,
        model = "m",
        providerName = "p",
        status = status,
        errorType = if (status >= 400) "other" else "",
        inputTokens = 1,
        outputTokens = 1,
        ms = 10,
        cost = 0.0,
    )

    private val rows = listOf(row(200), row(401), row(200), row(500))

    @Test
    fun statusFilterSplitsOkAndFailed() {
        assertEquals(4, requestsFilterRows(rows, RequestsStatus.ALL).size)
        assertEquals(2, requestsFilterRows(rows, RequestsStatus.OK).size)
        assertEquals(2, requestsFilterRows(rows, RequestsStatus.ERROR).size)
        assertTrue(requestsFilterRows(rows, RequestsStatus.OK).all { it.ok })
        assertTrue(requestsFilterRows(rows, RequestsStatus.ERROR).none { it.ok })
    }

    @Test
    fun unknownStatusFilterFallsBackToAll() {
        assertEquals(4, requestsFilterRows(rows, "whatever").size)
    }

    @Test
    fun pageCountIsAtLeastOne() {
        assertEquals(1, requestsPageCount(0))
        assertEquals(1, requestsPageCount(1))
        assertEquals(1, requestsPageCount(20))
        assertEquals(2, requestsPageCount(21))
        assertEquals(3, requestsPageCount(41))
    }

    @Test
    fun pageSliceIsTwentyPerPageAndClamped() {
        val many = (1..45).map { row(200, "r$it") }
        assertEquals(20, requestsPageSlice(many, 0).size)
        assertEquals("r21", requestsPageSlice(many, 1).first().id)
        assertEquals(5, requestsPageSlice(many, 2).size)
        // 越界页收敛到最后一页，不抛也不返回空。
        assertEquals(5, requestsPageSlice(many, 99).size)
        assertEquals("r1", requestsPageSlice(many, -3).first().id)
    }

    @Test
    fun emptyListStillYieldsOneEmptyPage() {
        assertTrue(requestsPageSlice(emptyList(), 0).isEmpty())
        assertEquals(1, requestsPageCount(0))
    }

    @Test
    fun periodLabelCoversDesktopThreeTabs() {
        assertEquals(listOf("today", "7d", "30d"), RequestsPeriods.map { it.first })
        assertEquals("今日", requestsPeriodLabel("today"))
        assertEquals("近 7 天", requestsPeriodLabel("7d"))
        assertEquals("近 30 天", requestsPeriodLabel("30d"))
    }

    @Test
    fun defaultQueryIsSevenDaysAndAll() {
        // 桌面 logs.js:110 的默认 range 是 7d、only 是 all。
        val query = RequestsQuery()
        assertEquals("7d", query.period)
        assertEquals(RequestsStatus.ALL, query.status)
        assertEquals("", query.search)
        assertEquals(false, query.failedOnly)
    }
}
