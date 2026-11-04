package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 「最近异常」只读视图的数据层。
 *
 * 契约（`trace.go`）：
 * - `GET /api/gateway/history` → `{days:[{day,requests}], routes:[], cut}`
 * - `GET /api/gateway/history?day=D` → `{days:[…], routes:[traceRoute], cut}`
 * - `traceRoute{id,agent,model,time,ms,status,done,tries:[{id,model,ms,status,fail,upstream}]}`
 *
 * 只挑**失败**的：任一 try 有 fail，或顶层 status >= 400。
 * `fail` 是上游错误串（可能含 URL），不含凭据与正文。
 */
class GatewayHistoryApiTest {
    @Test
    fun parsesDaysSortedAsGiven() {
        val days = parseHistoryDays(
            """{"days":[{"day":"2026-11-04","requests":12},{"day":"2026-11-03","requests":3}],"routes":[],"cut":false}""",
        )
        assertEquals(2, days.size)
        assertEquals("2026-11-04", days[0].day)
        assertEquals(12, days[0].requests)
    }

    @Test
    fun keepsOnlyRoutesThatActuallyFailed() {
        val snapshot = parseHistoryRoutes(
            """{"cut":false,"days":[],
                "routes":[
                  {"id":1,"agent":"claude","model":"gpt-x","time":1700000000000,"status":200,"done":true,
                   "tries":[{"id":"a","ms":120,"status":200,"done":true,"picked":true}]},
                  {"id":2,"agent":"codex","model":"gpt-y","time":1700000060000,"status":502,"done":true,
                   "tries":[
                     {"id":"b","ms":900,"status":429,"done":true,"fail":"rate limited"},
                     {"id":"c","ms":300,"status":200,"done":true,"picked":true}]}
                ]}""",
        )
        assertEquals(1, snapshot.failures.size)
        val failure = snapshot.failures[0]
        assertEquals("gpt-y", failure.model)
        assertEquals(502, failure.status)
        assertEquals(2, failure.attempts)
        assertEquals(listOf("rate limited"), failure.fails)
    }

    @Test
    fun statusOnlyFailureStillCounts() {
        val snapshot = parseHistoryRoutes(
            """{"routes":[{"id":3,"model":"m","time":1,"status":500,"done":true,
                 "tries":[{"id":"a","ms":5,"status":500,"done":true}]}]}""",
        )
        assertEquals(1, snapshot.failures.size)
        assertEquals(500, snapshot.failures[0].status)
        assertTrue(snapshot.failures[0].fails.isEmpty())
    }

    @Test
    fun summarySaysNothingRecentWhenEmpty() {
        val snapshot = parseHistoryRoutes("""{"routes":[]}""")
        val line = historySummaryLine(snapshot)
        assertTrue(line.contains("最近没有异常"))
        // 历史记录不许写成实时状态承诺
        assertTrue(!line.contains("当前正常"))
    }

    @Test
    fun summaryCountsAndWarnsAboutCutBuffer() {
        val snapshot = parseHistoryRoutes(
            """{"cut":true,"routes":[
                {"id":1,"model":"a","time":1,"status":500,"done":true,"tries":[{"id":"x","status":500,"fail":"boom"}]},
                {"id":2,"model":"b","time":2,"status":500,"done":true,"tries":[{"id":"y","status":500,"fail":"boom"}]}]}""",
        )
        val line = historySummaryLine(snapshot)
        assertTrue(line.contains("2"))
        assertTrue("缓冲被截断要说明：$line", line.contains("只保留"))
    }

    @Test
    fun relativeTimeIsHuman() {
        val now = 1_700_000_000_000L
        assertEquals("刚刚", relativeTimeLabel(now, now - 5_000))
        assertEquals("3 分钟前", relativeTimeLabel(now, now - 3 * 60_000))
        assertEquals("2 小时前", relativeTimeLabel(now, now - 2 * 3_600_000))
    }

    @Test
    fun malformedBodyRaisesInvalidResponse() {
        for (b in listOf("not-json", "")) {
            val error = runCatching { parseHistoryDays(b) }.exceptionOrNull()
            assertTrue(error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }
}
