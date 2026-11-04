package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.RequestsModelSeries
import com.muyuchat.aether.runtime.RequestsModelSeriesPoint
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 请求页趋势图的纯几何/归一化逻辑。
 *
 * 这些函数只做"数值 → 画布像素"，不碰 Compose：柱宽、分组并排、折线、面积闭合、
 * x 轴标签稀疏化都在这里定死，两档字号（1.0×/1.3×）共用同一套坐标。
 */
class RequestsChartMathTest {

    @Test
    fun peakIgnoresEmptyAndAllZero() {
        assertEquals(0.0, requestsChartPeak(emptyList()), 1e-9)
        assertEquals(0.0, requestsChartPeak(listOf(0.0, 0.0)), 1e-9)
        assertEquals(5.0, requestsChartPeak(listOf(0.0, 5.0, 2.0)), 1e-9)
        // 负值不可能来自服务端，但真出现了也不能当峰值画反。
        assertEquals(3.0, requestsChartPeak(listOf(-2.0, 3.0)), 1e-9)
    }

    @Test
    fun failureRatioIsShareOfCallsNotBoolean() {
        assertEquals(0f, requestsFailureRatio(calls = 0, errors = 0), 1e-6f)
        // 没有调用时不能算 100% 失败。
        assertEquals(0f, requestsFailureRatio(calls = 0, errors = 3), 1e-6f)
        assertEquals(0f, requestsFailureRatio(calls = 10, errors = 0), 1e-6f)
        assertEquals(0.25f, requestsFailureRatio(calls = 8, errors = 2), 1e-6f)
        // 脏数据（失败数 > 调用数）夹到 1，不画出超出柱子的红块。
        assertEquals(1f, requestsFailureRatio(calls = 2, errors = 9), 1e-6f)
    }

    @Test
    fun barRectsFillWidthOncePerValue() {
        val bars = requestsBarRects(listOf(1.0, 2.0, 0.0), max = 2.0, width = 300f, height = 100f)
        assertEquals(3, bars.size)
        // 每天 100px 槽宽，柱宽 70px（gap 0.3），第一个柱子从 15 开始。
        assertEquals(15f, bars[0].left, 1e-3f)
        assertEquals(85f, bars[0].right, 1e-3f)
        assertEquals(100f, bars[0].bottom, 1e-3f)
        // 高度按比例：1/2 → 50px；2/2 → 100px（顶到画布顶）；0 → 高度 0。
        assertEquals(50f, bars[0].bottom - bars[0].top, 1e-3f)
        assertEquals(100f, bars[1].bottom - bars[1].top, 1e-3f)
        assertEquals(0f, bars[2].bottom - bars[2].top, 1e-3f)
        // 三根柱子互不重叠，且横向都在画布内。
        assertTrue(bars[0].right <= bars[1].left + 1e-3f)
        assertTrue(bars[1].right <= bars[2].left + 1e-3f)
        assertTrue(bars[2].right <= 300f + 1e-3f)
    }

    @Test
    fun barRectsDegradeSafely() {
        assertTrue(requestsBarRects(emptyList(), 1.0, 100f, 100f).isEmpty())
        assertTrue(requestsBarRects(listOf(1.0), 1.0, 0f, 100f).isEmpty())
        assertTrue(requestsBarRects(listOf(1.0), 1.0, 100f, 0f).isEmpty())
        // 峰值为 0（全 0 的一天）不能除零，也不画高度。
        val flat = requestsBarRects(listOf(0.0, 0.0), 0.0, 100f, 50f)
        assertEquals(2, flat.size)
        assertTrue(flat.all { it.bottom - it.top == 0f })
    }

    @Test
    fun groupedBarsSitSideBySideInsideTheirDaySlot() {
        val bars = requestsGroupedBarRects(
            values = listOf(4.0, 2.0),
            max = 4.0,
            width = 200f,
            height = 100f,
            groupCount = 2,
            groupIndex = 1,
        )
        assertEquals(2, bars.size)
        // 每天槽宽 100，组内可用 78（gap 0.22），每根 39 宽。
        assertEquals(39f, bars[0].right - bars[0].left, 1e-3f)
        // 第二根在第一天槽内的右侧，第一天结束于 x=100。
        assertTrue(bars[0].left > 11f)
        assertTrue(bars[0].right <= 100f + 1e-3f)
        // 非法分组直接返回空，不抛异常。
        assertTrue(requestsGroupedBarRects(listOf(1.0), 1.0, 100f, 100f, 0, 0).isEmpty())
        assertTrue(requestsGroupedBarRects(listOf(1.0), 1.0, 100f, 100f, 2, 2).isEmpty())
        assertTrue(requestsGroupedBarRects(emptyList(), 1.0, 100f, 100f, 2, 0).isEmpty())
    }

    @Test
    fun lineDotsSpanFullWidthAndCenterSinglePoint() {
        val two = requestsLineDots(listOf(0.0, 4.0), max = 4.0, width = 100f, height = 50f)
        assertEquals(2, two.size)
        assertEquals(0f, two[0].x, 1e-3f)
        assertEquals(100f, two[1].x, 1e-3f)
        // 0 → 贴底；4/4 → 顶到画布顶。
        assertEquals(50f, two[0].y, 1e-3f)
        assertEquals(0f, two[1].y, 1e-3f)

        val one = requestsLineDots(listOf(2.0), max = 4.0, width = 100f, height = 50f)
        assertEquals(1, one.size)
        assertEquals(50f, one[0].x, 1e-3f)
        assertEquals(25f, one[0].y, 1e-3f)

        assertTrue(requestsLineDots(emptyList(), 1.0, 100f, 50f).isEmpty())
        assertTrue(requestsLineDots(listOf(1.0), 1.0, 0f, 50f).isEmpty())
    }

    @Test
    fun areaDotsCloseThePolygonOnTheBaseline() {
        val dots = requestsAreaDots(listOf(1.0, 2.0), max = 2.0, width = 100f, height = 50f)
        // 2 个折线点 + 2 个闭合点。
        assertEquals(4, dots.size)
        assertEquals(100f, dots[2].x, 1e-3f)
        assertEquals(50f, dots[2].y, 1e-3f)
        assertEquals(0f, dots[3].x, 1e-3f)
        assertEquals(50f, dots[3].y, 1e-3f)
        assertTrue(requestsAreaDots(emptyList(), 1.0, 100f, 50f).isEmpty())
    }

    @Test
    fun labelIndicesStaySparseAndKeepBothEnds() {
        // 少于上限：全画。
        assertEquals(listOf(0, 1, 2), requestsChartLabelIndices(3, maxLabels = 4))
        assertEquals(emptyList<Int>(), requestsChartLabelIndices(0))
        // 多于上限：最多 4 个，首尾必含，严格递增且不重复。
        val many = requestsChartLabelIndices(30, maxLabels = 4)
        assertEquals(4, many.size)
        assertEquals(0, many.first())
        assertEquals(29, many.last())
        assertEquals(many.distinct(), many)
        assertEquals(many.sorted(), many)
        // 上限被夹在 2..12：传 1 也至少两个端点（否则只剩一个标签，轴就读不出来）。
        val atLeastTwo = requestsChartLabelIndices(10, maxLabels = 1)
        assertEquals(2, atLeastTwo.size)
        assertEquals(listOf(0, 9), atLeastTwo)
    }

    @Test
    fun legendListsSeriesNamesAndTheFixedCostLine() {
        val series = listOf(
            RequestsModelSeries("qwen3-max", listOf(RequestsModelSeriesPoint("2026-11-04", 2L, 0.3))),
            RequestsModelSeries("其他", emptyList()),
        )
        assertEquals(listOf("qwen3-max", "其他"), requestsChartLegendNames(series))
        assertTrue(REQUESTS_CHART_COST_LEGEND.contains("成本"))
        assertTrue(REQUESTS_CHART_COST_LEGEND.contains("右轴"))
    }

    @Test
    fun toggleLabelsMatchDesktopWording() {
        assertEquals("总计", RequestsChartMetric.TOTAL.label)
        assertEquals("按模型", RequestsChartMetric.BY_MODEL.label)
        assertEquals("柱状", RequestsChartForm.BARS.label)
        assertEquals("面积", RequestsChartForm.AREA.label)
        assertFalse(RequestsChartMetric.entries.map { it.label }.toSet().size != 2)
    }
}
