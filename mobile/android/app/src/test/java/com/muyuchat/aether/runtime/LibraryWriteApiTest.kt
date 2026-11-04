package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 资源库新增（指令集 / 技能 / 子代理）。
 *
 * 两个**故意不做**的边界，都有断言盯着：
 * 1. **MCP 不提供保存**：`POST /api/library/mcp` 的 `config` 是原始 JSON，而列表下发的是
 *    **掩码后**的 config（`env`/`headers`/`args` 都被打码）。保存接口没有"保持不变"哨兵，
 *    回写就等于把 `***` 写进真实配置。所以客户端**根本没有**构造 config 的能力。
 * 2. **编辑不做**：没有按 id 更新的端点，`POST` 一律是"新增一条"。预填需要把 content
 *    解析进手机模型，本轮不扩大这个面。
 */
class LibraryWriteApiTest {
    @Test
    fun promptBodyHasExactlyThreeFields() {
        val json = org.json.JSONObject(
            buildLibrarySaveJson(
                LibraryDraft(
                    kind = LibraryKind.PROMPT,
                    name = "代码评审",
                    description = "评审清单",
                    content = "逐条检查…",
                ),
            ),
        )
        assertEquals("代码评审", json.getString("name"))
        assertEquals("评审清单", json.getString("description"))
        assertEquals("逐条检查…", json.getString("content"))
        assertEquals(3, json.length())
    }

    @Test
    fun agentBodyCarriesToolsAndModel() {
        val json = org.json.JSONObject(
            buildLibrarySaveJson(
                LibraryDraft(
                    kind = LibraryKind.SUBAGENT,
                    name = "检索助手",
                    description = "只读检索",
                    content = "你是一个检索助手",
                    tools = "Read,Grep",
                    model = "workbuddy-cn/glm-5.3-flash",
                ),
            ),
        )
        assertEquals("检索助手", json.getString("name"))
        assertEquals("你是一个检索助手", json.getString("content"))
        assertEquals("Read,Grep", json.getString("tools"))
        assertEquals("workbuddy-cn/glm-5.3-flash", json.getString("model"))
    }

    @Test
    fun promptBodyNeverCarriesToolsOrModel() {
        // 指令集/技能的 handler 只认三个字段；多塞字段会被忽略，但别让它看起来像生效。
        val draft = LibraryDraft(
            kind = LibraryKind.SKILL,
            name = "技能",
            content = "内容",
            tools = "Read",
            model = "m",
        )
        val json = org.json.JSONObject(buildLibrarySaveJson(draft))
        assertTrue(!json.has("tools"))
        assertTrue(!json.has("model"))
    }

    @Test
    fun mcpDraftIsRejectedAndCannotCarryConfig() {
        val draft = LibraryDraft(
            kind = LibraryKind.MCP,
            name = "某 MCP",
            content = "{\"env\":{\"API_KEY\":\"***\"}}",
        )
        val error = runCatching { buildLibrarySaveJson(draft) }.exceptionOrNull()
        assertTrue("MCP 必须在构造请求前就被拒绝，实际 $error", error is ManagementApiException)
        assertEquals("mcp_desktop_only", (error as ManagementApiException).code)
        // 且没有 config 字段能出现在任何返回里
        assertTrue(!(error as ManagementApiException).message.contains("API_KEY"))
    }

    @Test
    fun nameAndContentAreRequired() {
        val problems = validateLibraryDraft(
            LibraryDraft(kind = LibraryKind.PROMPT, name = "  ", description = "", content = ""),
        )
        assertEquals("名称不能为空", problems.name)
        assertEquals("内容不能为空", problems.content)

        val ok = validateLibraryDraft(
            LibraryDraft(kind = LibraryKind.PROMPT, name = "n", description = "", content = "c"),
        )
        assertNull(ok.name)
        assertNull(ok.content)
    }

    @Test
    fun nameIsTrimmedButContentIsKeptVerbatim() {
        val draft = LibraryDraft(
            kind = LibraryKind.PROMPT,
            name = "  名称  ",
            description = "  说明  ",
            content = "  第一行\n第二行  ",
        ).normalized()
        assertEquals("名称", draft.name)
        assertEquals("说明", draft.description)
        // 正文保留原样：它是用户写的指令文本，前后空白可能是刻意的
        assertEquals("  第一行\n第二行  ", draft.content)
    }

    @Test
    fun savePathIsPerKindAndNeverMCP() {
        assertEquals("/api/library/prompts", librarySavePath(LibraryKind.PROMPT))
        assertEquals("/api/library/skills", librarySavePath(LibraryKind.SKILL))
        assertEquals("/api/library/agents", librarySavePath(LibraryKind.SUBAGENT))
        val error = runCatching { librarySavePath(LibraryKind.MCP) }.exceptionOrNull()
        assertEquals("mcp_desktop_only", (error as ManagementApiException).code)
    }
}
