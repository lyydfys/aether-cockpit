package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 会话统计（`GET /api/sessions/stats`）。
 *
 * 契约：`{count, median, p90, days, messages, top:{…}, shape:{…}}`。
 * 只取 5 个数值——`top`/`shape` 里是会话级明细（可能带标题/目录），手机端不解析。
 */
class SessionStatsApiTest {
    private val body = """
        {"count":12,"messages":345,"median":1024,"p90":8192,"days":7,
         "top":{"tokens":[{"title":"私有标题","cwd":"C:/Users/x/proj"}],"cost":[]},
         "shape":{"a":1}}
    """.trimIndent()

    @Test
    fun parsesCounts() {
        val stats = parseSessionStats(body)
        assertEquals(12, stats.sessions)
        assertEquals(345, stats.messages)
        assertEquals(1024L, stats.medianTokens)
        assertEquals(8192L, stats.p90Tokens)
        assertEquals(7, stats.days)
    }

    @Test
    fun sessionLevelDetailIsNeverParsed() {
        // top 里带会话标题与目录：不该进入模型，也就不可能被顺手渲染出来
        val stats = parseSessionStats(body)
        val rendered = sessionStatsLine(stats) + sessionStatsTokensLine(stats)
        assertTrue(!rendered.contains("私有标题"))
        assertTrue(!rendered.contains("C:/Users"))
    }

    @Test
    fun summaryIsCountsOnly() {
        val line = sessionStatsLine(parseSessionStats(body))
        assertTrue(line.contains("12"))
        assertTrue(line.contains("345"))
        assertTrue(line.contains("7"))
        // 统计是快照，不写"实时"
        assertTrue(!line.contains("实时"))
    }

    @Test
    fun emptyWindowIsHonest() {
        val stats = parseSessionStats("""{"count":0,"messages":0,"median":0,"p90":0,"days":7}""")
        assertTrue(sessionStatsLine(stats).contains("最近 7 天没有会话记录"))
        assertTrue(sessionStatsTokensLine(stats).contains("没有 token 样本"))
    }

    @Test
    fun zeroDayWindowNeverSaysZeroDays() {
        // 真机上服务端在无样本时回 days=0，别写成"最近 0 天"
        val stats = parseSessionStats("""{"count":0,"messages":0,"median":0,"p90":0,"days":0}""")
        val line = sessionStatsLine(stats)
        assertTrue("不能出现「0 天」：$line", !line.contains("0 天"))
        assertTrue(line.contains("本机没有会话记录"))
    }

    @Test
    fun sessionsWithoutTokenSamplesDoNotClaimZero() {
        val stats = parseSessionStats("""{"count":3,"messages":0,"days":7}""")
        assertTrue(sessionStatsTokensLine(stats).contains("还没有 token 统计"))
    }

    @Test
    fun malformedBodyRaisesInvalidResponse() {
        for (b in listOf("not-json", "")) {
            val error = runCatching { parseSessionStats(b) }.exceptionOrNull()
            assertTrue(error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }
}
