package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 用量细分的三个只读解析器。
 *
 * 结构全部按**真实响应**核对过（不是猜字段）：
 * - `GET /api/usage/heatmap` → `{days:[{date,calls,tokens,cost}], from, to}`
 * - `GET /api/usage/quotas/history?days=N` → `[{provider,user,lines:[{name,points:[{at,left,start?,resetsAt?}]}]}]`
 * - `GET /api/pricing` → `{currency,usd_cny,defaults:[{pattern,price:{input_per_m,output_per_m,cached_read_per_m,cached_write_per_m},source}],overrides:{...}}`
 */
class UsageDetailApiTest {
    @Test
    fun parsesHeatmapDays() {
        val report = parseUsageHeatmap(
            """{"days":[{"date":"2026-10-27","calls":2,"tokens":42,"cost":0},
                        {"date":"2026-11-04","calls":1,"tokens":16,"cost":0.00048}],
                "from":"2025-10-30","to":"2026-11-04"}""",
        )
        assertEquals("2025-10-30", report.from)
        assertEquals("2026-11-04", report.to)
        assertEquals(2, report.days.size)
        assertEquals("2026-10-27", report.days[0].date)
        assertEquals(2, report.days[0].calls)
        assertEquals(42L, report.days[0].tokens)
        assertEquals(0.0, report.days[0].costUsd, 1e-9)
        assertEquals(0.00048, report.days[1].costUsd, 1e-9)
    }

    @Test
    fun heatmapFillsMissingDaysWithZeros() {
        // 服务端只回有数据的天；图上要按"今天往前 N 天"铺满，缺的那天补 0，不能跳格。
        val report = parseUsageHeatmap(
            """{"days":[{"date":"2026-11-03","calls":5,"tokens":100,"cost":0.5}],"from":"","to":""}""",
        )
        val strip = report.recentDays(3, today = "2026-11-04")
        assertEquals(listOf("2026-11-02", "2026-11-03", "2026-11-04"), strip.map { it.date })
        assertEquals(0, strip[0].calls)
        assertEquals(5, strip[1].calls)
        assertTrue(!strip[1].placeholder)
        assertTrue(strip[0].placeholder)
        assertTrue(strip[2].placeholder)
    }

    @Test
    fun parsesQuotaHistory() {
        val list = parseQuotaHistory(
            """[{"provider":"workbuddy-cn","user":"ly²","lines":[
                 {"name":"5 hours","points":[
                   {"at":"2026-10-27T16:38:03Z","left":100,"resetsAt":"2026-10-27T21:38:03Z"},
                   {"at":"2026-10-27T17:07:34Z","left":75}]}]},
               {"provider":"antigravity","user":"me","lines":[
                 {"name":"weekly","points":[{"at":"2026-10-27T16:38:03Z","left":20,"start":"2026-10-27T00:00:00Z"}]}]}]""",
        )
        assertEquals(2, list.size)
        assertEquals("workbuddy-cn", list[0].provider)
        assertEquals("ly²", list[0].user)
        assertEquals(2, list[0].lines[0].points.size)
        assertEquals(100, list[0].lines[0].points[0].left)
        assertEquals("2026-10-27T21:38:03Z", list[0].lines[0].points[0].resetsAt)
        assertEquals(20, list[1].lines[0].points[0].left)
    }

    @Test
    fun quotaLatestPointIsTheLastOne() {
        val list = parseQuotaHistory(
            """[{"provider":"p","user":"u","lines":[{"name":"5 hours","points":[
                 {"at":"2026-10-27T16:00:00Z","left":90},
                 {"at":"2026-10-27T17:00:00Z","left":40},
                 {"at":"2026-10-27T18:00:00Z","left":35}]}]}]""",
        )
        assertEquals(35, list[0].lines[0].latest()?.left)
    }

    @Test
    fun parsesPricingWithOverrides() {
        val pricing = parsePricing(
            """{"currency":"USD","usd_cny":7.1,
                "defaults":[{"pattern":"claude-*","price":{"input_per_m":3,"output_per_m":15,
                             "cached_read_per_m":0.3,"cached_write_per_m":3.75},"source":"official"}],
                "overrides":{"gpt-5":{"input_per_m":1.25,"output_per_m":10}}}""",
        )
        assertEquals("USD", pricing.currency)
        assertEquals(7.1, pricing.usdCny, 1e-9)
        assertEquals(1, pricing.defaults.size)
        val row = pricing.defaults[0]
        assertEquals("claude-*", row.pattern)
        assertEquals(3.0, row.inputPerM, 1e-9)
        assertEquals(15.0, row.outputPerM, 1e-9)
        assertEquals(3.75, row.cachedWritePerM, 1e-9)
        assertEquals("official", row.source)
        // overrides 是 map（模型名 → 价格），按名字排序并标记来源
        assertEquals(1, pricing.overrides.size)
        assertEquals("gpt-5", pricing.overrides[0].pattern)
        assertEquals(1.25, pricing.overrides[0].inputPerM, 1e-9)
        assertEquals("custom", pricing.overrides[0].source)
    }

    @Test
    fun moneyFormattingIsSharedAndHonest() {
        assertEquals("$0", formatMoney(0.0, "USD"))
        assertEquals("$0.0005", formatMoney(0.00048, "USD"))
        assertEquals("$1.23", formatMoney(1.234, "USD"))
        assertEquals("¥1.23", formatMoney(1.234, "CNY"))
        assertEquals("$12.00", formatMoney(12.0, "USD"))
        // 未知币种不要假装是美元
        assertEquals("12.00 XYZ", formatMoney(12.0, "XYZ"))
    }

    @Test
    fun emptyAndMalformedBodies() {
        assertTrue(parseUsageHeatmap("""{"days":[]}""").days.isEmpty())
        assertTrue(parseQuotaHistory("[]").isEmpty())
        assertEquals("USD", parsePricing("""{}""").currency)

        for (body in listOf("not-json", "")) {
            for (call in listOf<() -> Any>(
                { parseUsageHeatmap(body) },
                { parseQuotaHistory(body) },
                { parsePricing(body) },
            )) {
                val error = runCatching { call() }.exceptionOrNull()
                assertTrue("body=$body 应报 invalid_response", error is ManagementApiException)
                assertEquals("invalid_response", (error as ManagementApiException).code)
            }
        }
        assertNull(runCatching { parsePricing("""{"currency":"CNY"}""") }.exceptionOrNull())
    }
}
