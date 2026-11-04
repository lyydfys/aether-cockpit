package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 资源库只读投影 + 删除。
 *
 * 两条红线：
 * 1. **MCP 的完整配置（`mcp_servers[].config`，里面可能有 env 密钥）不解析、不展示** ——
 *    页面只认服务端下发的那套轻量行（`mcp`：id/name/kind）。
 * 2. 删除必须用对应仓库的端点：prompts / skills / mcp / agents 是四个不同的删除 handler，
 *    打错端点会得到 404 或者删错东西。
 */
class LibraryApiTest {
    private val body = """
        {
          "prompts":[{"id":"p1","name":"共享指令","content":"秘密提示词正文","created_at":100}],
          "skills_raw":[{"id":"s1","name":"代码审查","content":"技能正文","created_at":200}],
          "mcp":[{"id":"m1","name":"fetch","title":"fetch","kind":"mcp","updated_at":300}],
          "subagents":[{"id":"a1","name":"审查员","title":"审查员","kind":"subagent","updated_at":400}],
          "mcp_servers":[{"id":"m1","name":"fetch","config":{"command":"uvx","env":{"API_KEY":"sk-secret"}},"created_at":300}]
        }
    """.trimIndent()

    @Test
    fun mergesTheFourStoresIntoOneListWithExplicitKinds() {
        val snapshot = parseLibrary(body)
        assertEquals(4, snapshot.entries.size)
        val byId = snapshot.entries.associateBy { it.id }
        assertEquals(LibraryKind.PROMPT, byId.getValue("p1").kind)
        assertEquals(LibraryKind.SKILL, byId.getValue("s1").kind)
        assertEquals(LibraryKind.MCP, byId.getValue("m1").kind)
        assertEquals(LibraryKind.SUBAGENT, byId.getValue("a1").kind)
        assertEquals("共享指令", byId.getValue("p1").name)
        assertEquals(200L, byId.getValue("s1").updatedAt)
    }

    @Test
    fun neverSurfacesMcpConfigOrEntryContent() {
        // 结构保证：LibraryEntry 没有 config / content 字段，解析时可以断言只出现名字与时间。
        val text = parseLibrary(body).entries.joinToString("|") { "${it.id}:${it.name}" }
        assertTrue("不应出现 env 里的密钥", !text.contains("sk-secret"))
        assertTrue("不应出现技能正文", !text.contains("技能正文"))
        assertTrue("不应出现提示词正文", !text.contains("秘密提示词正文"))
    }

    @Test
    fun dropsRowsWithoutId() {
        val snapshot = parseLibrary("""{"prompts":[{"name":"无 id"}],"mcp":[{"id":"ok","name":"x"}]}""")
        assertEquals(listOf("ok"), snapshot.entries.map { it.id })
    }

    @Test
    fun emptyLibraryIsNormal() {
        val snapshot = parseLibrary("""{"prompts":[],"skills_raw":[],"mcp":[],"subagents":[]}""")
        assertTrue(snapshot.entries.isEmpty())
    }

    @Test
    fun deleteUsesTheStoreSpecificEndpoint() {
        assertEquals("/api/library/prompts/p1", libraryDeletePath(LibraryKind.PROMPT, "p1"))
        assertEquals("/api/library/skills/s1", libraryDeletePath(LibraryKind.SKILL, "s1"))
        assertEquals("/api/library/mcp/m1", libraryDeletePath(LibraryKind.MCP, "m1"))
        // 子代理的删除端点是 agents，不是 subagents。
        assertEquals("/api/library/agents/a1", libraryDeletePath(LibraryKind.SUBAGENT, "a1"))
    }

    @Test
    fun deletePathEncodesTheId() {
        val path = libraryDeletePath(LibraryKind.SKILL, "a b/c")
        assertTrue("id 必须做 URL 编码: $path", path.contains("a+b%2Fc") || path.contains("a%20b%2Fc"))
    }

    @Test
    fun malformedBodyRaisesInvalidResponse() {
        val error = runCatching { parseLibrary("not-json") }.exceptionOrNull()
        assertTrue(error is ManagementApiException)
        assertEquals("invalid_response", (error as ManagementApiException).code)
    }
}
