package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class RequestsApiTest {
    @Test
    fun parsesRealLedgerRowsWithoutRequestBodies() {
        val page = parseRequests(
            """
            {
              "rows": [
                {"id":"r1","t":1793600000000,"day":"2026-11-02","agent":"codex",
                 "model":"gpt-5","providerId":"openai","providerName":"OpenAI",
                 "in":1000,"out":500,"cost":0.012,"ms":850,"ttft_ms":120,
                 "status":200,"req":"gpt-5","session":"s1"},
                {"id":"r2","t":1793600100000,"day":"2026-11-02","agent":"claude",
                 "model":"claude-sonnet","providerId":"claude-sub","providerName":"Claude",
                 "in":200,"out":0,"cost":0,"ms":400,
                 "status":429,"err_type":"rate","err":"upstream detail"}
              ],
              "offset":0,"limit":20,"total":21,"avg_ms":625,"p95_ms":800,
              "calls":21,"errors":1,"cost":0.012
            }
            """.trimIndent(),
        )
        assertEquals(2, page.rows.size)
        assertEquals(0, page.offset)
        assertEquals(20, page.limit)
        assertEquals(21L, page.total)
        assertEquals(625L, page.avgMs)
        assertEquals(1L, page.errors)
        assertEquals("gpt-5", page.rows[0].model)
        assertEquals("OpenAI", page.rows[0].providerName)
        assertEquals(200, page.rows[0].status)
        assertEquals(1000L, page.rows[0].inputTokens)
        assertEquals(850L, page.rows[0].ms)
        assertTrue(page.rows[0].ok)
        assertFalse(page.rows[1].ok)
        assertEquals(429, page.rows[1].status)
        assertEquals("rate", page.rows[1].errorType)
        assertEquals("", page.rows[0].errorType)
    }

    @Test
    fun parsesDesktopRowColumnsAndFallsBackToDefaults() {
        val page = parseRequests(
            """
            {
              "rows": [
                {"id":"r1","t":1793600000000,"model":"gpt-5","req":"gpt-5-mini",
                 "providerId":"openai","providerName":"OpenAI","accountId":"a1","accountName":"主账号",
                 "kind":"openai_chat","agent":"codex","agentName":"Codex","session":"s1",
                 "stream":true,"ttft_ms":120,"cache_read":30,"cache_write":10,"reasoning":7,
                 "priced":true,"pricing_model":"gpt-5","err":"","tries":2,"retried":true,
                 "route_id":9,"fast_path":true,"items":3,"upstream_truncated":true,
                 "est_tokens":900,"purpose":"chat","in":100,"out":50,"cost":0.5,"ms":900,"status":200},
                {"id":"r2","t":1,"status":0}
              ],
              "offset":0,"limit":20,"total":2,
              "input":100,"output":50,"cache_read":30,"cache_write":10,"unpriced":4,"reasoning":7
            }
            """.trimIndent(),
        )
        val first = page.rows[0]
        assertEquals("openai_chat", first.kind)
        assertEquals("gpt-5-mini", first.requestedModel)
        assertEquals("a1", first.accountId)
        assertEquals("主账号", first.accountName)
        assertEquals("Codex", first.agent)
        assertEquals("s1", first.session)
        assertTrue(first.stream)
        assertEquals(120L, first.ttftMs)
        assertEquals(30L, first.cacheRead)
        assertEquals(10L, first.cacheWrite)
        assertEquals(7L, first.reasoningTokens)
        assertTrue(first.priced)
        assertEquals("gpt-5", first.pricingModel)
        assertEquals(2, first.tries)
        assertTrue(first.retried)
        assertEquals(9L, first.routeId)
        assertTrue(first.fastPath)
        assertEquals(3, first.items)
        assertTrue(first.upstreamTruncated)
        assertEquals(900L, first.estimatedTokens)
        assertEquals("chat", first.purpose)
        // 汇总字段（KPI 用）。
        assertEquals(100L, page.inputTokens)
        assertEquals(50L, page.outputTokens)
        assertEquals(4L, page.unpriced)

        // 缺字段一律回落默认值，不抛。
        val sparse = page.rows[1]
        assertEquals("", sparse.kind)
        assertEquals(0L, sparse.ttftMs)
        assertFalse(sparse.priced)
        assertEquals(0, sparse.tries)
        assertEquals(0L, sparse.routeId)
    }

    @Test
    fun emptyLedgerIsValidAndMalformedBodiesAreRejected() {
        val empty = parseRequests("""{"rows":[],"offset":0,"limit":20,"total":0}""")
        assertTrue(empty.rows.isEmpty())
        assertEquals(0L, empty.total)

        for (body in listOf("not json", "{}", """{"rows":"oops","total":0}""")) {
            val error = runCatching { parseRequests(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }

    @Test
    fun defaultPathKeepsLegacyBehaviour() {
        // 不传 period/query 时必须逐字等于旧实现的 URL。
        assertEquals(
            "/api/usage/requests?period=today&offset=40&limit=20",
            buildRequestsPath(offset = 40, limit = 20, failedOnly = false),
        )
        assertEquals(
            "/api/usage/requests?period=today&offset=0&limit=20&failed=1",
            buildRequestsPath(offset = 0, limit = 20, failedOnly = true),
        )
    }

    @Test
    fun periodAndQueryAreThreadedIntoTheUrl() {
        assertEquals(
            "/api/usage/requests?period=7d&offset=0&limit=500",
            buildRequestsPath(offset = 0, limit = 500, failedOnly = false, period = "7d"),
        )
        // q 需要 URL 编码（空格、中文、斜杠）。
        assertEquals(
            "/api/usage/requests?period=30d&offset=0&limit=500&q=claude+sonnet",
            buildRequestsPath(offset = 0, limit = 500, failedOnly = false, period = "30d", query = " claude sonnet "),
        )
        assertTrue(
            buildRequestsPath(offset = 0, limit = 20, failedOnly = false, query = "中文 模型")
                .contains("q=%E4%B8%AD%E6%96%87+%E6%A8%A1%E5%9E%8B"),
        )
    }

    @Test
    fun invalidPeriodFallsBackToTodayAndLimitIsClamped() {
        assertEquals(
            "/api/usage/requests?period=today&offset=0&limit=20",
            buildRequestsPath(offset = 0, limit = 20, failedOnly = false, period = "90d"),
        )
        // 服务端 ledgerPageMax = 500，超出按 500 收；0/负数按 1 收。
        assertTrue(buildRequestsPath(offset = 0, limit = 9_999, failedOnly = false).contains("limit=500"))
        assertTrue(buildRequestsPath(offset = 0, limit = 0, failedOnly = false).contains("limit=1"))
        assertTrue(buildRequestsPath(offset = -5, limit = 20, failedOnly = false).contains("offset=0"))
    }

    @Test
    fun blankQueryIsNotAppended() {
        assertEquals(
            buildRequestsPath(offset = 0, limit = 20, failedOnly = false),
            buildRequestsPath(offset = 0, limit = 20, failedOnly = false, query = "   "),
        )
    }
}
