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
}
