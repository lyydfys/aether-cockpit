package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 模型重定向（`GET /api/model-redirects`）。
 *
 * 契约：`{"redirects": {"虚拟名": {"渠道id": "上游模型名"}}}`。
 * 只读展示；改映射要 POST 同路径，留在桌面端。
 */
class ModelRedirectApiTest {
    private val body = """
        {"redirects":{
          "claude-sonnet-4-6":{"claude-sub":"claude-sonnet-4-6","kiro":"claude-3-7-sonnet"},
          "gpt-5":{"openai":"gpt-5-2025-08-07"},
          "empty":{}
        }}
    """.trimIndent()

    @Test
    fun parsesNestedMap() {
        val redirects = parseModelRedirects(body)
        // "empty" 没有任何目标 → 不占屏幕
        assertEquals(2, redirects.size)
        assertEquals("claude-sonnet-4-6", redirects[0].virtualName)
        assertEquals(2, redirects[0].targets.size)
        // 渠道按名字排序，输出稳定
        assertEquals("claude-sub", redirects[0].targets[0].provider)
        assertEquals("kiro", redirects[0].targets[1].provider)
        assertEquals("claude-3-7-sonnet", redirects[0].targets[1].upstream)
    }

    @Test
    fun lineShowsFirstTargetAndCount() {
        val redirects = parseModelRedirects(body)
        val line = modelRedirectLine(redirects[0])
        assertTrue(line.contains("claude-sonnet-4-6 → claude-sub/claude-sonnet-4-6"))
        assertTrue("多目标要提示：$line", line.contains("还有 1 个渠道"))
        val single = modelRedirectLine(redirects[1])
        assertTrue(!single.contains("还有"))
    }

    @Test
    fun summaryCounts() {
        assertTrue(modelRedirectSummary(parseModelRedirects(body)).contains("2 条重定向"))
        assertTrue(modelRedirectSummary(emptyList()).contains("没有配置"))
    }

    @Test
    fun missingRedirectsKeyMeansEmpty() {
        assertTrue(parseModelRedirects("""{"redirects":null}""").isEmpty())
        assertTrue(parseModelRedirects("""{}""").isEmpty())
    }

    @Test
    fun blankProviderOrUpstreamIsDropped() {
        val redirects = parseModelRedirects(
            """{"redirects":{"x":{"":"up","p":"","ok":"fine"}}}""",
        )
        assertEquals(1, redirects.size)
        assertEquals(1, redirects[0].targets.size)
        assertEquals("ok", redirects[0].targets[0].provider)
    }

    @Test
    fun malformedBodyRaisesInvalidResponse() {
        for (b in listOf("not-json", "", "[]")) {
            val error = runCatching { parseModelRedirects(b) }.exceptionOrNull()
            assertTrue("「$b」应被拒绝", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }
}
