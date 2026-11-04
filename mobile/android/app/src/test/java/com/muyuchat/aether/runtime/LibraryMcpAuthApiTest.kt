package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * MCP 登录/登出的请求与回包契约。
 *
 * 端点（`gateway.go:1006-1010`）：
 * - `POST /api/library/mcp-signin` `{"name"}` → `{id,state,url}` 或 `{id:"",state:"done",signedIn:true}`
 *   （`mcp_signin.go:279-395`，已登录的短路在 `:296-299`）
 * - `GET /api/library/mcp-signin/{id}` → `{id,state,url,name,error?}`（`:479-492`）
 * - `POST /api/library/mcp-signin/{id}/cancel`（`:494-509`）
 * - `POST /api/library/mcp-signout` `{"name"}`（`:511-...`）
 */
class LibraryMcpAuthApiTest {
    @Test
    fun pathsMatchTheRegisteredRoutes() {
        assertEquals("/api/library/mcp-signin", mcpSigninPath())
        assertEquals("/api/library/mcp-signin/flow-1", mcpSigninPollPath("flow-1"))
        assertEquals("/api/library/mcp-signin/flow-1/cancel", mcpSigninCancelPath("flow-1"))
        assertEquals("/api/library/mcp-signout", mcpSignoutPath())
        // flow id 进路径必须编码，不能拼出新路径
        assertEquals("/api/library/mcp-signin/a%2Fb", mcpSigninPollPath("a/b"))
        assertEquals("/api/library/mcp-signin/a%2Fb/cancel", mcpSigninCancelPath("a/b"))
    }

    @Test
    fun requestBodiesCarryOnlyTheServerName() {
        assertEquals("""{"name":"github"}""", buildMcpSigninJson("  github  "))
        assertEquals("""{"name":"github"}""", buildMcpSignoutJson("github"))
        // 不夹带任何凭据字段
        assertTrue(!buildMcpSigninJson("github").contains("token"))
    }

    @Test
    fun parsesWaitingStart() {
        val start = parseMcpSigninStart("""{"id":"flow-1","state":"waiting","url":"https://auth.example/authorize?x=1"}""")
        assertEquals("flow-1", start.id)
        assertEquals("waiting", start.state)
        assertEquals("https://auth.example/authorize?x=1", start.url)
        assertTrue(!start.alreadySignedIn)
    }

    @Test
    fun parsesAlreadySignedInShortCircuit() {
        val start = parseMcpSigninStart("""{"id":"","state":"done","signedIn":true}""")
        assertEquals("", start.id)
        assertEquals("done", start.state)
        assertTrue(start.alreadySignedIn)
    }

    @Test
    fun parsesPollStates() {
        val waiting = parseMcpSigninStatus("""{"id":"flow-1","state":"waiting","url":"https://a","name":"github"}""")
        assertEquals("waiting", waiting.state)
        assertEquals("github", waiting.name)
        assertEquals("", waiting.error)

        val done = parseMcpSigninStatus("""{"id":"flow-1","state":"done","name":"github"}""")
        assertEquals("done", done.state)

        // 轮询过期 flow 时服务端回 404 + state=failed + error
        val failed = parseMcpSigninStatus("""{"error":"没有这次登录","state":"failed"}""")
        assertEquals("failed", failed.state)
        assertEquals("没有这次登录", failed.error)

        val canceled = parseMcpSigninStatus("""{"state":"canceled"}""")
        assertEquals("canceled", canceled.state)
    }

    @Test
    fun malformedPayloadsAreInvalidResponse() {
        for (body in listOf("not json", "", "[]")) {
            val startError = runCatching { parseMcpSigninStart(body) }.exceptionOrNull()
            assertTrue("start body=$body", startError is ManagementApiException)
            assertEquals("invalid_response", (startError as ManagementApiException).code)

            val pollError = runCatching { parseMcpSigninStatus(body) }.exceptionOrNull()
            assertTrue("poll body=$body", pollError is ManagementApiException)
            assertEquals("invalid_response", (pollError as ManagementApiException).code)
        }
    }

    /** 缺 state 时兜底成 waiting/failed，而不是空串让 UI 无状态可判。 */
    @Test
    fun missingStateFallsBackSafely() {
        assertEquals("waiting", parseMcpSigninStart("""{"id":"x","url":"u"}""").state)
        assertEquals("failed", parseMcpSigninStatus("""{"id":"x"}""").state)
    }
}
