package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 会话内容检索。
 *
 * 契约：`GET /api/sessions/search` → `{hits:[{id,before,match,after}], searched, total, cut}`。
 *
 * 这组测试的重点不是"能解析"，而是**只解析 id、拒绝正文片段**：
 * `before/match/after` 是命中位置前后各 48 字符的会话正文。手机端不该展示会话正文，
 * 所以 DTO 里根本不该有这三个字段——解析进来就等于给了"顺手渲染"的机会。
 */
class SessionSearchApiTest {
    private val body = """
        {"searched":12,"total":3,"cut":false,"unread":false,
         "hits":[
           {"id":"aaaaaaaa-1111-2222","before":"用户说：帮我改一下部署脚本","match":"部署","after":"脚本在 tools 目录"},
           {"id":"bbbbbbbb-3333-4444","before":"前面","match":"部署","after":"后面"}
         ]}
    """.trimIndent()

    @Test
    fun keepsOnlySessionIds() {
        val result = parseSessionContentSearch(body)
        assertEquals(2, result.hits.size)
        assertEquals("aaaaaaaa-1111-2222", result.hits[0].sessionId)
        assertEquals(12, result.searched)
        assertEquals(3, result.total)
        assertTrue(!result.cut)
    }

    @Test
    fun contentSnippetsNeverEnterTheModel() {
        val result = parseSessionContentSearch(body)
        val rendered = sessionContentSearchLine(result) +
            result.hits.joinToString(" ") { it.sessionId }
        // before/match/after 里的正文片段一个都不许出现
        listOf("帮我改一下部署脚本", "脚本在 tools 目录", "前面", "后面").forEach { snippet ->
            assertTrue("正文片段「$snippet」不该流入手机端模型", !rendered.contains(snippet))
        }
    }

    @Test
    fun summaryCountsWithoutQuotingContent() {
        val line = sessionContentSearchLine(parseSessionContentSearch(body))
        assertTrue(line.contains("12"))
        assertTrue(line.contains("2"))
        assertTrue(line.contains("3"))
        assertTrue("不该出现正文", !line.contains("部署"))
    }

    @Test
    fun emptyHitsSayNoMatch() {
        val line = sessionContentSearchLine(parseSessionContentSearch("""{"searched":7,"total":0,"hits":[]}"""))
        assertTrue(line.contains("没找到"))
        assertTrue(line.contains("7"))
    }

    @Test
    fun noSessionsAtAll() {
        val line = sessionContentSearchLine(parseSessionContentSearch("""{"searched":0,"total":0,"hits":[]}"""))
        assertTrue(line.contains("还没有可搜索的会话"))
    }

    @Test
    fun cutIsDisclosed() {
        val line = sessionContentSearchLine(
            parseSessionContentSearch("""{"searched":80,"total":5,"cut":true,"hits":[{"id":"x"}]}"""),
        )
        assertTrue("截断必须说明：$line", line.contains("上限"))
    }

    @Test
    fun sessionIdIsShortenedForDisplay() {
        assertEquals("abcdefgh…", shortenSessionId("abcdefgh-1234"))
        assertEquals("short", shortenSessionId("short"))
    }

    @Test
    fun malformedBodyRaisesInvalidResponse() {
        for (b in listOf("not-json", "")) {
            val error = runCatching { parseSessionContentSearch(b) }.exceptionOrNull()
            assertTrue(error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }
}
