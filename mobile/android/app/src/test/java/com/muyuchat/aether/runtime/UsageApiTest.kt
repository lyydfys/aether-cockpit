package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class UsageApiTest {
    @Test
    fun parsesRealUsageShape() {
        val summary = parseUsage(
            """
            {
              "period": "today",
              "calls": 12, "errors": 1,
              "input": 1000, "output": 500,
              "cache_read": 200, "cache_write": 50,
              "cost": 0.0123,
              "models": [
                {"id":"gpt-5","name":"gpt-5","calls":8,"input":800,"output":400,"cost":0.01},
                {"id":"claude-sonnet","name":"claude-sonnet","calls":4,"input":200,"output":100,"cost":0.0023}
              ],
              "series": [
                {"label":"Nov 2","time":"2026-11-02T00:00:00+08:00","calls":12,"errors":1,"cost":0.0123}
              ]
            }
            """.trimIndent(),
        )
        assertEquals("today", summary.period)
        assertEquals(12L, summary.calls)
        assertEquals(1L, summary.errors)
        assertEquals(1000L, summary.inputTokens)
        assertEquals(500L, summary.outputTokens)
        assertEquals(200L, summary.cacheReadTokens)
        assertEquals(0.0123, summary.cost, 0.000001)
        assertEquals(2, summary.models.size)
        assertEquals("gpt-5", summary.models[0].id)
        assertEquals(8L, summary.models[0].calls)
        assertEquals(1, summary.series.size)
        assertEquals("Nov 2", summary.series[0].label)
        assertEquals(12L, summary.series[0].calls)
    }

    @Test
    fun emptyUsageIsValidAndMissingTotalsAreRejected() {
        val empty = parseUsage(
            """{"period":"7d","calls":0,"errors":0,"input":0,"output":0,"cost":0}""",
        )
        assertEquals(0L, empty.calls)
        assertTrue(empty.models.isEmpty())
        assertTrue(empty.series.isEmpty())

        for (body in listOf("not json", "{}", """{"models":[]}""")) {
            val error = runCatching { parseUsage(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }
}
