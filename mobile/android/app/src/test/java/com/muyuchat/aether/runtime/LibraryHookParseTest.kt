package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/**
 * 只读 Hook 解析（`GET /api/library` 的 `hooks[]`，`library_api.go:244-250`）。
 *
 * 契约：坏 JSON 不在这里兜底——由 `parseLibrary` 统一转成 `invalid_response`。
 */
class LibraryHookParseTest {

    @Test
    fun parsesHooksWithAgentsAndTimeout() {
        val array = JSONArray(
            """
            [
              {"id":"h1","name":"保存时格式化","event":"PostToolUse","matcher":"Write|Edit",
               "command":"gofmt -w .","timeout":30,"agents":["claude_code","codex"],
               "updated_at":1700000000},
              {"name":"仅名称 Hook","event":"SessionStart","command":"echo hi","timeout":5,
               "agents":[],"created_at":1690000000}
            ]
            """.trimIndent(),
        )
        val hooks = parseHooks(array)
        assertEquals(2, hooks.size)

        val first = hooks[0]
        assertEquals("h1", first.id)
        assertEquals("保存时格式化", first.name)
        assertEquals("PostToolUse", first.event)
        assertEquals("Write|Edit", first.matcher)
        assertEquals("gofmt -w .", first.command)
        assertEquals(30, first.timeout)
        assertEquals(listOf("claude_code", "codex"), first.agents)
        assertEquals(1_700_000_000L, first.updatedAt)

        val second = hooks[1]
        assertEquals("", second.id)
        assertEquals("仅名称 Hook", second.name)
        assertEquals(5, second.timeout)
        assertTrue(second.agents.isEmpty())
        // updated_at 缺失 → 回落 created_at。
        assertEquals(1_690_000_000L, second.updatedAt)
    }

    @Test
    fun nullAndEmptyArrayYieldEmptyList() {
        assertTrue(parseHooks(null).isEmpty())
        assertTrue(parseHooks(JSONArray()).isEmpty())
    }

    @Test
    fun missingFieldsUseDefaults() {
        val hooks = parseHooks(JSONArray("""[{"name":"只有名字"}]"""))
        assertEquals(1, hooks.size)
        val hook = hooks.single()
        assertEquals("", hook.id)
        assertEquals("只有名字", hook.name)
        assertEquals("", hook.event)
        assertEquals("", hook.matcher)
        assertEquals("", hook.command)
        assertEquals(0, hook.timeout)
        assertTrue(hook.agents.isEmpty())
        assertEquals(0L, hook.updatedAt)
    }

    @Test
    fun emptyAgentsArrayIsEmptyListNotNull() {
        val hooks = parseHooks(JSONArray("""[{"name":"h","agents":[]}]"""))
        assertEquals(1, hooks.size)
        assertEquals(emptyList<String>(), hooks.single().agents)
    }

    @Test
    fun invalidPayloadSurfacesInvalidResponse() {
        try {
            parseLibrary("not a json object")
            fail("坏回包应抛 invalid_response")
        } catch (e: ManagementApiException) {
            assertEquals("invalid_response", e.code)
        }
        try {
            // 合法 JSON 但不是对象（数组）：同样由 parseLibrary 归一成 invalid_response。
            parseLibrary("[]")
            fail("非对象回包应抛 invalid_response")
        } catch (e: ManagementApiException) {
            assertEquals("invalid_response", e.code)
        }
    }
}
