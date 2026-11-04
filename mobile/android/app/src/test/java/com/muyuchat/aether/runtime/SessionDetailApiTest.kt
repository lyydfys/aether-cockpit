package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 会话详情的三个端点（桌面 `views/sessions.js:403-478`）：
 *
 * - `GET /api/sessions/one?key=<agent>:<id>` → `sessionUsageView`（`sessions_api.go:80-94`）
 * - `GET /api/sessions/transcript?agent=&id=` → `{parts,cut,source}`（`:203-219`）
 * - `GET /api/sessions/markdown?agent=&id=` → `text/markdown`（`:249-259`）
 */
class SessionDetailApiTest {
    @Test
    fun pathsMatchTheDesktopCalls() {
        assertEquals("/api/sessions/one?key=codex%3Aabc-1", sessionOnePath("codex", "abc-1"))
        assertEquals(
            "/api/sessions/transcript?agent=codex&id=abc-1",
            sessionTranscriptPath("codex", "abc-1"),
        )
        assertEquals(
            "/api/sessions/markdown?agent=codex&id=abc-1",
            sessionMarkdownPath("codex", "abc-1"),
        )
        // agent/id 里的特殊字符必须编码，不能拼出第二个查询参数
        assertEquals(
            "/api/sessions/transcript?agent=a%26b&id=c%3Dd",
            sessionTranscriptPath(" a&b ", "c=d"),
        )
    }

    @Test
    fun parsesUsageViewWithTotalsAndModels() {
        val view = parseSessionUsageView(
            """
            {"key":"codex:abc","id":"abc","agent":"codex","title":"修一个 Kotlin 编译错误",
             "cwd":"/repo","start":1762200000,"last":1762290000,
             "input":1000,"output":300,"cache_read":2000,"cache_write":500,
             "cost":0.1234,"priced":true,"unpriced":false,"requests":42,
             "messages":18,"tool_calls":7,"resume":"codex resume abc",
             "wsl":"Ubuntu","deletable":true,"gateway":false,
             "models":[{"model":"gpt-5-codex","input":1000,"output":300,"cost":0.1},
                       {"name":"legacy-model"}]}
            """.trimIndent(),
        )
        assertEquals("codex:abc", view.key)
        assertEquals("修一个 Kotlin 编译错误", view.title)
        assertEquals(42, view.requests)
        assertEquals(18, view.messages)
        assertEquals(7, view.toolCalls)
        assertEquals(0.1234, view.cost, 1e-9)
        assertTrue(view.priced)
        assertEquals("codex resume abc", view.resume)
        assertEquals("Ubuntu", view.wsl)
        assertTrue(view.deletable)
        // 桌面的合计口径：input + cache_read + cache_write + output（sessions.js:445）
        assertEquals(3800L, view.totalTokens)
        // 「输入」chip 含缓存（sessions.js:450）
        assertEquals(3500L, view.inputWithCache)
        assertEquals(2, view.models.size)
        assertEquals("gpt-5-codex", view.models[0].model)
        // 老数据可能只有 name 字段
        assertEquals("legacy-model", view.models[1].model)
    }

    @Test
    fun parsesTranscriptPreferringTextThenContentThenBody() {
        val transcript = parseSessionTranscript(
            """
            {"source":"file","cut":true,"parts":[
              {"role":"user","text":"第一段","at":1762290000},
              {"kind":"assistant","content":"第二段"},
              {"body":"第三段"},
              {"role":"tool","text":"   "},
              {}
            ]}
            """.trimIndent(),
        )
        assertEquals("file", transcript.source)
        assertTrue(transcript.cut)
        assertEquals(3, transcript.parts.size)
        assertEquals("user", transcript.parts[0].role)
        assertEquals("第一段", transcript.parts[0].text)
        assertEquals(1762290000L, transcript.parts[0].at)
        // role 缺省时用 kind
        assertEquals("assistant", transcript.parts[1].role)
        assertEquals("第二段", transcript.parts[1].text)
        // 都没有 role/kind 时给「记录」（桌面 sessions.js:469）
        assertEquals("记录", transcript.parts[2].role)
        assertEquals("第三段", transcript.parts[2].text)
    }

    @Test
    fun gatewaySourceAndEmptyPartsAreKeptAsIs() {
        val transcript = parseSessionTranscript("""{"source":"gateway","parts":[]}""")
        assertEquals("gateway", transcript.source)
        assertTrue(transcript.parts.isEmpty())
        assertTrue(!transcript.cut)
        // source 缺省按 file（桌面据此选空态文案）
        assertEquals("file", parseSessionTranscript("""{"parts":[]}""").source)
    }

    @Test
    fun malformedBodiesAreInvalidResponse() {
        for (body in listOf("not json", "", "[]")) {
            val oneError = runCatching { parseSessionUsageView(body) }.exceptionOrNull()
            assertTrue("one body=$body", oneError is ManagementApiException)
            assertEquals("invalid_response", (oneError as ManagementApiException).code)

            val transcriptError = runCatching { parseSessionTranscript(body) }.exceptionOrNull()
            assertTrue("transcript body=$body", transcriptError is ManagementApiException)
            assertEquals("invalid_response", (transcriptError as ManagementApiException).code)
        }
    }

    @Test
    fun missingFieldsFallBackToSafeDefaults() {
        val view = parseSessionUsageView("""{"key":"a:b"}""")
        assertNull(view.resume.takeIf { it.isNotEmpty() })
        assertEquals(0L, view.totalTokens)
        assertEquals(0, view.requests)
        assertTrue(view.models.isEmpty())
        assertTrue(!view.deletable)
    }
}
