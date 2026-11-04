package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 会话页的纯逻辑。
 *
 * 一条硬规则：**不能把桌面全路径透到手机上**（`/Users/alice/projects/x` 会泄露桌面用户名）。
 * 页面只需要「是哪个项目」这一层信息，所以这里只保留末级目录名。
 */
class SessionsApiTest {
    @Test
    fun directoryLabelKeepsOnlyTheLastSegment() {
        assertEquals("aether", sessionDirectoryLabel("/Users/alice/projects/aether"))
        assertEquals("repo", sessionDirectoryLabel("/home/bob/repo/"))
        assertEquals("x", sessionDirectoryLabel("C:\\Users\\alice\\work\\x"))
    }

    @Test
    fun directoryLabelHidesUsernamesWhenThereIsNoProjectDepth() {
        // 只有家目录时，末级段就是用户名——不能显示。
        assertEquals("", sessionDirectoryLabel("/Users/alice"))
        assertEquals("", sessionDirectoryLabel("/home/bob"))
        assertEquals("", sessionDirectoryLabel("C:\\Users\\alice"))
        assertEquals("", sessionDirectoryLabel(""))
    }

    @Test
    fun parsesSessionsAndAgentDirectory() {
        val body = """
            {"sessions":[
              {"key":"k1","id":"s1","agent":"claude-code","name":"Claude Code","title":"重构网关",
               "cwd":"/Users/alice/projects/aether","start":1730000000,"last":1730000600,
               "requests":7,"messages":12,"tool_calls":3,"cost":0.42}
            ],"agents":{"claude-code":"Claude Code"}}
        """.trimIndent()
        val list = parseSessions(body)
        assertEquals(1, list.sessions.size)
        val row = list.sessions.single()
        assertEquals("k1", row.key)
        assertEquals("s1", row.id)
        assertEquals("claude-code", row.agent)
        assertEquals("Claude Code", row.agentName)
        assertEquals("重构网关", row.title)
        assertEquals("aether", row.directory)
        assertTrue(!row.directory.contains("alice"))
        assertEquals(1730000600L, row.lastAt)
        assertEquals(7, row.requests)
        assertEquals(12, row.messages)
        assertEquals(3, row.toolCalls)
        assertEquals(0.42, row.cost, 0.0001)
        assertEquals(listOf(AgentOption("claude-code", "Claude Code")), list.agents)
    }

    @Test
    fun emptyStoreIsAnEmptyListNotAnError() {
        // 手机上必然是这个形状：服务端扫不到任何 Agent 会话目录。
        val list = parseSessions("""{"sessions":[],"agents":{}}""")
        assertTrue(list.sessions.isEmpty())
        assertTrue(list.agents.isEmpty())
    }

    @Test
    fun dropsRowsWithoutStableKeys() {
        val list = parseSessions(
            """{"sessions":[{"id":"no-key"},{"key":"k2","agent":"codex"}]}""",
        )
        assertEquals(listOf("k2"), list.sessions.map { it.key })
    }

    @Test
    fun filterQueryEncodesAndClamps() {
        val q = sessionFilterQuery(agent = "claude code", query = "a&b", limit = 5000)
        assertTrue(q.contains("agent=claude+code") || q.contains("agent=claude%20code"))
        assertTrue(q.contains("q=a%26b"))
        assertTrue("limit 必须夹到 1000", q.contains("limit=1000"))
        assertTrue(sessionFilterQuery("", "", 0).contains("limit=1"))
    }

    @Test
    fun malformedBodyRaisesInvalidResponse() {
        val error = runCatching { parseSessions("not-json") }.exceptionOrNull()
        assertTrue(error is ManagementApiException)
        assertEquals("invalid_response", (error as ManagementApiException).code)
    }
}
