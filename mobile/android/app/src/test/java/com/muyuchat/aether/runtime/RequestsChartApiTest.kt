package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 趋势图的数据层：`GET /api/usage/requests` 的 `series[]` 与 `chartBy.model`。
 *
 * 契约（`internal/core/gateway/ledger.go`）：
 * - `series[]` = `{time,label,calls,errors,cost,…}`（`:387-399`）；
 * - `chartBy.model[]` = `{name, points:[{t,c,m}]}`（`:404-413`），服务端只给调用量前 5
 *   的模型，其余并成 `其他`（`:599-641`），每条 `points` 与 `series` 的天数对齐。
 *
 * 三条路径都要覆盖：正常、缺字段（老响应没有这两个字段）、坏点（缺 time/t 的条目跳过）。
 */
class RequestsChartApiTest {

    private fun payload(extra: String) = """
        {"rows":[],"offset":0,"limit":50,"total":0,
         "calls":3,"errors":1,"cost":0.5,
         "avg_ms":10,"p95_ms":20$extra}
    """.trimIndent()

    @Test
    fun parsesSeriesAndChartByModel() {
        val page = parseRequests(
            payload(
                """,
                 "series":[
                   {"time":"2026-11-03","label":"Nov 3","calls":2,"errors":1,"cost":0.3},
                   {"time":"2026-11-04","label":"Nov 4","calls":1,"errors":0,"cost":0.2}
                 ],
                 "chartBy":{"model":[
                   {"name":"qwen3-max","points":[{"t":"2026-11-03","c":2,"m":0.3},{"t":"2026-11-04","c":1,"m":0.2}]},
                   {"name":"其他","points":[{"t":"2026-11-03","c":0,"m":0.0},{"t":"2026-11-04","c":0,"m":0.0}]}
                 ]}
                """.trimIndent(),
            ),
        )
        assertEquals(2, page.series.size)
        assertEquals("2026-11-03", page.series[0].time)
        assertEquals("Nov 3", page.series[0].label)
        assertEquals(2L, page.series[0].calls)
        assertEquals(1L, page.series[0].errors)
        assertEquals(0.3, page.series[0].cost, 1e-9)

        assertEquals(2, page.modelSeries.size)
        assertEquals("qwen3-max", page.modelSeries[0].name)
        assertEquals(2, page.modelSeries[0].points.size)
        assertEquals(2L, page.modelSeries[0].points[0].calls)
        assertEquals(0.3, page.modelSeries[0].points[0].cost, 1e-9)
        assertEquals("其他", page.modelSeries[1].name)
    }

    @Test
    fun oldResponseWithoutSeriesStaysEmpty() {
        // 老服务端/老缓存里没有 series 与 chartBy：解析成空列表，页面按"没有趋势"处理。
        val page = parseRequests(payload(""))
        assertTrue(page.series.isEmpty())
        assertTrue(page.modelSeries.isEmpty())
        // 既有字段不受影响。
        assertEquals(3L, page.calls)
        assertEquals(1L, page.errors)
    }

    @Test
    fun chartByWithoutModelKeyIsEmptyNotAnError() {
        val page = parseRequests(payload(""", "series":[], "chartBy":{}"""))
        assertTrue(page.modelSeries.isEmpty())
    }

    @Test
    fun skipsMalformedPointsInsteadOfFailing() {
        val page = parseRequests(
            payload(
                """,
                 "series":[
                   {"label":"no time","calls":5},
                   {"time":"2026-11-04"},
                   {"time":"","calls":1}
                 ],
                 "chartBy":{"model":[
                   {"points":[{"t":"2026-11-04","c":1}]},
                   {"name":"","points":[{"t":"2026-11-04","c":1}]},
                   {"name":"ok","points":[{"c":9},{"t":"2026-11-04","c":2}]}
                 ]}
                """.trimIndent(),
            ),
        )
        // 只有带 time 的那条保留；label 缺失时回退成 time 本身。
        assertEquals(1, page.series.size)
        assertEquals("2026-11-04", page.series.single().time)
        assertEquals("2026-11-04", page.series.single().label)
        assertEquals(0L, page.series.single().calls)
        // 没有 name 的序列被丢掉；点里缺 t 的那个点被丢掉。
        assertEquals(1, page.modelSeries.size)
        assertEquals("ok", page.modelSeries.single().name)
        assertEquals(1, page.modelSeries.single().points.size)
        assertEquals(2L, page.modelSeries.single().points.single().calls)
    }

    @Test
    fun negativeNumbersAreClampedToZero() {
        val page = parseRequests(
            payload(
                """,
                 "series":[{"time":"2026-11-04","label":"x","calls":-5,"errors":-2,"cost":-1.0}],
                 "chartBy":{"model":[{"name":"m","points":[{"t":"2026-11-04","c":-3,"m":-0.5}]}]}
                """.trimIndent(),
            ),
        )
        assertEquals(0L, page.series.single().calls)
        assertEquals(0L, page.series.single().errors)
        assertEquals(0.0, page.series.single().cost, 1e-9)
        assertEquals(0L, page.modelSeries.single().points.single().calls)
        assertEquals(0.0, page.modelSeries.single().points.single().cost, 1e-9)
    }
}
