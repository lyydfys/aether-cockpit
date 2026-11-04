package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 渠道连通性探测（服务端自己探，不把凭据发给手机端）。
 *
 * 为什么不是 `/api/provider/test`：那个端点的请求体是**带凭据的 draft**，
 * 手机端要么读不到凭据、要么得把凭据从 Go 侧搬到 Kotlin 侧——两样都不该做。
 * 正确的组合是 `POST /api/channels/check`（异步发起）+ `GET /api/channels/status`（读结果）。
 */
class ChannelProbeApiTest {
    private val statusBody = """
        {"reading":false,"checkedAt":1793730000,
         "entries":[
           {"provider":"workbuddy-cn","account":"acc-1","label":"ly²","model":"glm-5.3-flash",
            "ok":true,"code":200,"ms":860,"at":1793729999,
            "history":[{"at":1793729000,"ok":false,"ms":0,"error":"429 限流"},
                       {"at":1793729999,"ok":true,"ms":860}]},
           {"provider":"antigravity","account":"acc-2","label":"Antigravity (本机)",
            "ok":false,"code":0,"ms":0,"at":1793729998,
            "error":"dial tcp 74.125.203.95:443: i/o timeout"}
         ]}
    """.trimIndent()

    @Test
    fun parsesProbeStatusEntries() {
        val report = parseChannelProbeStatus(statusBody)
        assertEquals(false, report.reading)
        assertEquals(1793730000L, report.checkedAt)
        assertEquals(2, report.entries.size)
        val ok = report.entries.first { it.providerId == "workbuddy-cn" }
        assertTrue(ok.ok)
        assertEquals(200, ok.code)
        assertEquals(860L, ok.ms)
        assertEquals("glm-5.3-flash", ok.model)
        assertEquals("ly²", ok.label)
        val bad = report.entries.first { it.providerId == "antigravity" }
        assertEquals(false, bad.ok)
        assertTrue(bad.error.contains("i/o timeout"))
    }

    @Test
    fun entriesGroupByProviderForRowDisplay() {
        val report = parseChannelProbeStatus(statusBody)
        val byProvider = report.byProvider()
        assertEquals(2, byProvider.size)
        assertEquals(1, byProvider.getValue("workbuddy-cn").size)
        // 同一渠道多账号时按"最差的一条"代表整行，避免把失败藏起来。
        val mixed = parseChannelProbeStatus(
            """{"reading":false,"checkedAt":0,"entries":[
                 {"provider":"p","account":"a1","ok":true,"code":200,"ms":10,"at":1},
                 {"provider":"p","account":"a2","ok":false,"code":429,"ms":0,"at":2,"error":"rate limited"}]}""",
        ).byProvider()
        val row = mixed.getValue("p").first()
        assertEquals(false, row.ok)
        assertEquals(429, row.code)
    }

    @Test
    fun summaryLineNeverLies() {
        val ok = ChannelProbe("p", "a", "主账号", "m", true, 200, 860, 1793729999, "")
        assertEquals("成功 · 200 · 860ms", channelProbeSummary(ok))
        val bad = ChannelProbe("p", "a", "主账号", "m", false, 429, 0, 1793729999, "rate limited")
        assertTrue(channelProbeSummary(bad).startsWith("失败 · 429"))
        assertTrue(channelProbeSummary(bad).contains("rate limited"))
        // 没有状态码时不要凑一个 0 出来
        val unknown = ChannelProbe("p", "a", "", "", false, 0, 0, 0, "network")
        assertTrue(channelProbeSummary(unknown).startsWith("失败"))
        assertTrue(!channelProbeSummary(unknown).contains("· 0"))
    }

    @Test
    fun ageLabelIsRelativeAndHonest() {
        val now = 1_800_000_000L
        assertEquals("刚刚", channelProbeAgeLabel(0L, now))
        assertEquals("刚刚", channelProbeAgeLabel(now - 10, now))
        assertEquals("3 分钟前", channelProbeAgeLabel(now - 180, now))
        assertEquals("2 小时前", channelProbeAgeLabel(now - 7_200, now))
        assertEquals("刚刚", channelProbeAgeLabel(now + 60, now)) // 时钟回拨
    }

    @Test
    fun emptyStatusIsAnEmptyReport() {
        val report = parseChannelProbeStatus("""{"reading":false,"checkedAt":0,"entries":[]}""")
        assertTrue(report.entries.isEmpty())
        assertTrue(report.byProvider().isEmpty())
    }

    @Test
    fun malformedBodyRaisesInvalidResponse() {
        for (body in listOf("not-json", "")) {
            val error = runCatching { parseChannelProbeStatus(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }

    @Test
    fun checkRequestBodyOnlyCarriesProvider() {
        val json = org.json.JSONObject(buildChannelCheckJson("workbuddy-cn", ""))
        assertEquals("workbuddy-cn", json.getString("provider"))
        assertEquals(1, json.length())
    }
}
