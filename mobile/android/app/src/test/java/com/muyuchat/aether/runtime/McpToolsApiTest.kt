package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * MCP 工具开关（桌面 `library.js:490-511`）。
 *
 * - 读清单：`POST /api/library/mcp/check {names:[name], fresh:true}`（`gateway.go:1002` → `mcp_check.go:168+`）
 * - 保存：`POST /api/library/servers/tools {name, names:[停用]}`（`gateway.go:1050` → `mcp_tools.go:33+`）
 * - 条目的 `disabledTools` 来自 `GET /api/library` 的 `mcp[]` 行（`library_api.go:284-288`）
 */
class McpToolsApiTest {
    @Test
    fun checkBodyAsksForOneServerFresly() {
        assertEquals("""{"names":["github"],"fresh":true}""", buildMcpCheckJson("  github "))
    }

    @Test
    fun toolsBodyTrimsDedupesAndDropsBlanks() {
        val body = buildMcpToolsJson("github", listOf(" a ", "b", "a", "  ", "b"))
        assertTrue(body, body.contains("\"name\":\"github\""))
        assertTrue(body, body.contains("\"names\":[\"a\",\"b\"]"))
        // 全部启用就是空数组（服务端会把 DisabledTools 清空）
        assertTrue(buildMcpToolsJson("github", emptyList()).contains("\"names\":[]"))
    }

    @Test
    fun parsesServersEnvelopeAndFlatShape() {
        // 桌面两种形态都兼容（library.js:506）
        val nested = parseMcpToolList(
            """{"servers":{"github":{"state":"ok","toolList":["list_issues","create_issue"],"tools":2}}}""",
            "github",
        )
        assertEquals(listOf("list_issues", "create_issue"), nested.tools)
        assertEquals("", nested.why)

        val flat = parseMcpToolList("""{"github":{"toolList":["x"]}}""", "github")
        assertEquals(listOf("x"), flat.tools)
    }

    @Test
    fun capturesWhyAndDetailForEmptyLists() {
        val result = parseMcpToolList(
            """{"servers":{"github":{"state":"error","toolList":[],"why":"未登录","detail":"需要先授权"}}}""",
            "github",
        )
        assertTrue(result.tools.isEmpty())
        assertEquals("未登录", result.why)
        assertEquals("需要先授权", result.detail)
    }

    @Test
    fun missingServerOrMalformedBodyIsHandled() {
        // 服务端没返回这台服务器：空清单 + 空原因（页面据此说"服务器没有返回工具清单"）
        val missing = parseMcpToolList("""{"servers":{}}""", "github")
        assertTrue(missing.tools.isEmpty())
        assertEquals("", missing.why)

        for (body in listOf("not json", "", "[]")) {
            val error = runCatching { parseMcpToolList(body, "github") }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }

    @Test
    fun librarySnapshotCarriesDisabledToolsForMcpRowsOnly() {
        val snapshot = parseLibrary(
            """
            {"mcp":[{"id":"m1","name":"github","disabledTools":["a","b"]},
                    {"id":"m2","name":"filesystem"}],
             "prompts":[{"id":"p1","name":"review","disabledTools":["should-not-leak"]}],
             "skills_raw":[],"subagents":[]}
            """.trimIndent(),
        )
        val mcp = snapshot.entries.filter { it.kind == LibraryKind.MCP }
        assertEquals(listOf("a", "b"), mcp[0].disabledTools)
        assertTrue(mcp[1].disabledTools.isEmpty())
        // 其它类型不读这个字段
        val prompt = snapshot.entries.first { it.kind == LibraryKind.PROMPT }
        assertTrue(prompt.disabledTools.isEmpty())
    }
}
